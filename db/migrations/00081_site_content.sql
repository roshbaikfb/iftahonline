-- The platform site's content the console edits (S4a, ADR-0073): its
-- contact details and its FAQ, in Arabic and English.
--
-- Platform data, owned by the site module, no store_id (like ad_settings,
-- ADR-0062): the console writes it as app_admin, the storefront role
-- (app_user) reads it. The FAQ starts with the copy the site shipped
-- with, so nothing changes on the day this runs.
--
-- Expand only: new tables.

-- +goose Up
CREATE TABLE site_settings (
  id             int         PRIMARY KEY CHECK (id = 1),
  -- E.164; '' shows none.
  whatsapp       text        NOT NULL DEFAULT '' CHECK (whatsapp = '' OR whatsapp ~ '^\+[1-9][0-9]{7,14}$'),
  whatsapp_intl  text        NOT NULL DEFAULT '' CHECK (whatsapp_intl = '' OR whatsapp_intl ~ '^\+[1-9][0-9]{7,14}$'),
  email          text        NOT NULL DEFAULT '' CHECK (email = '' OR (char_length(email) <= 254 AND email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$')),
  -- {"facebook": "https://…", …}: the networks the site links.
  socials        jsonb       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(socials) = 'object'),
  -- Companies House: 8 characters (12345678, SC123456).
  company_number text        NOT NULL DEFAULT '' CHECK (company_number = '' OR company_number ~ '^[0-9A-Z]{8}$'),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
INSERT INTO site_settings (id, whatsapp) VALUES (1, '+201153450716');

CREATE TABLE site_faqs (
  id          uuid        PRIMARY KEY,
  position    int         NOT NULL CHECK (position >= 0),
  on_home     boolean     NOT NULL DEFAULT false,
  question_ar text        NOT NULL CHECK (char_length(question_ar) BETWEEN 3 AND 200),
  answer_ar   text        NOT NULL CHECK (char_length(answer_ar) BETWEEN 3 AND 2000),
  -- English is optional: '' leaves the question off the English site.
  question_en text        NOT NULL DEFAULT '' CHECK (char_length(question_en) <= 200),
  answer_en   text        NOT NULL DEFAULT '' CHECK (char_length(answer_en) <= 2000),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((question_en = '') = (answer_en = ''))
);
-- The site and the console read them in order.
CREATE INDEX site_faqs_position ON site_faqs (position, created_at);

-- The copy the site shipped with (S2, S3b).
INSERT INTO site_faqs (id, position, on_home, question_ar, answer_ar, question_en, answer_en) VALUES
  ('019a4000-0000-7000-8000-000000000001', 0, true,
   'ما هي منصة افتح أونلاين؟',
   'افتح أونلاين (وتُكتب أيضًا افتح اونلاين، وبالإنجليزية iftahonline) منصة لإنشاء المتاجر الإلكترونية، صُممت لتجار مصر والخليج الذين يبيعون بالدفع عند الاستلام: متجر جاهز، وتأكيد الطلبات على واتساب، وحماية من الطلبات الوهمية، ولوحة للربح الصافي.',
   'What is iftahonline?',
   'iftahonline (also written eftahonline, and in Arabic افتح أونلاين) is a platform for creating online stores, built for merchants in Egypt and the Gulf who sell with cash on delivery: a ready store, order confirmation on WhatsApp, protection from fake orders, and a net profit dashboard.'),
  ('019a4000-0000-7000-8000-000000000002', 10, true,
   'هل يمكنني البدء مجانًا؟',
   'نعم. الباقة المجانية دائمة، ولا تطلب بطاقة بنكية. حدودها في صفحة الأسعار.',
   'Can I start free?',
   'Yes. The free plan is forever and needs no card. Its limits are on the pricing page.'),
  ('019a4000-0000-7000-8000-000000000003', 20, true,
   'كيف يؤكد العميل طلبه على واتساب؟',
   'تصله رسالة فيها تفاصيل طلبه وزرّا «تأكيد الطلب» و«إلغاء الطلب». بضغطة يتأكد الطلب في لوحتك، ومن ليس على واتساب تصله رسالة نصية برابط للتأكيد.',
   'How does a customer confirm an order on WhatsApp?',
   'They get a message with their order details and «Confirm order» and «Cancel order» buttons. One tap confirms the order in your dashboard, and customers who aren''t on WhatsApp get a text message with a confirmation link.'),
  ('019a4000-0000-7000-8000-000000000004', 30, true,
   'بأي عملة أدفع الاشتراك؟',
   'بالجنيه المصري إن كان متجرك في مصر، وبالدولار الأمريكي في باقي الدول.',
   'Which currency do I pay in?',
   'In Egyptian pounds if your store is in Egypt, and in US dollars in every other country.'),
  ('019a4000-0000-7000-8000-000000000005', 40, true,
   'هل أستطيع ربط دومين خاص بي؟',
   'نعم في الباقات المدفوعة، ويبقى لمتجرك أيضًا رابط مجاني على افتح أونلاين.',
   'Can I connect my own domain?',
   'Yes, on paid plans, and your store keeps a free iftahonline link too.'),
  ('019a4000-0000-7000-8000-000000000006', 50, false,
   'كيف تحمي المنصة من الطلبات الوهمية؟',
   'كل طلب يحصل على تقييم من نتائج توصيل رقم العميل في متاجر افتح أونلاين كلها. لا يرى أي متجر بيانات متجر آخر، فقط مستوى التقييم، وأنت تختار ما يحدث للطلبات عالية المخاطر: إيقاف للمراجعة، أو تأكيد بالهاتف، أو دفع التوصيل مقدمًا.',
   'How does the platform protect from fake orders?',
   'Every order gets a rating from the delivery results of the customer''s number across all iftahonline stores. No store sees another store''s data, only the rating level, and you choose what happens to high-risk orders: hold for review, confirm by phone, or ask for the delivery fee upfront.'),
  ('019a4000-0000-7000-8000-000000000007', 60, false,
   'هل تعمل المنصة في الخليج؟',
   'نعم. المتجر يعرض الأسعار بعملته، ويعمل بالعربية والإنجليزية.',
   'Does the platform work in the Gulf?',
   'Yes. The store shows prices in its own currency, in Arabic and English.'),
  ('019a4000-0000-7000-8000-000000000008', 70, false,
   'عندي متجر في منصة أخرى، هل يمكن نقله؟',
   'نعم. مع أي باقة مدفوعة ننقل منتجاتك وصورها وتصنيفاتها إلى متجرك الجديد مجانًا، ويمكنك أيضًا استيرادها بنفسك من ملف.',
   'I have a store on another platform. Can I move it?',
   'Yes. With any paid plan we move your products, their photos and categories to your new store for free, and you can also import them yourself from a file.'),
  ('019a4000-0000-7000-8000-000000000009', 80, false,
   'هل توجد رسوم أخرى غير الاشتراك؟',
   'لا رسوم مخفية. رسوم Paymob على الدفع بالبطاقة، وأسعار شركة الشحن، تدفعها لهم مباشرة بأسعارهم، ولا نأخذ منها شيئًا.',
   'Are there fees besides the subscription?',
   'No hidden fees. Paymob''s card fees and your carrier''s rates are paid to them directly at their prices, and we take nothing from them.'),
  ('019a4000-0000-7000-8000-00000000000a', 90, false,
   'هل أستطيع إلغاء اشتراكي؟',
   'نعم، بلا عقد، في أي وقت من لوحة التاجر. وبعد نهاية المدة المدفوعة يعود متجرك إلى الباقة المجانية وتبقى بياناته كما هي.',
   'Can I cancel my subscription?',
   'Yes, with no contract, any time from your dashboard. When the paid period ends your store returns to the free plan and its data stays as it is.');

REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON site_settings, site_faqs FROM app_user;
GRANT SELECT, UPDATE ON site_settings TO app_admin;
GRANT SELECT, INSERT, UPDATE, DELETE ON site_faqs TO app_admin;

-- +goose Down
DROP TABLE IF EXISTS site_faqs;
DROP TABLE IF EXISTS site_settings;
