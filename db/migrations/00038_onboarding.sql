-- The setup wizard (ADR-0034): one row per store made since the wizard,
-- written by sign-up in the same transaction as the store. Stores made
-- before it have no row, and so no wizard.
--
-- template_seq counts the merchant's choices: each choice enqueues an
-- apply job carrying the new value, and a job whose value is no longer
-- current does nothing, so the last choice wins however the jobs run.
-- Expand only: a new table.

-- +goose Up
CREATE TABLE store_onboarding (
  store_id         uuid        PRIMARY KEY REFERENCES stores (id) ON DELETE CASCADE,
  activity         text        CHECK (activity IN ('clothing', 'electronics', 'beauty', 'perfume', 'kids', 'food', 'home', 'other')),
  look             text        CHECK (look IN ('classic', 'modern', 'soft')),
  color            text        CHECK (color ~ '^#[0-9a-f]{6}$'),
  template_status  text        NOT NULL DEFAULT 'none' CHECK (template_status IN ('none', 'preparing', 'ready', 'failed')),
  template_seq     integer     NOT NULL DEFAULT 0 CHECK (template_seq >= 0),
  template_version integer     CHECK (template_version > 0),
  completed_at     timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (num_nulls(activity, look, color) IN (0, 3)),
  CHECK (template_status = 'none' OR activity IS NOT NULL)
);

ALTER TABLE store_onboarding ENABLE ROW LEVEL SECURITY;
ALTER TABLE store_onboarding FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON store_onboarding
  USING (store_id = app.current_store_id()) WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE store_onboarding;
