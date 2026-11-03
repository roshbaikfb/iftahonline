-- The platform site's content (S4a, ADR-0073).

-- name: GetSettings :one
SELECT whatsapp, whatsapp_intl, email, socials, company_number, story_ar, story_en, video_id, logo_key, og_key FROM site_settings WHERE id = 1;

-- name: SaveSettings :exec
UPDATE site_settings
SET whatsapp = @whatsapp, whatsapp_intl = @whatsapp_intl, email = @email, socials = @socials, company_number = @company_number, updated_at = now()
WHERE id = 1;

-- name: ListFAQs :many
SELECT id, position, on_home, question_ar, answer_ar, question_en, answer_en
FROM site_faqs
ORDER BY position, created_at
LIMIT 200;

-- name: InsertFAQ :exec
INSERT INTO site_faqs (id, position, on_home, question_ar, answer_ar, question_en, answer_en)
VALUES (@id, @position, @on_home, @question_ar, @answer_ar, @question_en, @answer_en);

-- name: UpdateFAQ :execrows
UPDATE site_faqs
SET position = @position, on_home = @on_home, question_ar = @question_ar, answer_ar = @answer_ar,
    question_en = @question_en, answer_en = @answer_en, updated_at = now()
WHERE id = @id;

-- name: DeleteFAQ :execrows
DELETE FROM site_faqs WHERE id = @id;

-- name: SaveStory :exec
UPDATE site_settings SET story_ar = @story_ar, story_en = @story_en, video_id = @video_id, updated_at = now() WHERE id = 1;

-- name: SetLogo :exec
UPDATE site_settings SET logo_key = @logo_key, updated_at = now() WHERE id = 1;

-- name: SetShareImage :exec
UPDATE site_settings SET og_key = @og_key, updated_at = now() WHERE id = 1;

-- name: ListPeople :many
SELECT id, kind, position, name_ar, name_en, role_ar, role_en, bio_ar, bio_en, photo_key, linkedin_url, x_url
FROM site_people
ORDER BY kind, position, created_at
LIMIT 200;

-- name: GetPerson :one
SELECT id, kind, position, name_ar, name_en, role_ar, role_en, bio_ar, bio_en, photo_key, linkedin_url, x_url
FROM site_people WHERE id = @id;

-- name: InsertPerson :exec
INSERT INTO site_people (id, kind, position, name_ar, name_en, role_ar, role_en, bio_ar, bio_en, photo_key, linkedin_url, x_url)
VALUES (@id, @kind, @position, @name_ar, @name_en, @role_ar, @role_en, @bio_ar, @bio_en, @photo_key, @linkedin_url, @x_url);

-- name: UpdatePerson :execrows
UPDATE site_people
SET kind = @kind, position = @position, name_ar = @name_ar, name_en = @name_en, role_ar = @role_ar, role_en = @role_en,
    bio_ar = @bio_ar, bio_en = @bio_en, photo_key = @photo_key, linkedin_url = @linkedin_url, x_url = @x_url, updated_at = now()
WHERE id = @id;

-- name: DeletePerson :execrows
DELETE FROM site_people WHERE id = @id;

-- name: ListTestimonials :many
SELECT id, position, name, store_name, store_url, quote_ar, quote_en, photo_key
FROM site_testimonials
ORDER BY position, created_at
LIMIT 200;

-- name: GetTestimonial :one
SELECT id, position, name, store_name, store_url, quote_ar, quote_en, photo_key FROM site_testimonials WHERE id = @id;

-- name: InsertTestimonial :exec
INSERT INTO site_testimonials (id, position, name, store_name, store_url, quote_ar, quote_en, photo_key)
VALUES (@id, @position, @name, @store_name, @store_url, @quote_ar, @quote_en, @photo_key);

-- name: UpdateTestimonial :execrows
UPDATE site_testimonials
SET position = @position, name = @name, store_name = @store_name, store_url = @store_url, quote_ar = @quote_ar, quote_en = @quote_en,
    photo_key = @photo_key, updated_at = now()
WHERE id = @id;

-- name: DeleteTestimonial :execrows
DELETE FROM site_testimonials WHERE id = @id;

-- name: ListGuides :many
-- Every guide for the console, newest first, without their bodies.
SELECT id, slug, status, title_ar, summary_ar, title_en, cover_key, published_at, updated_at
FROM site_guides
ORDER BY updated_at DESC
LIMIT 500;

-- name: PublishedGuides :many
-- The guides the site shows, newest first, with their bodies.
SELECT id, slug, title_ar, summary_ar, body_ar, title_en, summary_en, body_en, cover_key, published_at, updated_at
FROM site_guides
WHERE status = 'published'
ORDER BY published_at DESC
LIMIT 200;

-- name: GetGuide :one
SELECT id, slug, status, title_ar, summary_ar, body_ar, title_en, summary_en, body_en, cover_key, published_at, updated_at
FROM site_guides WHERE id = @id;

-- name: InsertGuide :exec
INSERT INTO site_guides (id, slug, status, title_ar, summary_ar, body_ar, title_en, summary_en, body_en, cover_key, published_at)
VALUES (@id, @slug, @status, @title_ar, @summary_ar, @body_ar, @title_en, @summary_en, @body_en, @cover_key, @published_at);

-- name: UpdateGuide :execrows
UPDATE site_guides
SET slug = @slug, status = @status, title_ar = @title_ar, summary_ar = @summary_ar, body_ar = @body_ar,
    title_en = @title_en, summary_en = @summary_en, body_en = @body_en, cover_key = @cover_key, published_at = @published_at, updated_at = now()
WHERE id = @id;

-- name: DeleteGuide :execrows
DELETE FROM site_guides WHERE id = @id;

-- name: CountGuides :one
SELECT count(*) FROM site_guides;
