import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { Layout } from '../components/Layout';
import { ago, forgetNotices, smsParts } from '../lib/messages';
import type { Role } from '../lib/settings';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { MessageLog } from './MessageLog';
import { Messages } from './Messages';
import { OrderDetail } from './OrderDetail';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const orderID = '01M6ORDER00000000YM2ECZRR';

function session(role: Role): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'U1', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const arDefault = '{{store}}: أهلاً {{name}}، أكّد طلبك رقم {{ref}} ({{total}}) من هنا: {{url}}';
const enDefault = '{{store}}: Hi {{name}}, please confirm your order {{ref}} ({{total}}): {{url}}';

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
    [`GET ${base}/messaging/settings`]: () => ({ status: 200, body: { confirm_cod: true, auto_cancel_hours: 24 } }),
    [`GET ${base}/messaging/usage`]: () => ({ status: 200, body: { period: '2026-10-01', used: 412, limit: 500, remaining: 88, level: 'nearly' } }),
    [`GET ${base}/messaging/texts`]: () => ({
      status: 200,
      body: {
        items: [
          { kind: 'order_confirmation', locale: 'ar', sms_body: arDefault, custom: false },
          { kind: 'order_confirmation', locale: 'en', sms_body: enDefault, custom: false },
        ],
      },
    }),
    ...over,
  });
}

function renderAt(path: string, role: Role = 'owner') {
  return render(
    <SessionContext.Provider value={session(role)}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/messages" element={<Messages />} />
          <Route path="/stores/:storeId/messages/log" element={<MessageLog />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<OrderDetail />} />
          <Route
            path="/stores/:storeId/home"
            element={
              <Layout storeId={store}>
                <h1>الرئيسية</h1>
              </Layout>
            }
          />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  forgetNotices();
});

describe('Messages', () => {
  it('turns confirmation off and on, and saves the auto-cancel hours on leaving the field', async () => {
    const calls = routes({ [`PUT ${base}/messaging/settings`]: (c) => ({ status: 200, body: c.body }) });
    renderAt(`/stores/${store}/messages`);
    const user = userEvent.setup();
    const toggle = await screen.findByRole('button', { name: 'تأكيد طلبات الدفع عند الاستلام' });
    expect(toggle).toHaveAttribute('aria-pressed', 'true');

    const hours = screen.getByLabelText('إلغاء تلقائي إن لم يرد العميل خلال');
    await user.clear(hours);
    await user.type(hours, '100');
    await user.tab();
    expect(screen.getByText('من 1 إلى 72 ساعة.')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
    await user.clear(hours);
    await user.type(hours, '12');
    await user.tab();
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ confirm_cod: true, auto_cancel_hours: 12 });
    });
    expect(await screen.findByText('حُفظت الإعدادات.')).toBeInTheDocument();

    await user.click(toggle);
    await waitFor(() => {
      expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({ confirm_cod: false, auto_cancel_hours: 12 });
    });
    expect(await screen.findByText(/التأكيد متوقف/)).toBeInTheDocument();
    expect(screen.queryByLabelText('إلغاء تلقائي إن لم يرد العميل خلال')).toBeNull();
  });

  it('edits the SMS text with a live preview, refuses one without the link, saves and resets it', async () => {
    const calls = routes({
      [`PUT ${base}/messaging/texts/order_confirmation/ar`]: (c) => ({
        status: 200,
        body: { kind: 'order_confirmation', locale: 'ar', sms_body: (c.body as { sms_body: string }).sms_body, custom: true },
      }),
      [`DELETE ${base}/messaging/texts/order_confirmation/ar`]: () => ({ status: 200, body: { kind: 'order_confirmation', locale: 'ar', sms_body: arDefault, custom: false } }),
    });
    renderAt(`/stores/${store}/messages`);
    const user = userEvent.setup();
    const text = await screen.findByLabelText('النص');
    expect(screen.getByTestId('sms-preview')).toHaveTextContent('متجر منى: أهلاً سارة، أكّد طلبك رقم YM2ECZRR (500 ج.م) من هنا: mona.iftahonline.com/o/k7Q2xR');
    const save = screen.getByRole('button', { name: 'حفظ النص' });
    expect(save).toBeDisabled();

    await user.clear(text);
    await user.type(text, 'أكّد طلبك يا {{{{name}}');
    expect(screen.getByText(/لا بد أن يحوي النص «رابط التأكيد»/)).toBeInTheDocument();
    expect(save).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'رابط التأكيد' }));
    expect(text).toHaveValue('أكّد طلبك يا {{name}}{{url}}');
    expect(screen.getByTestId('sms-preview')).toHaveTextContent('أكّد طلبك يا سارةmona.iftahonline.com/o/k7Q2xR');
    await user.click(save);
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ sms_body: 'أكّد طلبك يا {{name}}{{url}}' });
    });
    expect(await screen.findByText('نصّك')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'الرجوع للنص الافتراضي' }));
    await waitFor(() => {
      expect(text).toHaveValue(arDefault);
    });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);

    // English has its own text, read left to right.
    await user.click(screen.getByRole('button', { name: 'English' }));
    expect(screen.getByLabelText('النص')).toHaveValue(enDefault);
    expect(screen.getByLabelText('النص')).toHaveAttribute('dir', 'ltr');
  });

  it('shows the month\'s quota, warning at 80%', async () => {
    routes();
    renderAt(`/stores/${store}/messages`);
    const card = (await screen.findByRole('heading', { name: /رصيد الرسائل لشهر/ })).closest('section');
    if (!card) throw new Error('no quota card');
    expect(within(card).getByText('412')).toBeInTheDocument();
    expect(within(card).getByRole('meter')).toHaveAttribute('aria-valuenow', '412');
    expect(within(card).getByText(/استهلكت 82%/)).toBeInTheDocument();
    expect(within(card).getByText(/يتجدد الرصيد في 1 نوفمبر/)).toBeInTheDocument();
  });

  it('lets support read but not change', async () => {
    routes();
    renderAt(`/stores/${store}/messages`, 'support');
    expect(await screen.findByRole('button', { name: 'تأكيد طلبات الدفع عند الاستلام' })).toBeDisabled();
    expect(await screen.findByLabelText('النص')).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'حفظ النص' })).toBeNull();
    expect(screen.queryByRole('group', { name: 'أدرج متغيرًا' })).toBeNull();
  });
});

