import { expect, test } from '@playwright/test';

// The platform's own site (ADR-0071), on the demo's domain
// (http://localhost:<port>/): what a merchant sees first.
const site = 'http://localhost:18191';

test.describe('the platform site, on a desktop', () => {
  test.use({ viewport: { width: 1440, height: 900 }, isMobile: false, hasTouch: false });

  test('home → pricing, in Arabic, under its policy', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() === 'error') errors.push(msg.text());
    });
    page.on('pageerror', (err) => errors.push(err.message));

    const home = await page.goto(`${site}/`);
    expect(home?.status()).toBe(200);
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.getByRole('heading', { level: 1 })).toContainText('للدفع عند الاستلام');
    await expect(page.getByRole('link', { name: 'أنشئ متجرك مجانًا' }).first()).toHaveAttribute('href', 'http://localhost:5173/signup');
    await expect(page.getByRole('link', { name: 'تواصل معنا على واتساب' }).first()).toBeVisible();

    await page.getByRole('navigation', { name: 'القائمة الرئيسية' }).getByRole('link', { name: 'الأسعار' }).click();
    await expect(page).toHaveURL(`${site}/pricing`);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('أسعار واضحة، تبدأ من الصفر');
    // The cycle switch is CSS only: annual prices show once it is chosen.
    const annual = page.getByText('في السنة').first();
    await expect(annual).toBeHidden();
    await page.getByText('سنوي', { exact: true }).click();
    await expect(annual).toBeVisible();
    expect(errors).toEqual([]);
  });
});
