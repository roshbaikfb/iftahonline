-- Orders carry their coupon's discount (ADR-0042).
--
-- discount_minor comes off the items' subtotal (never off delivery or
-- the COD fee); coupon_code is the code the order took, kept as it was.
-- The total's rule gains the discount: old code writes no discount
-- (default 0), which the new rule accepts, so this is expand only. As in
-- 00029, the old rule is dropped from the parent and from each attached
-- partition. The new checks are NOT VALID (migsafe): rows before this
-- migration have no discount and satisfy them; a later migration
-- validates them.

-- +goose Up
ALTER TABLE orders ADD COLUMN IF NOT EXISTS discount_minor bigint NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS coupon_code text;
ALTER TABLE orders ADD CONSTRAINT orders_discount_range CHECK (discount_minor >= 0 AND discount_minor <= subtotal_minor) NOT VALID;
ALTER TABLE orders ADD CONSTRAINT orders_coupon_code_format CHECK (coupon_code ~ '^[A-Z0-9_-]{3,32}$') NOT VALID;
ALTER TABLE orders ADD CONSTRAINT orders_total_with_discount
  CHECK (total_minor = subtotal_minor - discount_minor + shipping_minor + cod_fee_minor) NOT VALID;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_total_with_fees;
-- +goose StatementBegin
DO $$
DECLARE p record;
BEGIN
  FOR p IN SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
           WHERE i.inhparent = 'public.orders'::regclass LOOP
    EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT IF EXISTS orders_total_with_fees', p.relname);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE orders ADD CONSTRAINT orders_total_with_fees CHECK (total_minor = subtotal_minor + shipping_minor + cod_fee_minor) NOT VALID;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_total_with_discount;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_coupon_code_format;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_discount_range;
ALTER TABLE orders DROP COLUMN IF EXISTS coupon_code;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_minor;
