-- name: InsertCheckout :exec
INSERT INTO billing_checkouts (store_id, id, account_id, invoice_id, reference, currency, amount_minor)
VALUES (@store_id, @id, @account_id, @invoice_id, @reference, @currency, @amount_minor);

-- name: SetCheckoutOrder :exec
-- Paymob's order, once known (from the intention, or the transaction
-- callback): card token callbacks name only it.
UPDATE billing_checkouts SET paymob_order_id = @paymob_order_id, updated_at = now()
WHERE store_id = @store_id AND reference = @reference AND paymob_order_id IS NULL;

-- name: GetCheckout :one
-- By our reference or Paymob's order.
SELECT id, account_id, invoice_id, reference, currency, amount_minor, status FROM billing_checkouts
WHERE store_id = @store_id
  AND ((@reference::text <> '' AND reference = @reference::text) OR (@paymob_order_id::text <> '' AND paymob_order_id = @paymob_order_id::text));

-- name: SetCheckoutStatus :exec
UPDATE billing_checkouts SET status = @status, updated_at = now() WHERE store_id = @store_id AND id = @id;

-- name: CheckoutStore :one
-- The zero UUID when no checkout matches.
SELECT coalesce(app.billing_checkout_store(@reference::text, @paymob_order_id::text), '00000000-0000-0000-0000-000000000000')::uuid AS store_id;

-- name: ListOpenInvoices :many
SELECT id, number, period, currency, total_minor, due_at, attempts, next_attempt_at FROM invoices
WHERE account_id = @account_id AND status = 'open' ORDER BY issued_at DESC LIMIT 24;

-- name: GetPlanNames :one
SELECT name_ar, name_en FROM plans WHERE id = @id;
