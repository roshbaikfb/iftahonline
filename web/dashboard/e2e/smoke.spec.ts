import { expect, test, type Page } from '@playwright/test';

// watch fails the test on any console error: a CSP violation shows up
// there. One is expected: asking who is signed in (GET /v1/me) answers
// 401 to a visitor, which the browser logs as a failed resource.
function watch(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return;
    if (msg.location().url.endsWith('/v1/me') && msg.text().includes('401')) return;
    errors.push(msg.text());
  });
  page.on('pageerror', (err) => errors.push(err.message));
  return errors;
}

test('the login page renders build info fetched through the generated client', async ({ page }) => {
  const errors = watch(page);
  const response = await page.goto('/');
  expect(response?.status()).toBe(200);
  expect(response?.headers()['content-security-policy']).toContain("script-src 'self'");
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('تسجيل الدخول');
  await expect(page.getByText('لوحة التاجر')).toBeVisible();
  await expect(page.getByTestId('build-info')).toContainText('go1.');
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

// The merchant's day: sign in, see the orders the shop received, confirm
// the pending ones in bulk, move one along on its page, and export.
test('a merchant signs in, confirms orders in bulk, ships one and exports', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();

  // Signing in opens the home page (U4): the greeting, the period's
  // figures, then the orders through the sidebar.
  await expect(page).toHaveURL(/\/stores\/[0-9A-Z]{26}\/home$/);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('يا منى');
  await expect(page.getByRole('heading', { name: 'آخر الطلبات' })).toBeVisible();
  const side = page.getByRole('navigation', { name: 'القائمة الرئيسية' });
  await expect(side.getByRole('link', { name: 'الرئيسية', exact: true })).toHaveAttribute('aria-current', 'page');
  await side.getByRole('link', { name: 'الطلبات' }).click();
  await expect(page).toHaveURL(/\/stores\/[0-9A-Z]{26}\/orders$/);
  await expect(page.locator('.top .store-name')).toHaveText('متجر منى');
  await expect(side.getByRole('link', { name: 'الطلبات' })).toHaveAttribute('aria-current', 'page');
  const rows = page.locator('table.orders tbody tr');
  await expect(rows).toHaveCount(3);
  await expect(rows.filter({ hasText: 'مؤكد' })).toHaveCount(1);
  await expect(rows.filter({ hasText: 'قيد الانتظار' })).toHaveCount(2);

  // Bulk confirm the two pending orders.
  await page.getByLabel('تحديد كل الطلبات قيد الانتظار').check();
  const bulk = page.getByRole('region', { name: 'إجراءات المحدد' });
  await expect(bulk).toContainText('طلبان محددان');
  await bulk.getByRole('button', { name: 'تأكيد' }).click();
  await expect(page.getByText('تم تأكيد 2 طلب')).toBeVisible();
  await expect(rows.filter({ hasText: 'قيد الانتظار' })).toHaveCount(0);

  // Filter by phone, open the order, pack and ship it.
  await page.getByLabel('رقم الموبايل').fill('01022222222');
  await page.getByRole('button', { name: 'تصفية' }).click();
  await expect(rows).toHaveCount(1);
  await rows.first().getByRole('link').click();
  const status = page.locator('.title-row .status');
  await expect(status).toHaveText('مؤكد');
  await expect(page.getByText('أحمد حسن')).toBeVisible();
  await page.getByRole('button', { name: 'جاهز للشحن' }).click();
  await page.getByRole('button', { name: 'شُحن' }).click();
  await expect(status).toHaveText('تم الشحن');
  await expect(page.locator('.od-timeline li')).toHaveCount(4);

  // Export: a CSV with every order, a BOM and the Arabic names.
  await page.getByRole('navigation', { name: 'مسار الصفحة' }).getByRole('link', { name: 'الطلبات' }).click();
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'تصدير CSV' }).click()]);
  expect(download.suggestedFilename()).toMatch(/^orders-\d{4}-\d{2}-\d{2}\.csv$/);
  const path = await download.path();
  const { readFile } = await import('node:fs/promises');
  const csv = await readFile(path, 'utf8');
  expect(csv.startsWith('﻿')).toBe(true);
  expect(csv.split('\n').filter(Boolean)).toHaveLength(4);
  expect(csv).toContain('أحمد حسن');

  // Sign out from the account menu: back to the login page.
  await page.getByLabel(/^حسابي/).click();
  await page.getByRole('button', { name: 'تسجيل الخروج' }).click();
  await expect(page.getByRole('button', { name: 'تسجيل الدخول' })).toBeVisible();
  expect(errors, 'no console errors').toEqual([]);
});

