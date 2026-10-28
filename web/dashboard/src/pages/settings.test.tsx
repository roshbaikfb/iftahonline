import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { Role } from '../lib/settings';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Domains } from './Domains';
import { StoreSettings } from './StoreSettings';
import { Team } from './Team';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const owner = '01M5USER000000000000000001';
const helper = '01M5USER000000000000000002';
const pendingID = '01M5DOM0000000000000000002';

function session(role: Role): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: owner, email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const storeBody = { id: store, name: 'متجر منى', slug: 'mona', status: 'active', plan: 'free', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo', created_at: '', updated_at: '' };
const platform = { id: '01M5DOM0000000000000000001', host: 'mona.iftahonline.com', kind: 'subdomain', status: 'verified', is_primary: true, created_at: '' };
const pending = {
  id: pendingID, host: 'shop.mona.com', kind: 'custom', status: 'failed', failure_reason: 'txt_record_not_found', is_primary: false, created_at: '',
  verification: { record_type: 'TXT', name: '_iftah-verify.shop.mona.com', value: 'iftah-verify=k3j5' },
};

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: storeBody }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/payments/settings`]: () => ({ status: 200, body: { default_method: 'cod', card_enabled: false } }),
    [`GET ${base}/domains`]: () => ({ status: 200, body: { items: [platform, pending] } }),
    [`GET ${base}/members`]: () => ({
      status: 200,
      body: { items: [{ user_id: owner, name: 'منى', email: 'mona@example.com', role: 'owner', joined_at: '' }, { user_id: helper, name: 'سارة', email: 'sara@example.com', role: 'support', joined_at: '' }] },
    }),
    [`GET ${base}/invitations`]: () => ({ status: 200, body: { items: [] } }),
    ...over,
  });
}

function renderAt(path: string, role: Role = 'owner') {
  return render(
    <SessionContext.Provider value={session(role)}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/settings" element={<StoreSettings />} />
          <Route path="/stores/:storeId/domains" element={<Domains />} />
          <Route path="/stores/:storeId/team" element={<Team />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('StoreSettings', () => {
  it('saves the name, language and time zone, and the payment checkout starts on', async () => {
    const calls = routes({
      [`PATCH ${base}`]: (c) => ({ status: 200, body: { ...storeBody, ...(c.body as object) } }),
      [`PUT ${base}/payments/settings`]: (c) => ({ status: 200, body: { ...(c.body as object), card_enabled: false } }),
    });
    renderAt(`/stores/${store}/settings`);
    const user = userEvent.setup();
    const name = await screen.findByLabelText('اسم المتجر');
    const save = screen.getByRole('button', { name: 'حفظ التغييرات' });
    expect(save).toBeDisabled();
    expect(screen.getByText('الجنيه المصري (ج.م)')).toBeInTheDocument();

    await user.clear(name);
    await user.type(name, 'منى ستور ');
    await user.click(screen.getByRole('button', { name: 'English' }));
    await user.selectOptions(screen.getByLabelText('المنطقة الزمنية'), 'الرياض (السعودية)');
    await user.click(save);
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ name: 'منى ستور', locale: 'en', timezone: 'Asia/Riyadh' });
    });
    expect(await screen.findByText('حُفظت الإعدادات.')).toBeInTheDocument();
    expect(save).toBeDisabled();

    // no gateway: card cannot be the default
    expect(await screen.findByRole('radio', { name: /الدفع بالبطاقة/ })).toBeDisabled();
    expect(screen.getByRole('radio', { name: /الدفع عند الاستلام/ })).toBeChecked();
    const payCard = screen.getByRole('heading', { name: 'طريقة الدفع الافتراضية' }).closest('section');
    if (!payCard) throw new Error('no payment card');
    expect(within(payCard).getByRole('link', { name: 'الدفع بالبطاقة' })).toHaveAttribute('href', `/stores/${store}/gateways`);
  });

  it('chooses card when a gateway is connected, and hides the choice from those who cannot make it', async () => {
    const calls = routes({
      [`GET ${base}/payments/settings`]: () => ({ status: 200, body: { default_method: 'cod', card_enabled: true } }),
      [`PUT ${base}/payments/settings`]: (c) => ({ status: 200, body: { ...(c.body as object), card_enabled: true } }),
    });
    const { unmount } = renderAt(`/stores/${store}/settings`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('radio', { name: /الدفع بالبطاقة/ }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ default_method: 'card' });
    });
    expect(await screen.findByText('حُفظ.')).toBeInTheDocument();
    unmount();

    routes({ [`GET ${base}/payments/settings`]: () => ({ status: 403, body: { title: 'Forbidden', status: 403 } }) });
    renderAt(`/stores/${store}/settings`, 'manager');
    expect(await screen.findByLabelText('اسم المتجر')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'طريقة الدفع الافتراضية' })).toBeNull();
  });
});

describe('Domains', () => {
  it('shows the TXT steps of a domain not verified yet, checks it again, and adds one', async () => {
    const calls = routes({
      [`POST ${base}/domains/${pendingID}/verify`]: () => ({ status: 202, body: { ...pending, status: 'pending', failure_reason: undefined } }),
      [`POST ${base}/domains`]: (c) => ({
        status: 201,
        body: { ...pending, id: '01M5DOM0000000000000000003', status: 'pending', failure_reason: undefined, host: (c.body as { host: string }).host },
      }),
    });
    renderAt(`/stores/${store}/domains`);
    const user = userEvent.setup();
    const custom = (await screen.findByText('shop.mona.com')).closest('li');
    if (!custom) throw new Error('no domain row');
    expect(within(custom).getByText('فشل التحقق')).toBeInTheDocument();
    expect(within(custom).getByText(/لم نجد سجل TXT/)).toBeInTheDocument();
    expect(within(custom).getByText('_iftah-verify.shop.mona.com')).toBeInTheDocument();
    expect(within(custom).getByText('iftah-verify=k3j5')).toBeInTheDocument();
    expect(within(custom).getByText('mona.iftahonline.com')).toBeInTheDocument(); // the CNAME target
    const main = screen.getAllByText('mona.iftahonline.com')[0]?.closest('li');
    if (!main) throw new Error('no platform row');
    expect(within(main).getByText('العنوان الرئيسي')).toBeInTheDocument();
    expect(within(main).queryByRole('button', { name: 'إزالة' })).toBeNull();

    await user.click(within(custom).getByRole('button', { name: 'تحقّق الآن' }));
    expect(await within(custom).findByText('بانتظار التحقق')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST' && c.path.endsWith('/verify'))).toBe(true);

    await user.type(screen.getByLabelText('الدومين'), 'https://Store.Mona.com/');
    await user.click(screen.getByRole('button', { name: 'أضف' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST' && c.path === `${base}/domains`)?.body).toEqual({ host: 'store.mona.com' });
    });
    expect(await screen.findByText('store.mona.com')).toBeInTheDocument();
  });

  it('makes a working domain the main one and removes another', async () => {
    const verified = { ...pending, status: 'verified', failure_reason: undefined, verification: undefined };
    const calls = routes({
      [`GET ${base}/domains`]: () => ({ status: 200, body: { items: [platform, verified] } }),
      [`POST ${base}/domains/${pendingID}/primary`]: () => ({ status: 200, body: { ...verified, is_primary: true } }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/domains`);
    const user = userEvent.setup();
    const row = (await screen.findByText('shop.mona.com')).closest('li');
    if (!row) throw new Error('no row');
    expect(within(row).getByText('يعمل')).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: 'اجعله الرئيسي' }));
    expect(await within(row).findByText('العنوان الرئيسي')).toBeInTheDocument();
    expect(calls.some((c) => c.path.endsWith('/primary'))).toBe(true);
    // the old main one is not main any more, and the new one cannot be removed
    const old = screen.getByText('mona.iftahonline.com').closest('li');
    if (!old) throw new Error('no platform row');
    expect(within(old).queryByText('العنوان الرئيسي')).toBeNull();
    expect(within(row).queryByRole('button', { name: 'إزالة' })).toBeNull();
  });

  it('shows support staff the addresses without changing them', async () => {
    routes();
    renderAt(`/stores/${store}/domains`, 'support');
    expect(await screen.findByText('shop.mona.com')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'تحقّق الآن' })).toBeNull();
    expect(screen.queryByLabelText('الدومين')).toBeNull();
  });
});

