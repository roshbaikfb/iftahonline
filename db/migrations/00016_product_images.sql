-- Product images (ADR-0012). A row is created with a presigned upload,
-- then a job validates and re-encodes the original into a clean master
-- (object_key); imgproxy serves resized WebP/AVIF from the master through
-- signed URLs. Object keys always start with stores/<store_id>/.

-- +goose Up
CREATE TABLE product_images (
  id             uuid        PRIMARY KEY,
  store_id       uuid        NOT NULL,
  product_id     uuid        NOT NULL,
  status         text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'ready', 'failed')),
  upload_key     text        NOT NULL,
  object_key     text,
  format         text        CHECK (format IN ('jpeg', 'png')),
  width          integer     CHECK (width > 0),
  height         integer     CHECK (height > 0),
  bytes          bigint      CHECK (bytes > 0),
  alt            text        NOT NULL DEFAULT '' CHECK (char_length(alt) <= 200),
  position       integer     NOT NULL DEFAULT 0 CHECK (position >= 0),
  failure_reason text        NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT product_images_ready_has_master CHECK (status <> 'ready' OR (object_key IS NOT NULL AND width IS NOT NULL AND height IS NOT NULL)),
  CONSTRAINT product_images_product_fkey FOREIGN KEY (store_id, product_id)
    REFERENCES products (store_id, id) ON DELETE CASCADE
);
-- Leads with store_id and covers the (store_id, product_id) FK.
CREATE INDEX product_images_store_product ON product_images (store_id, product_id, position, id);

ALTER TABLE product_images ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_images FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_images
  USING (store_id = app.current_store_id())
  WITH CHECK (store_id = app.current_store_id());

-- +goose Down
DROP TABLE product_images;
