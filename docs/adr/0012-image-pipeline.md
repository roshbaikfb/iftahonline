# ADR-0012: Product image pipeline

- Status: Accepted
- Date: 2026-10-19

## Context

Merchants upload product photos from the dashboard and the mobile app.
Uploads are hostile input:
- SVG with scripts;
- HTML renamed to `.jpg`;
- decompression bombs;
- EXIF blocks that carry GPS coordinates of the merchant's home.

The storefront must serve modern formats (AVIF, WebP) in a few sizes from
the edge cache. Our servers should not proxy upload bytes, and every
binary is `CGO_ENABLED=0` (ADR-0001).

## Decision

1. **Uploads go straight to object storage, under a presigned POST
   policy.**
   - The policy fixes the key, the declared content type and a
     `content-length-range` (`MEDIA_MAX_UPLOAD_BYTES`, default 15 MiB).
     The storage service enforces it, before anything reaches us.
   - The API creates a `pending` row first; the client then calls
     `complete`. We do not rely on bucket notifications, which differ
     between providers.
   - Keys are `stores/<store_id>/uploads/<image_id>` for originals and
     `stores/<store_id>/images/<image_id>.<jpg|png>` for masters.
2. **A River job (`catalog.process_image`) validates and re-encodes
   every original.**
   - The real type is sniffed from the bytes. Only JPEG, PNG, GIF (first
     frame) and WebP are decoded; everything else is rejected, so raw
     SVG never reaches storage.
   - Width × height is checked from the header before decoding
     (`MEDIA_MAX_IMAGE_PIXELS`, default 25 MP; at most 16384 px a side).
   - The EXIF orientation is applied to JPEGs.
   - The image is re-encoded with the standard library: JPEG q90 when
     opaque, PNG when it has transparency. Re-encoding drops every
     metadata block (EXIF/GPS, XMP, ICC, comments).
   - Pure Go, no cgo. The master is a clean, upright, metadata-free
     file.
   - A bad upload ends as `failed` with a stable reason code
     (`unsupported_type`, `file_too_large`, `too_many_pixels`,
     `corrupt_image`, `upload_missing`). Storage errors are retried.
3. **Delivery is imgproxy, behind the CDN, through signed URLs only.**
   - imgproxy converts masters to AVIF and WebP at four fixed sizes
     (thumb 160, small 400, medium 800, large 1600; fit, never enlarge).
   - URLs are signed with HMAC-SHA256 (`IMGPROXY_KEY`, `IMGPROXY_SALT`),
     so nobody can make it fetch other sources or render arbitrary
     sizes. `IMGPROXY_ALLOWED_SOURCES` limits it to our bucket.
   - Both formats are separate URLs (`<picture>`), so the CDN never
     varies on `Accept`.
   - We rejected encoding WebP/AVIF in the job with WASM encoders: they
     are slower and heavier, and would freeze each size and format at
     upload time.
4. **Memory is bounded by a dedicated `media` River queue**
   (`MEDIA_IMAGE_WORKERS`, default 2 per worker). Peak use per job is
   roughly the original, plus the decoded image, plus one rotated copy:
   about 220 MB at 25 MP.
5. **Cleanup is always a job, scheduled in the transaction that creates
   the garbage.**
   - Uploads not completed within an hour are removed
     (`catalog.expire_image_upload`).
   - Originals are removed after processing.
   - Masters are removed when an image or its product is deleted
     (`catalog.delete_objects`).
   - The delete worker refuses any key outside its own store's prefix.
6. **Limits:** at most 20 images per product, alt text ≤ 200 characters.

## Consequences

- HEIC and AVIF originals (some phones' defaults) are rejected until a
  pure-Go decoder exists or we accept a cgo sidecar; the apps convert to
  JPEG before upload.
- Animated GIFs keep only their first frame.
- imgproxy is a new runtime service (`deploy/compose`, and production
  next to the storefront, behind the CDN); its key and salt live in the
  secret manager, identical in the api's environment.
- Product reads gain one constant query: images for the whole page,
  through the same per-product `LATERAL` lookup as variants (ADR-0011
  §7). A list is now 6 statements with BEGIN, set tenant and COMMIT.
- A full round trip (presigned upload, policy enforcement, processing,
  signed delivery, tampered URLs → 403) is tested against real SeaweedFS
  and imgproxy containers.