describe('MessageLog', () => {
  const message = (over: object) => ({
    id: 'M1',
    kind: 'order_confirmation',
    locale: 'ar',
    status: 'read',
    channel: 'whatsapp',
    to: '+20 10•• ••• 678',
    fallback: false,
    order_id: orderID,
    created_at: '2026-10-29T12:02:00Z',
    updated_at: '2026-10-29T12:05:00Z',
    ...over,
  });

  it('lists messages, filters by status, and pages', async () => {
    const calls = routes({
      [`GET ${base}/messages`]: (c) =>
        c.search.get('status') === 'failed'
          ? { status: 200, body: { items: [message({ id: 'M3', status: 'failed', error_code: 'undeliverable' })] } }
          : c.search.get('cursor') === 'next'
            ? { status: 200, body: { items: [message({ id: 'M4', kind: 'order_answer_ack', status: 'delivered' })] } }
            : { status: 200, body: { items: [message({ reply: 'confirm' }), message({ id: 'M2', channel: 'sms', fallback: true, status: 'delivered' })], next_cursor: 'next' } },
    });
    renderAt(`/stores/${store}/messages/log`);
    const user = userEvent.setup();
    await screen.findAllByRole('link', { name: '#YM2ECZRR' });
    const rows = screen.getAllByRole('row');
    expect(rows).toHaveLength(3);
    const [, first, second] = rows;
    if (!first || !second) throw new Error('rows missing');
    expect(within(first).getByRole('link', { name: '#YM2ECZRR' })).toHaveAttribute('href', `/stores/${store}/orders/${orderID}`);
    expect(within(first).getByText('أكّد')).toBeInTheDocument();
    expect(within(second).getByText('بديل عن واتساب')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'المزيد' }));
    expect(await screen.findByText('شكر على الرد')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'المزيد' })).toBeNull();

    await user.click(screen.getByRole('tab', { name: 'لم تصل' }));
    expect(await screen.findByText('الرقم لا يستقبل الرسائل')).toBeInTheDocument();
    expect(screen.getAllByRole('row')).toHaveLength(2);
    expect(calls.filter((c) => c.path === `${base}/messages`).at(-1)?.search.get('status')).toBe('failed');
  });

  it("shows one order's messages", async () => {
    const calls = routes({ [`GET ${base}/messages`]: () => ({ status: 200, body: { items: [message({})] } }) });
    renderAt(`/stores/${store}/messages/log?order=${orderID}`);
    expect(await screen.findByText(/رسائل الطلب/)).toBeInTheDocument();
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(calls.find((c) => c.path === `${base}/messages`)?.search.get('order_id')).toBe(orderID);
  });
});

