import { expect, test, type Page } from '@playwright/test';

// watch fails the test on any console error: a CSP violation (inline
// script or style the policy does not allow) shows up there.
function watch(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });
  page.on('pageerror', (err) => errors.push(err.message));
  return errors;
}

test('home → product → landing page → one-step order, on a phone, in Arabic', async ({ page }) => {
  const errors = watch(page);

  // Home: RTL Arabic, the store's hero and newest products.
  const home = await page.goto('/');
  expect(home?.status()).toBe(200);
  expect(home?.headers()['content-security-policy']).toMatch(/style-src 'self' 'sha256-[A-Za-z0-9+/=]+'/);
  expect(home?.headers()['cache-tag']).toMatch(/^s-[0-9A-Z]{26},/);
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('أهلًا بك في متجر العرض');
  await expect(page.getByText('الدفع عند الاستلام')).toBeVisible();
  const cards = page.locator('a.card');
  await expect(cards).toHaveCount(8);

  // Product: open the oud perfume from the grid.
  await page.locator('a.card', { hasText: 'عطر العود الملكي' }).click();
  await expect(page).toHaveURL(/\/p\/royal-oud$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('عطر العود الملكي');
  const price = page.locator('form[data-buy] [data-price]');
  await expect(price).toContainText('450 ج.م');
  await expect(price.locator('s')).toHaveText('600 ج.م');
  // The variant picker updates the price without a reload (our script).
  await page.getByLabel('الحجم').selectOption({ label: '100 مل — 750 ج.م' });
  await expect(price).toHaveText('750 ج.م');
  await expect(page.getByRole('button', { name: 'أضف إلى السلة' })).toBeEnabled();
  const ld = JSON.parse((await page.locator('script[type="application/ld+json"]').textContent()) ?? '[]') as { '@type': string }[];
  expect(ld[0]?.['@type']).toBe('Product');

  // Landing page: from the home hero's button.
  await page.goto('/');
  await page.getByRole('link', { name: 'شاهد عرض الصيف' }).click();
  await expect(page).toHaveURL(/\/pages\/summer$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('عطر العود الملكي بخصم ٢٥٪');
  await expect(page.getByText('ثبات أكثر من ١٢ ساعة')).toBeVisible();
  const countdown = page.locator('[data-countdown] [data-unit]').last();
  const before = await countdown.textContent();
  await expect(countdown).not.toHaveText(before ?? '', { timeout: 3_000 }); // it ticks
  await page.getByText('متى يصل الطلب؟').click();
  await expect(page.getByText('خلال ٢ إلى ٤ أيام عمل.')).toBeVisible();

  // Order from the landing page in one step: the form, then thank you.
  const form = page.locator('form[data-order]');
  await expect(form.locator('input[name=key]')).not.toHaveValue(''); // our script made an idempotency key
  await form.getByLabel('الاسم').fill('منى أحمد');
  await form.getByLabel('رقم الموبايل').fill('010 1234 5678');
  await form.getByLabel('المحافظة').selectOption({ label: 'الإسكندرية' });
  await form.getByLabel('المدينة / المنطقة').fill('سموحة');
  await form.getByLabel('العنوان بالتفصيل').fill('14 شارع فوزي معاذ، الدور الثالث');
  await page.getByRole('button', { name: 'اطلب الآن' }).click();
  await expect(page).toHaveURL(/\/thank-you\/[0-9A-Z]{26}$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('شكرًا لطلبك!');
  await expect(page.locator('.subtotal')).toContainText('450 ج.م');

  expect(errors, 'no console errors (CSP violations, script errors)').toEqual([]);
});

test('product → cart → checkout, on a phone, in Arabic', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/p/royal-oud');
  await page.getByRole('button', { name: 'أضف إلى السلة' }).click();
  await expect(page).toHaveURL(/\/cart$/);
  await expect(page.getByRole('link', { name: 'عطر العود الملكي' })).toBeVisible();
  await expect(page.locator('[data-cart-count]')).toHaveText('1');

  await page.getByRole('link', { name: 'إتمام الطلب' }).click();
  await expect(page).toHaveURL(/\/checkout$/);
  await expect(page.getByText('عطر العود الملكي × 1')).toBeVisible();
  await page.getByLabel('الاسم').fill('سارة علي');
  await page.getByLabel('رقم الموبايل').fill('01122223333');
  await page.getByLabel('المحافظة').selectOption({ label: 'القاهرة' });
  await page.getByLabel('المدينة / المنطقة').fill('مدينة نصر');
  await page.getByLabel('العنوان بالتفصيل').fill('12 شارع عباس العقاد');
  await page.getByRole('button', { name: 'تأكيد الطلب' }).click();
  await expect(page).toHaveURL(/\/thank-you\/[0-9A-Z]{26}$/);
  await expect(page.locator('[data-cart-count]')).toBeHidden(); // the cart was emptied

  expect(errors, 'no console errors (CSP violations, script errors)').toEqual([]);
});

test('the English store mirrors the Arabic one', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/en/p/royal-oud');
  await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  await expect(page.locator('form[data-buy] [data-price]')).toContainText('EGP 450');
  await page.getByRole('link', { name: 'العربية' }).click();
  await expect(page).toHaveURL(/\/p\/royal-oud$/);
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  expect(errors).toEqual([]);
});
