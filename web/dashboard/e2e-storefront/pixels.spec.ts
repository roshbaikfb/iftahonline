import { expect, test, type Page, type Route } from '@playwright/test';

// The ad pixels (ADR-0043) on the demo store pixels.localhost: Meta,
// TikTok, Snap, Google Ads and GA4 connected, consent asked first. The
// providers' scripts are stood in for here, so nothing leaves the
// machine: each stand-in records what px.js asked of its tag in
// window.__px, the way the real tag would receive it.

const store = 'http://pixels.localhost:18191';

type Call = [string, ...unknown[]];

// Each stand-in replays what was queued before it loaded, then records
// every later call.
const standIns: Record<string, string> = {
  'https://connect.facebook.net/**': `
    window.__px = window.__px || [];
    const f = window.fbq;
    f.callMethod = (...a) => window.__px.push(['meta', ...a]);
    for (const a of f.queue) f.callMethod(...a);`,
  'https://analytics.tiktok.com/**': `
    window.__px = window.__px || [];
    for (const a of window.ttq) window.__px.push(['tiktok', ...a]);
    window.ttq.push = (a) => window.__px.push(['tiktok', ...a]);`,
  'https://sc-static.net/**': `
    window.__px = window.__px || [];
    const s = window.snaptr;
    s.handleRequest = (...a) => window.__px.push(['snap', ...a]);
    for (const a of s.queue) s.handleRequest(...a);`,
  'https://www.googletagmanager.com/**': `
    window.__px = window.__px || [];
    for (const a of window.dataLayer) window.__px.push(['gtag', ...a]);
    window.dataLayer.push = (a) => window.__px.push(['gtag', ...a]);`,
};

// standIn serves the providers' stand-ins, counts their loads, and fails
// the test on a request to any other outside origin.
async function standIn(page: Page): Promise<{ loads: Record<string, number>; strays: string[] }> {
  const loads: Record<string, number> = {};
  const strays: string[] = [];
  for (const [pattern, body] of Object.entries(standIns)) {
    loads[pattern] = 0;
    await page.route(pattern, (route: Route) => {
      loads[pattern] = (loads[pattern] ?? 0) + 1;
      return route.fulfill({ contentType: 'text/javascript', body });
    });
  }
  // Registered last, so it runs first (Playwright's order): a stand-in's
  // URL falls through to its route.
  await page.route(/^https?:\/\/(?!pixels\.localhost)/, (route: Route) => {
    if (Object.keys(standIns).some((p) => route.request().url().startsWith(p.replace('/**', '')))) return route.fallback();
    strays.push(route.request().url());
    return route.abort();
  });
  return { loads, strays };
}

function watch(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });
  page.on('pageerror', (err) => errors.push(err.message));
  return errors;
}

const calls = (page: Page): Promise<Call[]> => page.evaluate(() => JSON.parse(JSON.stringify((globalThis as unknown as { __px?: Call[] }).__px ?? [])) as Call[]);

// eventIDs are the event_ids px.js handed Meta for an event name.
async function metaEventIDs(page: Page, name: string): Promise<string[]> {
  return (await calls(page))
    .filter((c) => c[0] === 'meta' && c[1] === 'trackSingle' && c[3] === name)
    .map((c) => ((c[5] ?? {}) as { eventID?: string }).eventID ?? '');
}

// pageEventID is the event_id the server wrote into the page.
async function pageEventID(page: Page, name: string): Promise<string> {
  const cfg = JSON.parse((await page.locator('#px').textContent()) ?? '{}') as { ev?: { n: string; id?: string }[] };
  return cfg.ev?.find((e) => e.n === name)?.id ?? '';
}