describe('Team', () => {
  it('invites by email with a described role, changes a role, removes and revokes', async () => {
    const calls = routes({
      [`POST ${base}/invitations`]: (c) => ({
        status: 201,
        body: { id: '01M5INV0000000000000000001', ...(c.body as object), created_at: '2026-10-28T10:00:00Z', expires_at: '2026-11-04T10:00:00Z' },
      }),
      [`PATCH ${base}/members/${helper}`]: () => ({ status: 204 }),
      [`DELETE ${base}/members/${helper}`]: () => ({ status: 204 }),
      [`DELETE ${base}/invitations/01M5INV0000000000000000001`]: () => ({ status: 204 }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/team`);
    const user = userEvent.setup();
    const me = (await screen.findByText('(أنت)')).closest('li');
    if (!me) throw new Error('no own row');
    expect(within(me).getByText('المالك')).toBeInTheDocument();
    expect(within(me).queryByRole('combobox')).toBeNull();

    expect(screen.getByRole('radio', { name: /خدمة العملاء.*يرد على العملاء/ })).toBeChecked();
    expect(screen.getByRole('radio', { name: /الشحن.*يجهّز الطلبات/ })).toBeInTheDocument();
    await user.type(screen.getByLabelText('البريد الإلكتروني'), 'ali@example.com');
    await user.click(screen.getByRole('radio', { name: /مدير/ }));
    await user.click(screen.getByRole('button', { name: 'أرسل الدعوة' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ email: 'ali@example.com', role: 'manager' });
    });
    expect(await screen.findByText('أرسلنا الدعوة إلى ali@example.com. الرابط صالح 7 أيام.')).toBeInTheDocument();
    expect(screen.getByText('دعوات لم تُقبل بعد')).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText('دور سارة'), 'الشحن');
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ role: 'shipping' });
    });
    await user.click(screen.getByRole('button', { name: 'إزالة' }));
    await waitFor(() => {
      expect(screen.queryByText('سارة')).toBeNull();
    });
    await user.click(screen.getByRole('button', { name: 'إلغاء الدعوة' }));
    await waitFor(() => {
      expect(screen.queryByText('دعوات لم تُقبل بعد')).toBeNull();
    });
  });

  it('shows others the team without managing it', async () => {
    const calls = routes();
    renderAt(`/stores/${store}/team`, 'manager');
    expect(await screen.findByText('سارة')).toBeInTheDocument();
    expect(screen.getByText('خدمة العملاء')).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.queryByRole('heading', { name: 'ادعُ عضوًا' })).toBeNull();
    expect(screen.getByText('المالك وحده يدعو الأعضاء ويغيّر أدوارهم.')).toBeInTheDocument();
    expect(calls.some((c) => c.path.endsWith('/invitations'))).toBe(false);
  });
});
