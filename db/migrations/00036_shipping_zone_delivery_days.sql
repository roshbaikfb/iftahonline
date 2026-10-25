-- Shipping zones say how long delivery takes ("2–3 days"), for the
-- product page and the quote.
--
-- Expand only: two nullable columns. Old code neither reads nor writes
-- them; a zone without days stays as it was. Both are set or neither,
-- and the range check matches domain.MaxDeliveryDays.

-- +goose Up
ALTER TABLE shipping_zones
  ADD COLUMN eta_min_days smallint CHECK (eta_min_days BETWEEN 0 AND 30),
  ADD COLUMN eta_max_days smallint CHECK (eta_max_days BETWEEN 0 AND 30),
  ADD CONSTRAINT shipping_zones_eta CHECK (num_nulls(eta_min_days, eta_max_days) IN (0, 2) AND eta_min_days <= eta_max_days); -- migsafe:allow shipping_zones is small (at most 27 zones per store)

-- +goose Down
ALTER TABLE shipping_zones
  DROP CONSTRAINT shipping_zones_eta,
  DROP COLUMN eta_max_days,
  DROP COLUMN eta_min_days;
