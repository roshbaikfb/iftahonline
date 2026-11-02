-- Validates the invoice lines' checks 00083 added NOT VALID (expand,
-- then validate: VALIDATE takes a lighter lock than ADD).

-- +goose Up
ALTER TABLE invoice_lines VALIDATE CONSTRAINT invoice_lines_kind_check;
ALTER TABLE invoice_lines VALIDATE CONSTRAINT invoice_lines_check;

-- +goose Down
SELECT 1;