// Delivery setup: a zone with its rate and a city rate, then the store's
// carrier account (the fake one), whose webhook secret is shown once.
test('a merchant sets up a shipping zone and connects a carrier', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);

  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'مناطق الشحن', exact: true }).click();
  await page.getByRole('button', { name: 'منطقة جديدة' }).click();
  const zone = page.locator('form.zone');
  await zone.getByLabel('اسم المنطقة').fill('الإسكندرية');
  await zone.getByLabel('سعر الشحن').fill('60');
  await zone.getByRole('checkbox', { name: 'الإسكندرية' }).check();
  await zone.getByRole('button', { name: '+ مدينة' }).click();
  await zone.getByLabel('المدينة').fill('سموحة');
  await zone.getByLabel('سعرها').fill('45');
  await zone.getByRole('button', { name: 'حفظ المنطقة' }).click();
  await expect(page.getByText('أُضيفت المنطقة')).toBeVisible();
  await expect(page.locator('.zones-table tbody tr').filter({ hasText: 'الإسكندرية' })).toContainText('60 ج.م');

  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'شركات الشحن', exact: true }).click();
  await page.getByRole('combobox', { name: 'الشركة' }).selectOption({ label: 'شركة شحن تجريبية' });
  await page.getByLabel('مفتاح API من لوحة الشركة').fill('demo-carrier-key');
  await page.getByRole('button', { name: 'ربط' }).click();
  await expect(page.locator('.secret')).toContainText('/v1/webhooks/fake/');
  await expect(page.locator('.rows li').filter({ hasText: 'شركة شحن تجريبية' })).toContainText('مربوط');
  expect(errors, 'no console errors').toEqual([]);
});

// Money: connect the card gateway (the fake one), so checkout offers
// cards, and read the cash-on-delivery report.
test('a merchant connects a card gateway and reads the COD report', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);

  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'الدفع بالبطاقة', exact: true }).click();
  await page.getByRole('combobox', { name: 'البوابة' }).selectOption({ label: 'بوابة دفع تجريبية' });
  await page.getByLabel('API key').fill('demo-gateway-key');
  await page.getByLabel('سر الإشعارات').fill('demo-webhook-secret');
  await page.getByRole('button', { name: 'ربط' }).click();
  await expect(page.getByText(/تم الربط/)).toBeVisible();
  await expect(page.locator('.rows li').filter({ hasText: 'بوابة دفع تجريبية' })).toContainText('مربوطة');

  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'التحصيل', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'تحصيل الدفع عند الاستلام' })).toBeVisible();
  await expect(page.getByText(/حُوِّلت قيمتها|لا توجد طرود/).first()).toBeVisible();
  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'المدفوعات', exact: true }).click();
  await expect(page.getByText('لا توجد مدفوعات بالبطاقة بعد.')).toBeVisible();
  expect(errors, 'no console errors').toEqual([]);
});

