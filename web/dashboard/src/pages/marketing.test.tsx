import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Feeds } from './Feeds';
import { Pixels } from './Pixels';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

const session: Session = {
  state: { kind: 'signed-in', me: { user: { id: 'U1', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
  reload: vi.fn(() => Promise.resolve()),
  signOut: vi.fn(() => Promise.resolve()),
};

const providers = [
  { provider: 'meta', label: false, test_code: true, server: true },
  { provider: 'tiktok', label: false, test_code: true, server: true },
  { provider: 'snap', label: false, test_code: false, server: true },
  { provider: 'google_ads', label: true, test_code: false, server: false },
  { provider: 'ga4', label: false, test_code: false, server: true },
  { provider: 'pinterest', label: false, test_code: false, server: false }, // a provider this build does not know
];
const pixel = (o: object) => ({ label: '', test_code: '', enabled: true, has_server_token: false, version: 1, updated_at: '', ...o });

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
    [`GET ${base}/marketing/pixels`]: () => ({
      status: 200,
      body: {
        providers,
        items: [
          pixel({ provider: 'meta', pixel_id: '123456789012345', has_server_token: true }),
          pixel({ provider: 'tiktok', pixel_id: 'C4ABCDEF1234567890AB' }),
          pixel({ provider: 'ga4', pixel_id: 'G-ABC123XYZ', enabled: false, has_server_token: true }),
        ],
      },
    }),
    [`GET ${base}/marketing/settings`]: () => ({ status: 200, body: { consent: 'auto', version: 2 } }),
    ...over,
  });
}

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/pixels" element={<Pixels />} />
          <Route path="/stores/:storeId/feeds" element={<Feeds />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Pixels', () => {
  it('lists the providers with their state, and skips one it does not know', async () => {
    routes();
    renderAt(`/stores/${store}/pixels`);
    const list = (await screen.findByRole('heading', { name: 'المنصات' })).closest('section');
    if (!list) throw new Error('no list');
    const meta = within(list).getByRole('button', { name: /Meta/ });
    expect(meta).toHaveAttribute('aria-pressed', 'true');
    expect(meta).toHaveTextContent('من المتصفح ومن الخادم');
    expect(within(list).getByRole('button', { name: /TikTok/ })).toHaveTextContent('من المتصفح فقط');
    expect(within(list).getByRole('button', { name: /Snapchat/ })).toHaveTextContent('غير مربوط');
    expect(within(list).getByRole('button', { name: /Google Analytics 4/ })).toHaveTextContent('متوقف');
    expect(within(list).getAllByRole('button')).toHaveLength(5);
    // Meta's token is saved: never shown, replaceable.
    expect(screen.getByText('محفوظ ومشفّر · لا يظهر مرة أخرى')).toBeInTheDocument();
    expect(screen.getByLabelText('معرّف البكسل (Pixel ID)')).toHaveValue('123456789012345');
  });

  it('connects a provider, checking its ID first', async () => {
    const calls = routes({
      [`PUT ${base}/marketing/pixels/snap`]: (c) => ({ status: 200, body: pixel({ provider: 'snap', ...(c.body as object), has_server_token: true, version: 1 }) }),
    });
    renderAt(`/stores/${store}/pixels`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Snapchat/ }));
    expect(screen.getByRole('heading', { name: 'Snapchat' })).toBeInTheDocument();
    expect(screen.queryByLabelText('رمز الاختبار (اختياري)')).toBeNull();
    const id = screen.getByLabelText('معرّف البكسل (Pixel ID)');
    await user.type(id, '12345');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    expect(screen.getByText(/انسخه كما يظهر في حسابك/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);

    await user.clear(id);
    await user.type(id, ' 0f8fad5b-d9cb-469f-a165-70867728950e ');
    await user.type(screen.getByLabelText('رمز Conversions API'), 'snap-token-123');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({
        pixel_id: '0f8fad5b-d9cb-469f-a165-70867728950e',
        enabled: true,
        version: 0,
        server_token: 'snap-token-123',
      });
    });
    expect(await screen.findByText('رُبط Snapchat.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Snapchat/ })).toHaveTextContent('من المتصفح ومن الخادم');
  });

  it('keeps a saved token unless replaced, and asks Google Ads for its label', async () => {
    const calls = routes({
      [`PUT ${base}/marketing/pixels/meta`]: (c) => ({ status: 200, body: pixel({ provider: 'meta', ...(c.body as object), has_server_token: true, version: 2 }) }),
      [`PUT ${base}/marketing/pixels/google_ads`]: () => ({ status: 200, body: pixel({ provider: 'google_ads', pixel_id: 'AW-123456789', label: 'AbC-123_xyz' }) }),
    });
    renderAt(`/stores/${store}/pixels`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('رمز الاختبار (اختياري)'), 'TEST1');
    await user.click(screen.getByRole('button', { name: 'حفظ' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ pixel_id: '123456789012345', enabled: true, version: 1, test_code: 'TEST1' });
    });
    await user.click(screen.getByRole('button', { name: 'استبدال' }));
    expect(screen.getByLabelText('رمز Conversions API')).toHaveAttribute('type', 'password');

    await user.click(screen.getByRole('button', { name: /Google Ads/ }));
    expect(screen.getByText('من المتصفح فقط حاليًا.')).toBeInTheDocument();
    await user.type(screen.getByLabelText('معرّف التحويل (Conversion ID)'), 'AW-123456789');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    expect(screen.getByText('تجده في إعداد تحويل الشراء في Google Ads.')).toBeInTheDocument();
    await user.type(screen.getByLabelText(/^Conversion label/), 'AbC-123_xyz');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    await waitFor(() => {
      expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({ pixel_id: 'AW-123456789', label: 'AbC-123_xyz', enabled: true, version: 0 });
    });
  });

  it('disconnects a provider and changes the consent mode', async () => {
    const calls = routes({
      [`DELETE ${base}/marketing/pixels/meta`]: () => ({ status: 204 }),
      [`PUT ${base}/marketing/settings`]: (c) => ({ status: 200, body: { ...(c.body as object), version: 3 } }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/pixels`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'فصل' }));
    expect(await screen.findByText('فُصل Meta (فيسبوك وإنستجرام).')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Meta/ })).toHaveTextContent('غير مربوط');
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);

    expect(screen.getByRole('radio', { name: /تلقائي/ })).toBeChecked();
    await user.click(screen.getByRole('radio', { name: /اسأل دائمًا/ }));
    await waitFor(() => {
      expect(calls.find((c) => c.path.endsWith('/marketing/settings') && c.method === 'PUT')?.body).toEqual({ consent: 'required', version: 2 });
    });
    expect(screen.getByRole('radio', { name: /اسأل دائمًا/ })).toBeChecked();
  });
});

describe('Feeds', () => {
  const on = { enabled: true, key: 'k'.repeat(32), google_path: `/feeds/google.xml?key=${'k'.repeat(32)}`, meta_path: `/feeds/meta.xml?key=${'k'.repeat(32)}` };

  it('turns the feeds on, shows both links on the store\'s domain, copies, renews and turns them off', async () => {
    const renewed = { ...on, key: 'n'.repeat(32), google_path: `/feeds/google.xml?key=${'n'.repeat(32)}`, meta_path: `/feeds/meta.xml?key=${'n'.repeat(32)}` };
    let next = on;
    const calls = routes({
      [`GET ${base}/marketing/feed`]: () => ({ status: 200, body: { enabled: false } }),
      [`POST ${base}/marketing/feed`]: () => {
        const body = next;
        next = renewed;
        return { status: 200, body };
      },
      [`DELETE ${base}/marketing/feed`]: () => ({ status: 204 }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/feeds`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { name: 'الكتالوج متوقف' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'تشغيل الكتالوج' }));

    const google = await screen.findByRole('heading', { name: 'Google Merchant Center' });
    const card = google.closest('section');
    if (!card) throw new Error('no card');
    const url = within(card).getByLabelText('رابط الكتالوج');
    expect(url).toHaveValue(`http://mona.iftahonline.localhost:18081/feeds/google.xml?key=${'k'.repeat(32)}`);
    expect(screen.getAllByLabelText('رابط الكتالوج')[1]).toHaveValue(`http://mona.iftahonline.localhost:18081/feeds/meta.xml?key=${'k'.repeat(32)}`);
    await user.click(within(card).getByRole('button', { name: 'نسخ رابط Google Merchant Center' }));
    expect(await navigator.clipboard.readText()).toBe(`http://mona.iftahonline.localhost:18081/feeds/google.xml?key=${'k'.repeat(32)}`);
    expect(await within(card).findByText('نُسخ')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'تجديد المفتاح' }));
    await waitFor(() => {
      expect(within(card).getByLabelText('رابط الكتالوج')).toHaveValue(`http://mona.iftahonline.localhost:18081/feeds/google.xml?key=${'n'.repeat(32)}`);
    });
    expect(screen.getByText(/جُدّد المفتاح/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'إيقاف الكتالوج' }));
    expect(await screen.findByRole('heading', { name: 'الكتالوج متوقف' })).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(2);
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
  });
});