describe('NotificationBell', () => {
  const notices = [
    { id: 'N1', kind: 'confirmation_unreached', order_id: orderID, order_ref: 'YM2ECZRR', read: false, created_at: new Date(Date.now() - 2 * 3600e3).toISOString() },
    { id: 'N2', kind: 'quota_nearly', used: 400, limit: 500, read: false, created_at: new Date(Date.now() - 3600e3).toISOString() },
    { id: 'N3', kind: 'order_confirmed', order_id: orderID, order_ref: 'YM2ECZRR', read: true, created_at: '2026-10-28T10:00:00Z' },
  ];

  it('counts unread notices, lists them and marks them read', async () => {
    const calls = routes({
      [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: notices, unread: 2 } }),
      [`POST ${base}/messaging/notifications/read`]: (c) => ({ status: 200, body: { marked: (c.body as { ids: string[] }).ids.length } }),
    });
    renderAt(`/stores/${store}/home`);
    const user = userEvent.setup();
    const bell = await screen.findByLabelText('الإشعارات: 2 غير مقروءة');
    await user.click(bell);
    const panel = screen.getByRole('region', { name: 'الإشعارات' });
    expect(within(panel).getByText(/لم تصل رسالة التأكيد لعميل الطلب/)).toBeInTheDocument();
    expect(within(panel).getAllByRole('link', { name: '#YM2ECZRR' })).toHaveLength(2);
    expect(within(panel).getByText(/استهلكت 80% من رسائل هذا الشهر \(400 من 500\)/)).toBeInTheDocument();
    expect(within(panel).getByText('منذ ساعتين')).toBeInTheDocument();

    await user.click(within(panel).getByRole('button', { name: 'تحديد الكل كمقروء' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ ids: ['N1', 'N2'] });
    });
    await waitFor(() => {
      expect(screen.queryByLabelText(/غير مقروءة/)).toBeNull();
    });
  });

  it('asks again only after a minute, however many screens open meanwhile', async () => {
    const calls = routes({ [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: notices, unread: 2 } }) });
    let now = Date.parse('2026-10-29T12:00:00Z');
    vi.spyOn(Date, 'now').mockImplementation(() => now);
    renderAt(`/stores/${store}/home`);
    const user = userEvent.setup();
    expect(await screen.findByLabelText('الإشعارات: 2 غير مقروءة')).toBeInTheDocument();
    const reads = () => calls.filter((c) => c.path === `${base}/messaging/notifications`).length;
    expect(reads()).toBe(1);
    await user.click(screen.getByRole('link', { name: 'رسائل العملاء' }));
    expect(await screen.findByRole('heading', { name: 'رسائل العملاء', level: 1 })).toBeInTheDocument();
    expect(screen.getByLabelText('الإشعارات: 2 غير مقروءة')).toBeInTheDocument(); // the kept read, at once
    expect(reads()).toBe(1);
    now += 61_000;
    await user.click(screen.getByRole('link', { name: 'سجل الرسائل' }));
    await waitFor(() => {
      expect(reads()).toBe(2);
    });
  });

  it('is not shown to roles that cannot read messages', async () => {
    const calls = routes();
    renderAt(`/stores/${store}/home`, 'shipping');
    expect(await screen.findByRole('heading', { name: 'الرئيسية' })).toBeInTheDocument();
    expect(screen.queryByLabelText(/الإشعارات/)).toBeNull();
    expect(calls.some((c) => c.path.endsWith('/notifications'))).toBe(false);
  });
});