// The setup wizard (ADR-0034) against the real API: sign up from the
// login page, then the activity, look and shipping steps. The demo API has
// no worker, so the template stays "preparing" here; applying it is the
// onboarding module's own tests. Under the production CSP, the looks'
// colours must not raise a violation.
test('a new merchant signs up and walks the setup wizard', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByRole('link', { name: 'أنشئ متجرك' }).click();
  await expect(page).toHaveURL(/\/signup$/);
  await page.getByLabel('اسمك').fill('تاجر جديد');
  await page.getByLabel('البريد الإلكتروني').fill(`new-${String(Date.now())}@demo.test`);
  await page.getByLabel(/كلمة المرور/).fill('demo-password-123');
  await page.getByRole('button', { name: 'متابعة' }).click();

  await page.getByLabel('اسم المتجر').fill('متجر جديد');
  await page.getByLabel(/^رابط المتجر/).fill(`new-${String(Date.now())}`);
  await expect(page.getByText('الرابط متاح')).toBeVisible();
  await page.getByRole('button', { name: 'إنشاء المتجر' }).click();

  await expect(page).toHaveURL(/\/stores\/[0-9A-Z]{26}\/setup$/);
  await expect(page.getByRole('heading', { name: 'ماذا تبيع؟' })).toBeVisible();
  await page.getByRole('button', { name: 'عطور' }).click();
  await page.getByRole('button', { name: 'متابعة' }).click();
  await page.getByRole('button', { name: /ناعم/ }).click();
  await page.getByRole('button', { name: 'متابعة' }).click();
  await page.getByLabel(/^سعر الشحن/).fill('50');
  await page.getByRole('button', { name: 'تجهيز متجري' }).click();
  await expect(page.getByRole('heading', { name: 'جارٍ تجهيز متجرك…' })).toBeVisible();
  expect(errors, 'no console errors').toEqual([]);
});

