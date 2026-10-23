-- How an order is paid, and where its money is (ADR-0029).
--
-- Cash-on-delivery orders (every order so far) are 'unpaid' until the
-- carrier collects; card orders are 'awaiting' the gateway, then paid
-- (and possibly refunded, in part or in full). Expand only: old writers
-- leave the defaults, which describe every existing order.

-- +goose Up
ALTER TABLE orders
  ADD COLUMN payment_method text NOT NULL DEFAULT 'cod' CHECK (payment_method IN ('cod', 'card')),
  ADD COLUMN payment_status text NOT NULL DEFAULT 'unpaid'
    CHECK (payment_status IN ('unpaid', 'awaiting', 'paid', 'partially_refunded', 'refunded'));

-- +goose Down
ALTER TABLE orders DROP COLUMN payment_status, DROP COLUMN payment_method;