test('pixels wait for consent, then report the journey with the server’s event_ids', async ({ page }) => {
  const errors = watch(page);
  const { loads, strays } = await standIn(page);

  // Nothing loads before the shopper answers.
  // The shopper comes from an ad: app.js keeps its campaign for the order.
  const res = await page.goto(`${store}/p/musk?utm_source=e2e&utm_campaign=eid&fbclid=IwAR0`);
  expect(res?.headers()['content-security-policy']).toContain('script-src \'self\' https://connect.facebook.net');
  const banner = page.locator('[data-consent]');
  await expect(banner).toBeVisible();
  await page.waitForLoadState('load');
  expect(Object.values(loads).every((n) => n === 0), 'no tag before consent').toBe(true);

  // Accepting loads every provider once, with the page view and the product.
  await banner.getByRole('button', { name: 'موافق' }).click();
  await expect(banner).toBeHidden();
  const cookies = Object.fromEntries((await page.context().cookies(store)).map((c) => [c.name, c.value]));
  expect(cookies.px_c, 'the answer reaches the server').toBe('yes');
  const source = new URLSearchParams(decodeURIComponent(cookies.ift_src ?? ''));
  expect([source.get('utm_source'), source.get('utm_campaign'), source.get('fbclid')]).toEqual(['e2e', 'eid', 'IwAR0']);
  expect(source.get('ts')).toMatch(/^\d{13}$/);
  await expect.poll(async () => (await metaEventIDs(page, 'ViewContent')).length).toBe(1);
  expect(Object.values(loads), 'each tag once; Google Ads and GA4 share gtag').toEqual([1, 1, 1, 1]);
  const all = await calls(page);
  expect(all).toContainEqual(['meta', 'init', '123456789012345']);
  expect(all).toContainEqual(['meta', 'track', 'PageView']);
  expect(all.some((c) => c[0] === 'tiktok' && c[1] === 'track' && c[2] === 'ViewContent')).toBe(true);
  expect(all.some((c) => c[0] === 'snap' && c[1] === 'track' && c[2] === 'VIEW_CONTENT')).toBe(true);
  expect(all.some((c) => c[0] === 'gtag' && c[1] === 'event' && c[2] === 'view_item')).toBe(true);
  const vc = all.find((c) => c[0] === 'meta' && c[3] === 'ViewContent');
  expect(vc?.[4]).toMatchObject({ value: 250, currency: 'EGP', content_type: 'product' });

  // The choice is remembered: the next page loads the tags at once.
  await page.locator('[data-total]').click();
  await expect(page).toHaveURL(/\/cart$/);
  await expect(banner).toHaveCount(1);
  await expect(banner).toBeHidden();
  const added = await pageEventID(page, 'AddToCart');
  expect(added).toMatch(/^[0-9A-Z]{26}$/);
  await expect.poll(() => metaEventIDs(page, 'AddToCart')).toEqual([added]);
  const ttq = (await calls(page)).find((c) => c[0] === 'tiktok' && c[2] === 'AddToCart');
  expect(ttq?.[4]).toEqual({ event_id: added });

  await page.getByRole('link', { name: 'إتمام الطلب' }).click();
  const started = await pageEventID(page, 'InitiateCheckout');
  await expect.poll(() => metaEventIDs(page, 'InitiateCheckout')).toEqual([started]);

  await page.getByLabel('الاسم').fill('هدى سمير');
  await page.getByLabel('رقم الموبايل').fill('01033334444');
  await page.getByLabel('المحافظة').selectOption({ label: 'القاهرة' });
  await page.getByLabel('المدينة / المنطقة').fill('المعادي');
  await page.getByLabel('العنوان بالتفصيل').fill('5 شارع 9');
  await page.getByRole('button', { name: 'تأكيد الطلب' }).click();
  await expect(page).toHaveURL(/\/thank-you\/[0-9A-Z]{26}$/);
  const order = page.url().split('/').pop() ?? '';

  // Purchase: the order's event_id, which the server sends too.
  await expect.poll(() => metaEventIDs(page, 'Purchase')).toEqual([`purchase:${order}`]);
  const purchase = await calls(page);
  expect(purchase.find((c) => c[0] === 'gtag' && c[2] === 'conversion')?.[3]).toMatchObject({ send_to: 'AW-123456789/AbC-123_xyz', transaction_id: order, currency: 'EGP' });
  expect(purchase.find((c) => c[0] === 'gtag' && c[2] === 'purchase')?.[3]).toMatchObject({ transaction_id: order });
  expect(purchase.find((c) => c[0] === 'snap' && c[2] === 'PURCHASE')?.[3]).toMatchObject({ client_dedup_id: `purchase:${order}`, transaction_id: order });
  expect(purchase.some((c) => c[0] === 'tiktok' && c[2] === 'CompletePayment')).toBe(true);

  // A reload reports the purchase no second time.
  await page.reload();
  await expect.poll(async () => (await calls(page)).some((c) => c[0] === 'meta' && c[2] === 'PageView')).toBe(true);
  expect(await metaEventIDs(page, 'Purchase')).toEqual([]);

  expect(strays, 'no request to an origin we did not stand in for').toEqual([]);
  expect(errors, 'no console errors (CSP violations, script errors)').toEqual([]);
});

test('declining keeps every tag away, and stays declined', async ({ page }) => {
  const { loads } = await standIn(page);
  await page.goto(`${store}/`);
  await page.locator('[data-consent]').getByRole('button', { name: 'لا، شكرًا' }).click();
  await expect(page.locator('[data-consent]')).toBeHidden();
  await page.goto(`${store}/p/musk`);
  await page.waitForLoadState('load');
  await expect(page.locator('[data-consent]')).toBeHidden();
  await page.waitForTimeout(500); // px.js would start on idle
  expect(Object.values(loads).every((n) => n === 0)).toBe(true);
  expect(await calls(page)).toEqual([]);
});

test('a store without pixels loads no tag and asks nothing', async ({ page }) => {
  const res = await page.goto('/p/royal-oud');
  expect(res?.headers()['content-security-policy']).toContain("script-src 'self';");
  await expect(page.locator('#px')).toHaveCount(0);
  await expect(page.locator('[data-consent]')).toHaveCount(0);
});
