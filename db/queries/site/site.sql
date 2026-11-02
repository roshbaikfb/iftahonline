-- The platform site's content (S4a, ADR-0073).

-- name: GetSettings :one
SELECT whatsapp, whatsapp_intl, email, socials, company_number FROM site_settings WHERE id = 1;

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