// The products list (U5): reached from the sidebar, filtered by stock,
// and the product moved to drafts and back in bulk.
test('a merchant lists, filters and bulk-edits products', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'المنتجات', exact: true }).click();
  await expect(page).toHaveURL(/\/stores\/[0-9A-Z]{26}\/products$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('المنتجات');
  const row = page.locator('table.products-table tbody tr').filter({ hasText: 'عطر العود الملكي' });
  await expect(row).toContainText('منشور');
  await expect(row).toContainText('450 ج.م');
  await expect(page.getByRole('tab', { name: /^منشورة/ })).toContainText('1');

  await page.getByLabel('المخزون').selectOption('out');
  await page.getByRole('button', { name: 'تصفية' }).click();
  await expect(page.getByText('لا منتجات تطابق التصفية.')).toBeVisible();
  await page.getByLabel('المخزون').selectOption('in_stock');
  await page.getByRole('button', { name: 'تصفية' }).click();
  await expect(row).toBeVisible();

  await row.getByRole('checkbox').check();
  const bulk = page.getByRole('region', { name: 'إجراءات المحدد' });
  await bulk.getByRole('button', { name: 'تحويل لمسودة' }).click();
  await expect(page.getByText('تحوّل إلى مسودة: منتج واحد')).toBeVisible();
  await expect(row).toContainText('مسودة');
  await row.getByRole('checkbox').check();
  await bulk.getByRole('button', { name: 'نشر' }).click();
  await expect(row).toContainText('منشور');
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

// The product editor and size charts (U5): a chart in centimetres, then a
// product whose colour option makes its variants, one taken off sale.
test('a merchant makes a size chart and a product with variants', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  const side = page.getByRole('navigation', { name: 'القائمة الرئيسية' });

  await side.getByRole('link', { name: 'جداول المقاسات' }).click();
  await page.getByRole('button', { name: '+ جدول جديد' }).click();
  await page.getByLabel('اسم الجدول (لك فقط)').fill('تيشيرتات');
  await page.getByLabel('الصدر S').fill('96');
  await page.getByRole('button', { name: 'حفظ', exact: true }).click();
  await expect(page.getByText('حُفظ الجدول.')).toBeVisible();

  await side.getByRole('link', { name: 'المنتجات', exact: true }).click();
  await page.getByRole('link', { name: 'أضف منتجًا' }).click();
  await page.getByLabel('اسم المنتج').fill('بولو قطن');
  await page.getByRole('button', { name: '+ أضف خيارًا' }).click();
  await page.getByLabel('اسم الخيار').fill('اللون');
  await page.getByLabel('قيمة جديدة لـ اللون').fill('أسود');
  await page.keyboard.press('Enter');
  await page.getByLabel('قيمة جديدة لـ اللون').fill('أبيض');
  await page.keyboard.press('Enter');
  await page.getByLabel('سعر أسود').fill('320');
  await page.getByLabel('سعر أبيض').fill('320');
  await page.getByLabel('مخزون أسود').fill('5');
  await page.getByLabel('أبيض متاح للبيع').uncheck();
  await page.getByRole('combobox', { name: 'جدول المقاسات' }).selectOption({ label: 'تيشيرتات' });
  await page.getByRole('button', { name: 'حفظ المنتج' }).click();

  await expect(page.getByText('حُفظ المنتج.')).toBeVisible();
  await expect(page).toHaveURL(/\/products\/[0-9A-Z]{26}$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('بولو قطن');
  await expect(page.getByLabel('أبيض متاح للبيع')).not.toBeChecked();
  await expect(page.getByLabel('مخزون أسود')).toHaveValue('5');
  await expect(page.getByRole('heading', { name: 'الصور' })).toBeVisible();
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

// Categories and inventory (U5): a category under another, then a stock
// count set from the inventory screen.
test('a merchant arranges categories and counts stock', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  const side = page.getByRole('navigation', { name: 'القائمة الرئيسية' });

  await side.getByRole('link', { name: 'التصنيفات' }).click();
  await page.getByRole('button', { name: 'أضف تصنيفًا' }).click();
  let form = page.getByRole('form', { name: 'تصنيف جديد' });
  await form.getByLabel('الاسم').fill('عطور');
  await form.getByRole('button', { name: 'حفظ' }).click();
  await expect(page.getByText('أُضيف «عطور».')).toBeVisible();
  await page.getByRole('button', { name: 'أضف تصنيفًا' }).click();
  form = page.getByRole('form', { name: 'تصنيف جديد' });
  await form.getByLabel('الاسم').fill('عطور شرقية');
  await form.getByLabel('تحت').selectOption({ label: 'عطور' });
  await form.getByRole('button', { name: 'حفظ' }).click();
  await expect(page.locator('.cat-tree li.depth-2')).toContainText('عطور شرقية');

  await side.getByRole('link', { name: 'المخزون' }).click();
  await page.getByRole('tab', { name: 'الكل' }).click();
  const row = page.locator('table.inventory tbody tr').filter({ hasText: 'عطر العود الملكي' }).first();
  await row.getByRole('textbox').fill('250');
  await row.getByRole('button', { name: 'حفظ' }).click();
  await expect(row).toContainText('حُفظ');
  await expect(row.locator('td').nth(2)).toHaveText('250');

  await side.getByRole('link', { name: 'الاستيراد' }).click();
  await expect(page.getByRole('link', { name: 'الملف النموذجي' })).toHaveAttribute('href', '/products-template.csv');
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

// The design screen (U6): an edit is saved as a draft at once, then
// published; under the production CSP, the shop fonts load from 'self'.
test('a merchant edits the look as a draft and publishes it', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'التصميم' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('التصميم');

  await page.getByRole('button', { name: 'ألوان جاهزة' }).click();
  await page.getByRole('group', { name: 'ألوان جاهزة' }).getByRole('button', { name: /فيروزي/ }).click();
  await page.getByRole('combobox', { name: 'الخط', exact: true }).selectOption('cairo');
  await expect(page.getByText('حُفظت المسودة')).toBeVisible();
  await expect(page.getByText('تغييرات لم تُنشر')).toBeVisible();

  await page.locator('.design-head').getByRole('button', { name: 'نشر' }).click();
  await expect(page.getByText('تغييرات لم تُنشر')).toBeHidden();
  await page.reload();
  await expect(page.getByLabel('كود اللون الأساسي')).toHaveValue('#0F766E');
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

test('a merchant adds a home page section as a draft and publishes it', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'الصفحة الرئيسية' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('الصفحة الرئيسية');
  await expect(page.getByText(/^القسم 1 من \d+$/)).toBeVisible();

  await page.getByRole('button', { name: '+ أضف قسمًا' }).click();
  await page.getByRole('dialog', { name: 'أضف قسمًا' }).getByRole('button', { name: /أسئلة شائعة/ }).click();
  await page.getByLabel('السؤال').fill('متى يصل طلبي؟');
  await page.getByLabel('الجواب').fill('خلال 3 أيام عمل');
  await expect(page.getByText('حُفظت المسودة')).toBeAttached();
  await expect(page.getByText('تغييرات لم تُنشر')).toBeVisible();

  await page.locator('.design-head').getByRole('button', { name: 'نشر' }).click();
  await expect(page.getByText('تغييرات لم تُنشر')).toBeHidden();
  await page.reload();
  await expect(page.locator('.sec-list .name strong').last()).toHaveText('أسئلة شائعة');
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

test('a merchant publishes a landing page and a policy', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'الصفحات' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('الصفحات');

  await page.getByRole('link', { name: '+ صفحة جديدة' }).click();
  await page.getByLabel('العنوان', { exact: true }).fill('عرض الشتاء');
  const product = page.getByLabel('المنتج', { exact: true });
  await expect(product.locator('option')).not.toHaveCount(1);
  await product.selectOption({ index: 1 });
  await page.getByRole('button', { name: 'نشر' }).click();
  await expect(page.getByText('نُشرت الصفحة.')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('عرض الشتاء');

  await page.getByRole('link', { name: '→ الصفحات' }).click();
  await expect(page.getByRole('link', { name: 'عرض الشتاء' })).toBeVisible();
  await page.getByRole('link', { name: 'سياسة الاسترجاع' }).click();
  await page.getByLabel('النص').fill('يمكنك إرجاع المنتج خلال 14 يومًا من الاستلام.');
  await page.getByRole('button', { name: 'نشر' }).click();
  await expect(page.getByText('نُشرت. تظهر في أسفل صفحات متجرك.')).toBeVisible();
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});

test('a merchant renames the store, adds a domain and invites a teammate', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/');
  await page.getByLabel('البريد الإلكتروني').fill('owner@demo.test');
  await page.getByLabel('كلمة المرور').fill('demo-password-123');
  await page.getByRole('button', { name: 'تسجيل الدخول' }).click();
  await expect(page).toHaveURL(/\/home$/);
  const nav = page.getByRole('navigation', { name: 'القائمة الرئيسية' });

  await nav.getByRole('link', { name: 'إعدادات المتجر' }).click();
  const name = page.getByLabel('اسم المتجر');
  await name.fill('متجر التجربة');
  await page.getByRole('button', { name: 'حفظ التغييرات' }).click();
  await expect(page.getByText('حُفظت الإعدادات.')).toBeVisible();

  await nav.getByRole('link', { name: 'الدومينات' }).click();
  await page.getByLabel('الدومين').fill('shop.demo-e2e.test');
  await page.getByRole('button', { name: 'أضف' }).click();
  await expect(page.getByText('shop.demo-e2e.test', { exact: true })).toBeVisible();
  await expect(page.getByText('_iftah-verify.shop.demo-e2e.test')).toBeVisible();

  await nav.getByRole('link', { name: 'فريق العمل' }).click();
  await page.getByLabel('البريد الإلكتروني').fill('helper@demo.test');
  await page.getByRole('radio', { name: /^الشحن/ }).check();
  await page.getByRole('button', { name: 'أرسل الدعوة' }).click();
  await expect(page.getByText('أرسلنا الدعوة إلى helper@demo.test. الرابط صالح 7 أيام.')).toBeVisible();
  expect(errors, 'no console errors (e.g. CSP violations)').toEqual([]);
});
