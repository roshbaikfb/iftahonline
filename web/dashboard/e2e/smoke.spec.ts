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
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('لوحة تحكم التاجر');
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

  await expect(page).toHaveURL(/\/stores\/[0-9A-Z]{26}\/orders$/);
  await expect(page.getByRole('link', { name: 'متجر منى' })).toBeVisible();
  const rows = page.locator('table.orders tbody tr');
  await expect(rows).toHaveCount(3);
  await expect(rows.filter({ hasText: 'مؤكد' })).toHaveCount(1);
  await expect(rows.filter({ hasText: 'قيد الانتظار' })).toHaveCount(2);

  // Bulk confirm the two pending orders.
  await page.getByLabel('تحديد كل الطلبات قيد الانتظار').check();
  await page.getByRole('button', { name: 'تأكيد المحدد (2)' }).click();
  await expect(page.getByText('تم تأكيد 2 طلب')).toBeVisible();
  await expect(rows.filter({ hasText: 'قيد الانتظار' })).toHaveCount(0);

  // Filter by phone, open the order, pack and ship it.
  await page.getByLabel('رقم الموبايل').fill('01022222222');
  await page.getByRole('button', { name: 'تصفية' }).click();
  await expect(rows).toHaveCount(1);
  await rows.first().getByRole('link').click();
  await expect(page.getByRole('heading', { level: 1 })).toContainText('مؤكد');
  await expect(page.getByText('أحمد حسن')).toBeVisible();
  await page.getByRole('button', { name: 'جاهز للشحن' }).click();
  await page.getByRole('button', { name: 'شُحن' }).click();
  await expect(page.getByRole('heading', { level: 1 })).toContainText('تم الشحن');
  await expect(page.locator('.timeline li')).toHaveCount(4);

  // Export: a CSV with every order, a BOM and the Arabic names.
  await page.getByRole('link', { name: '→ الطلبات' }).click();
  const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'تصدير CSV' }).click()]);
  expect(download.suggestedFilename()).toMatch(/^orders-\d{4}-\d{2}-\d{2}\.csv$/);
  const path = await download.path();
  const { readFile } = await import('node:fs/promises');
  const csv = await readFile(path, 'utf8');
  expect(csv.startsWith('﻿')).toBe(true);
  expect(csv.split('\n').filter(Boolean)).toHaveLength(4);
  expect(csv).toContain('أحمد حسن');

  // Sign out: back to the login page.
  await page.getByRole('button', { name: 'تسجيل الخروج' }).click();
  await expect(page.getByRole('button', { name: 'تسجيل الدخول' })).toBeVisible();
  expect(errors, 'no console errors').toEqual([]);
});