describe('OrderDetail', () => {
  const order = {
    id: orderID,
    code: 'YM2ECZRR',
    status: 'pending',
    version: 1,
    source: 'storefront',
    customer_id: 'CU',
    name: 'سارة علي',
    phone: '+201012345678',
    governorate: 'EG-C',
    city: 'مدينة نصر',
    address: '12 شارع عباس العقاد',
    notes: '',
    currency: 'EGP',
    subtotal_minor: 50000,
    discount_minor: 5000,
    coupon_code: 'SUMMER10',
    shipping_minor: 4000,
    cod_fee_minor: 1000,
    payment_method: 'cod',
    payment_status: 'unpaid',
    total_minor: 50000,
    item_count: 2,
    attribution: { source: 'facebook', medium: 'paid_social', campaign: 'summer_sale' },
    created_at: '2026-10-29T12:01:00Z',
    updated_at: '2026-10-29T12:01:00Z',
    status_changed_at: '2026-10-29T12:01:00Z',
  };
  const orderRoutes = {
    [`GET ${base}/orders/${orderID}`]: () => ({ status: 200, body: { order, lines: [], events: [] } }),
    [`GET ${base}/shipping/shipments`]: () => ({ status: 200, body: { items: [] } }),
    [`GET ${base}/shipping/carriers`]: () => ({ status: 200, body: { carriers: [], connections: [] } }),
  };

  it('shows the confirmation on its way, the coupon and where the order came from', async () => {
    routes({
      ...orderRoutes,
      [`GET ${base}/messaging/confirmations/${orderID}`]: () => ({
        status: 200,
        body: { order_id: orderID, status: 'pending', expires_at: '2026-10-30T12:02:00Z', created_at: '2026-10-29T12:02:00Z' },
      }),
      [`GET ${base}/messages`]: () => ({
        status: 200,
        body: {
          items: [{ id: 'M1', kind: 'order_confirmation', locale: 'ar', status: 'read', channel: 'whatsapp', to: '+20…', fallback: false, order_id: orderID, created_at: '2026-10-29T12:02:00Z', updated_at: '2026-10-29T12:05:00Z' }],
        },
      }),
    });
    renderAt(`/stores/${store}/orders/${orderID}`);
    const card = (await screen.findByRole('heading', { name: 'تأكيد العميل' })).closest('section');
    if (!card) throw new Error('no confirmation card');
    expect(within(card).getByText('بانتظار رده')).toBeInTheDocument();
    expect(within(card).getByText('أُرسلت على واتساب')).toBeInTheDocument();
    expect(within(card).getByText('وصلت وقرأها العميل')).toBeInTheDocument();
    expect(within(card).getByText(/يُلغى تلقائيًا/)).toBeInTheDocument();
    expect(within(card).getByRole('link', { name: 'رسائل هذا الطلب (1)' })).toHaveAttribute('href', `/stores/${store}/messages/log?order=${orderID}`);
    expect(screen.getByText(/أرسلنا للعميل رسالة تأكيد على واتساب/)).toBeInTheDocument();
    expect(screen.queryByText(/اتصل بالعميل لتأكيده قبل الشحن/)).toBeNull();

    expect(screen.getByText('SUMMER10')).toBeInTheDocument();
    expect(screen.getByText('−50 ج.م')).toBeInTheDocument();
    const source = screen.getByRole('heading', { name: 'مصدر الطلب' }).closest('section');
    if (!source) throw new Error('no source card');
    expect(within(source).getByText('facebook')).toBeInTheDocument();
    expect(within(source).getByText('summer_sale')).toBeInTheDocument();
  });

  it('asks the merchant to call when no message confirms the order, without asking for a confirmation', async () => {
    const calls = routes({ ...orderRoutes, [`GET ${base}/messages`]: () => ({ status: 200, body: { items: [] } }) });
    renderAt(`/stores/${store}/orders/${orderID}`);
    expect(await screen.findByText(/اتصل بالعميل لتأكيده قبل الشحن/)).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'تأكيد العميل' })).toBeNull();
    await waitFor(() => {
      expect(calls.some((c) => c.path === `${base}/messages`)).toBe(true);
    });
    // no message asked: the confirmation is not asked for (it would be a 404)
    expect(calls.some((c) => c.path.includes('/confirmations/'))).toBe(false);
  });
});

describe('helpers', () => {
  it('counts SMS parts the way operators do', () => {
    expect(smsParts('a'.repeat(160))).toBe(1);
    expect(smsParts('a'.repeat(161))).toBe(2);
    expect(smsParts('ب'.repeat(70))).toBe(1);
    expect(smsParts('ب'.repeat(71))).toBe(2);
    expect(smsParts('ب'.repeat(135))).toBe(3);
  });

  it('says how long ago in Arabic', () => {
    const now = Date.parse('2026-10-29T12:00:00Z');
    expect(ago('2026-10-29T11:59:30Z', now)).toBe('الآن');
    expect(ago('2026-10-29T11:59:00Z', now)).toBe('منذ دقيقة');
    expect(ago('2026-10-29T11:58:00Z', now)).toBe('منذ دقيقتين');
    expect(ago('2026-10-29T11:55:00Z', now)).toBe('منذ 5 دقائق');
    expect(ago('2026-10-29T11:00:00Z', now)).toBe('منذ ساعة');
    expect(ago('2026-10-29T00:00:00Z', now)).toBe('منذ 12 ساعة');
  });
});
