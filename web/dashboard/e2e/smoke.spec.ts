import { expect, test } from '@playwright/test';

test('dashboard renders build info fetched through the generated client', async ({ page }) => {
  const violations: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') violations.push(msg.text());
  });

  const response = await page.goto('/');
  expect(response?.status()).toBe(200);
  expect(response?.headers()['content-security-policy']).toContain("script-src 'self'");

  await expect(page.getByRole('heading', { level: 1 })).toHaveText('لوحة تحكم التاجر');
  const info = page.getByTestId('build-info');
  await expect(info).toContainText('go1.');
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  expect(violations, 'no console errors (e.g. CSP violations)').toEqual([]);
});
