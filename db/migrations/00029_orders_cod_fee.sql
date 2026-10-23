-- Orders carry the cash-on-delivery fee (ADR-0025).
--
-- Shipping rates exist now: shipping_minor is the quoted rate (it was 0,
-- ADR-0021 §8) and cod_fee_minor the COD fee, both fixed when the order
-- is placed. The total includes both.
--
-- Expand only: old code writes no cod_fee_minor (default 0), which the
-- new CHECK accepts. Partitions are built standalone and attached
-- (00022), so each may hold its own local copy of the old CHECK, which
-- dropping it on the parent leaves behind: they are dropped one by one.
-- Validating the new CHECK scans every partition under its lock; orders
-- are small enough today (pre-launch) for that to be brief.

-- +goose Up
ALTER TABLE orders ADD COLUMN cod_fee_minor bigint NOT NULL DEFAULT 0 CHECK (cod_fee_minor >= 0);
ALTER TABLE orders ADD CONSTRAINT orders_total_with_fees CHECK (total_minor = subtotal_minor + shipping_minor + cod_fee_minor);
ALTER TABLE orders DROP CONSTRAINT orders_total;
-- +goose StatementBegin
DO $$
DECLARE p record;
BEGIN
  FOR p IN SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
           WHERE i.inhparent = 'public.orders'::regclass LOOP
    EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT IF EXISTS orders_total', p.relname);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE orders ADD CONSTRAINT orders_total CHECK (total_minor = subtotal_minor + shipping_minor);
ALTER TABLE orders DROP CONSTRAINT orders_total_with_fees;
ALTER TABLE orders DROP COLUMN cod_fee_minor;
