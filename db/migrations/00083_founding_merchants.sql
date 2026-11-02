-- The founding-merchants program (S4b-2, approved 2026-11-02): the first
-- paying merchants keep a discount for life and their price, while they
-- stay subscribed.
--
-- founding_program is platform data (one row) the console writes as
-- app_admin. founding_members is the stores' (RLS on store_id): billing
-- reads and writes a store's membership in its tenant transaction; what
-- crosses stores (the seat count, one store per owner, joining, losing,
-- the console's list) is in SECURITY DEFINER functions.
--
-- Invoices gain a line kind, founding_discount, negative like a fee
-- credit. The old line checks are dropped and their replacements added
-- NOT VALID here, validated in 00084 (expand, then validate).

-- +goose Up
CREATE TABLE founding_program (
  id           int         PRIMARY KEY CHECK (id = 1),
  enabled      boolean     NOT NULL DEFAULT false,
  seats        int         NOT NULL DEFAULT 100 CHECK (seats BETWEEN 1 AND 10000),
  discount_bps int         NOT NULL DEFAULT 2000 CHECK (discount_bps BETWEEN 100 AND 9000),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
INSERT INTO founding_program (id) VALUES (1);
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON founding_program FROM app_user;
GRANT SELECT, UPDATE ON founding_program TO app_admin;

CREATE TABLE founding_members (
  store_id     uuid        PRIMARY KEY REFERENCES stores (id),
  -- The store's owner when it joined: one founding store per owner.
  owner_id     uuid        NOT NULL,
  seat         int         NOT NULL CHECK (seat >= 1),
  -- Kept from the day it joined: a later change of the program's
  -- discount does not reach existing members.
  discount_bps int         NOT NULL CHECK (discount_bps BETWEEN 100 AND 9000),
  -- The locked price: the plan, cycle and amount it renews at at most.
  currency     char(3)     NOT NULL CHECK (currency IN ('EGP', 'SAR', 'AED')),
  plan_code    text        NOT NULL,
  cycle        text        NOT NULL CHECK (cycle IN ('monthly', 'annual')),
  locked_minor bigint      NOT NULL CHECK (locked_minor >= 0),
  joined_at    timestamptz NOT NULL DEFAULT now(),
  lost_at      timestamptz,
  lost_reason  text        CHECK (lost_reason IN ('cancelled', 'unpaid')),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CHECK ((lost_at IS NULL) = (lost_reason IS NULL))
);
-- Seats are never reused; an owner joins once.
CREATE UNIQUE INDEX founding_members_seat ON founding_members (seat);
CREATE UNIQUE INDEX founding_members_owner ON founding_members (owner_id);
ALTER TABLE founding_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE founding_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON founding_members
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());
-- The functions below run as the table's owner.
CREATE POLICY founding_functions ON founding_members TO app_owner USING (true) WITH CHECK (true);
REVOKE INSERT, DELETE, TRUNCATE ON founding_members FROM app_user;

-- founding_open is the program's discount for a merchant subscribing now
-- (owned by p_owner), or 0: off, full, or the owner already joined.
-- +goose StatementBegin
CREATE FUNCTION app.founding_open(p_owner uuid)
  RETURNS int
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT CASE WHEN p.enabled
      AND (SELECT count(*) FROM public.founding_members) < p.seats
      AND NOT EXISTS (SELECT 1 FROM public.founding_members m WHERE m.owner_id = p_owner)
    THEN p.discount_bps ELSE 0 END
  FROM public.founding_program p WHERE p.id = 1
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.founding_open(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.founding_open(uuid) TO app_user;

-- founding_join gives the store the next seat, once its first
-- subscription invoice is paid; 0 when it cannot join (off, full, the
-- owner or the store already joined). Seats are taken one at a time.
-- +goose StatementBegin
CREATE FUNCTION app.founding_join(p_store uuid, p_owner uuid, p_currency char(3), p_plan_code text, p_cycle text, p_locked bigint)
  RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
DECLARE
  prog public.founding_program;
  taken int;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('founding_members'));
  SELECT * INTO prog FROM public.founding_program WHERE id = 1;
  SELECT count(*) INTO taken FROM public.founding_members;
  IF NOT prog.enabled OR taken >= prog.seats
     OR EXISTS (SELECT 1 FROM public.founding_members WHERE owner_id = p_owner OR store_id = p_store) THEN
    RETURN 0;
  END IF;
  INSERT INTO public.founding_members (store_id, owner_id, seat, discount_bps, currency, plan_code, cycle, locked_minor)
  VALUES (p_store, p_owner, taken + 1, prog.discount_bps, p_currency, p_plan_code, p_cycle, p_locked);
  RETURN taken + 1;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.founding_join(uuid, uuid, char, text, text, bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.founding_join(uuid, uuid, char, text, text, bigint) TO app_user;

-- founding_sweep ends the membership of stores that left their
-- subscription (now on a plan that is not a subscription, or closed) or
-- whose renewal is unpaid past p_unpaid_before; it returns how many.
-- +goose StatementBegin
CREATE FUNCTION app.founding_sweep(p_now timestamptz, p_unpaid_before timestamptz)
  RETURNS int
  LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
DECLARE
  n int;
BEGIN
  WITH lost AS (
    SELECT m.store_id,
      CASE WHEN p.kind <> 'subscription' OR s.status = 'cancelled' THEN 'cancelled' ELSE 'unpaid' END AS reason
    FROM public.founding_members m
    JOIN public.subscriptions s ON s.store_id = m.store_id
    JOIN public.plans p ON p.id = s.plan_id
    LEFT JOIN public.invoices i ON i.id = s.renewal_invoice_id
    WHERE m.lost_at IS NULL
      AND (p.kind <> 'subscription' OR s.status = 'cancelled'
        OR (i.status = 'open' AND i.due_at < p_unpaid_before))
  )
  UPDATE public.founding_members m SET lost_at = p_now, lost_reason = lost.reason, updated_at = p_now
  FROM lost WHERE m.store_id = lost.store_id;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.founding_sweep(timestamptz, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.founding_sweep(timestamptz, timestamptz) TO app_user;

-- founding_list is every member for the console, by seat.
-- +goose StatementBegin
CREATE FUNCTION app.founding_list()
  RETURNS TABLE (store_id uuid, seat int, discount_bps int, currency char(3), plan_code text, cycle text,
                 locked_minor bigint, joined_at timestamptz, lost_at timestamptz, lost_reason text)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT store_id, seat, discount_bps, currency, plan_code, cycle, locked_minor, joined_at, lost_at, lost_reason
  FROM public.founding_members ORDER BY seat LIMIT 10000
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.founding_list() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.founding_list() TO app_admin;

-- founding_seats is the program as the site shows it: on, seats and how
-- many are taken (lost seats stay taken).
-- +goose StatementBegin
CREATE FUNCTION app.founding_seats()
  RETURNS TABLE (enabled boolean, seats int, taken int, discount_bps int)
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, public
AS $$
  SELECT p.enabled, p.seats, (SELECT count(*)::int FROM public.founding_members), p.discount_bps
  FROM public.founding_program p WHERE p.id = 1
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION app.founding_seats() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION app.founding_seats() TO app_user, app_admin;

-- The discount line on invoices.
ALTER TABLE invoice_lines DROP CONSTRAINT IF EXISTS invoice_lines_kind_check;
ALTER TABLE invoice_lines DROP CONSTRAINT IF EXISTS invoice_lines_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_kind_check
  CHECK (kind IN ('subscription', 'sell_fees', 'fee_credit', 'founding_discount')) NOT VALID;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_check
  CHECK ((kind IN ('fee_credit', 'founding_discount')) = (amount_minor < 0)) NOT VALID;

-- +goose Down
ALTER TABLE invoice_lines DROP CONSTRAINT IF EXISTS invoice_lines_check;
ALTER TABLE invoice_lines DROP CONSTRAINT IF EXISTS invoice_lines_kind_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_kind_check CHECK (kind IN ('subscription', 'sell_fees', 'fee_credit')) NOT VALID;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_check CHECK ((kind = 'fee_credit') = (amount_minor < 0)) NOT VALID;
DROP FUNCTION IF EXISTS app.founding_seats();
DROP FUNCTION IF EXISTS app.founding_list();
DROP FUNCTION IF EXISTS app.founding_sweep(timestamptz, timestamptz);
DROP FUNCTION IF EXISTS app.founding_join(uuid, uuid, char, text, text, bigint);
DROP FUNCTION IF EXISTS app.founding_open(uuid);
DROP TABLE IF EXISTS founding_members;
DROP TABLE IF EXISTS founding_program;
