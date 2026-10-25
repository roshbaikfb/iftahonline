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
  // The trust badge in the page, and the payment method in the footer.
  await expect(page.locator('#main').getByText('الدفع عند الاستلام')).toBeVisible();
  await expect(page.getByRole('contentinfo').getByText('الدفع عند الاستلام')).toBeVisible();
  const cards = page.locator('.card');
  await expect(cards).toHaveCount(8);

  // Product: open the oud perfume from the grid, by its title link.
  await cards.getByRole('link', { name: 'عطر العود الملكي' }).click();
  await expect(page).toHaveURL(/\/p\/royal-oud$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('عطر العود الملكي');
  const now = page.locator('[data-now]');
  await expect(now).toHaveText('450 ج.م');
  await expect(page.locator('[data-was]')).toHaveText('600 ج.م');
  await expect(page.locator('[data-save]')).toHaveText('وفّر 150 ج.م');
  const add = page.locator('[data-total]'); // the bar at the bottom of a phone's screen
  await expect(add).toHaveText('أضف إلى السلة · 450 ج.م');
  // Choosing the other size follows without a reload (our script): its
  // price, no saving, and the few units left.
  await page.getByText('100 مل', { exact: true }).click();
  await expect(now).toHaveText('750 ج.م');
  await expect(page.locator('[data-save]')).toBeHidden();
  await expect(page.locator('[data-left]')).toHaveText('باقي 4 قطع فقط');
  await expect(page.locator('[data-chosen="0"]')).toHaveText('100 مل');
  await page.getByRole('button', { name: 'أكثر' }).click();
  await expect(add).toHaveText('أضف إلى السلة · 1,500 ج.م');
  // The delivery box asks nothing until the shopper names a governorate.
  await expect(page.locator('[data-deliv-line]')).toBeHidden();
  await page.getByLabel('اختر محافظتك لمعرفة الشحن وموعد التوصيل').selectOption({ label: 'القاهرة' });
  await expect(page.locator('[data-deliv-line]')).toHaveText('التوصيل إلى القاهرة خلال 2–3 أيام');
  await expect(page.locator('[data-ship-line]')).toHaveText('الشحن مجاني'); // 1,500 is over the free threshold
  await page.getByRole('button', { name: 'أقل' }).click();
  await expect(page.locator('[data-ship-line]')).toHaveText('الشحن 40 ج.م · مجاني فوق 1,000 ج.م');
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
  // Choosing the governorate shows what delivery adds (our script asks
  // the store; the demo charges 60 outside Cairo and a 10 COD fee).
  const quote = form.locator('[data-quote]');
  await expect(quote).toHaveText('الشحن: 60 ج.م · رسوم الدفع عند الاستلام: 10 ج.م · الإجمالي: 520 ج.م');
  await form.getByLabel('المدينة / المنطقة').fill('سموحة');
  await form.getByLabel('العنوان بالتفصيل').fill('14 شارع فوزي معاذ، الدور الثالث');
  await page.getByRole('button', { name: 'اطلب الآن' }).click();
  await expect(page).toHaveURL(/\/thank-you\/[0-9A-Z]{26}$/);
  // The first name only (its link may be shared), and where it goes.
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('شكراً يا منى، طلبك وصلنا');
  await expect(page.locator('main')).not.toContainText('أحمد');
  await expect(page.locator('.where')).toHaveText('التوصيل إلى: سموحة، الإسكندرية');
  await expect(page.locator('.subtotal')).toContainText('450 ج.م');
  await expect(page.locator('.shipping')).toContainText('60 ج.م');
  await expect(page.locator('.cod-fee')).toContainText('10 ج.م');
  await expect(page.locator('.order-total')).toContainText('520 ج.م');

  expect(errors, 'no console errors (CSP violations, script errors)').toEqual([]);
});

test('product → cart → checkout, on a phone, in Arabic', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/p/royal-oud');
  await page.locator('[data-total]').click();
  await expect(page).toHaveURL(/\/cart$/);
  await expect(page.getByRole('link', { name: 'عطر العود الملكي' })).toBeVisible();
  await expect(page.locator('[data-cart-count]')).toHaveText('1');

  await page.getByRole('link', { name: 'إتمام الطلب' }).click();
  await expect(page).toHaveURL(/\/checkout$/);
  // The summary opens on demand.
  await expect(page.getByText('عطر العود الملكي × 1')).toBeHidden();
  await page.getByText('ملخص الطلب (قطعة واحدة)').click();
  await expect(page.getByText('عطر العود الملكي × 1')).toBeVisible();
  await page.getByLabel('الاسم').fill('سارة علي');
  await page.getByLabel('رقم الموبايل').fill('01122223333');
  // The totals card waits for the governorate, then fills line by line.
  const totals = page.locator('[data-totals]');
  await expect(totals.locator('[data-amt="shipping"]')).toHaveText('يُحسب بعد اختيار المحافظة');
  await page.getByLabel('المحافظة').selectOption({ label: 'القاهرة' });
  await expect(totals.locator('[data-amt="ship_label"]')).toHaveText('الشحن إلى القاهرة');
  await expect(totals.locator('[data-amt="shipping"]')).toHaveText('40 ج.م');
  await expect(totals.locator('[data-amt="cod_fee"]')).toHaveText('10 ج.م');
  await expect(totals.locator('[data-amt="total"]')).toHaveText('500 ج.م');
  await expect(page.locator('[data-sum-total]')).toHaveText('500 ج.م');
  await page.getByLabel('المدينة / المنطقة').fill('مدينة نصر');
  await page.getByLabel('العنوان بالتفصيل').fill('12 شارع عباس العقاد');
  await page.getByRole('button', { name: 'تأكيد الطلب' }).click();
  await expect(page).toHaveURL(/\/thank-you\/[0-9A-Z]{26}$/);
  await expect(page.locator('.order-total')).toContainText('500 ج.م');
  await expect(page.locator('[data-cart-count]')).toBeHidden(); // the cart was emptied

  expect(errors, 'no console errors (CSP violations, script errors)').toEqual([]);
});

test('the English store mirrors the Arabic one', async ({ page }) => {
  const errors = watch(page);
  await page.goto('/en/p/royal-oud');
  await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  await expect(page.locator('[data-now]')).toHaveText('EGP 450');
  await page.getByRole('link', { name: 'العربية' }).click();
  await expect(page).toHaveURL(/\/p\/royal-oud$/);
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  expect(errors).toEqual([]);
});
