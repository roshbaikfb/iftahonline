import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { components } from '../api/client';
import { SessionContext, type Session } from '../session';
import { mockAPI } from '../test/api';
import { Login } from './Login';
import { OrderDetail } from './OrderDetail';
import { Orders } from './Orders';

type Order = components['schemas']['Order'];

const store = '01M5STORE0000000000000000A';

function order(id: string, status: Order['status'], extra: Partial<Order> = {}): Order {
  return {
    id, code: id.slice(-8), status, version: 1, source: 'storefront', customer_id: '01M5CUST000000000000000000',
    name: 'منى أحمد', phone: '+201012345678', governorate: 'EG-ALX', city: 'سموحة', address: '14 شارع فوزي معاذ', notes: '',
    currency: 'EGP', subtotal_minor: 34950, shipping_minor: 0, cod_fee_minor: 0, payment_method: 'cod', payment_status: 'unpaid',
    total_minor: 34950, item_count: 2,
    created_at: '2026-10-22T10:00:00Z', updated_at: '2026-10-22T10:00:00Z', status_changed_at: '2026-10-22T10:00:00Z', ...extra,
  };
}

function session(reload = vi.fn(() => Promise.resolve())): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'u', email: 'a@b.c', name: 'A', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload, signOut: vi.fn(() => Promise.resolve()),
  };
}

function renderAt(path: string, s: Session = session()) {
  return render(
    <SessionContext.Provider value={s}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/stores/:storeId/orders" element={<Orders />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<OrderDetail />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const storeRoute = { [`GET /v1/stores/${store}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى' } }) };

describe('Login', () => {
  it('signs in with email and password, then asks for the code when MFA is on', async () => {
    const reload = vi.fn(() => Promise.resolve());
    const calls = mockAPI({
      'GET /v1/meta/version': () => ({ status: 200, body: { version: 'v1', commit: 'c', go_version: 'go1.27.3', role: 'api' } }),
      'POST /v1/auth/login': () => ({ status: 202, body: { mfa_required: true, challenge_id: 'CH1', expires_at: '' } }),
      'POST /v1/auth/mfa': () => ({ status: 200, body: { user: {}, expires_at: '' } }),
    });
    renderAt('/login', session(reload));
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('البريد الإلكتروني'), 'mona@example.com');
    await user.type(screen.getByLabelText('كلمة المرور'), 'correct horse battery');
    await user.click(screen.getByRole('button', { name: 'تسجيل الدخول' }));
    await user.type(await screen.findByLabelText('رمز التحقق'), '123456');
    await user.click(screen.getByRole('button', { name: 'تأكيد' }));
    await waitFor(() => {
      expect(reload).toHaveBeenCalled();
    });
    expect(calls.find((c) => c.path === '/v1/auth/login')?.body).toEqual({ email: 'mona@example.com', password: 'correct horse battery', client: 'web' });
    expect(calls.find((c) => c.path === '/v1/auth/mfa')?.body).toEqual({ challenge_id: 'CH1', code: '123456', client: 'web' });
  });

  it('says so when the credentials are wrong', async () => {
    mockAPI({
      'GET /v1/meta/version': () => ({ status: 200, body: { version: 'v1', commit: 'c', go_version: 'go', role: 'api' } }),
      'POST /v1/auth/login': () => ({ status: 401, body: { title: 'Unauthorized', status: 401 } }),
    });
    renderAt('/login');
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('البريد الإلكتروني'), 'x@example.com');
    await user.type(screen.getByLabelText('كلمة المرور'), 'wrong');
    await user.click(screen.getByRole('button', { name: 'تسجيل الدخول' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('البريد أو كلمة المرور غير صحيحة');
  });
});

const counts = { pending: 2, confirmed: 0, ready_to_ship: 0, shipped: 1, delivered: 4, cancelled: 0, failed_delivery: 0, returned: 0 };

describe('Orders', () => {
  it('lists orders under status tabs, confirms the selected ones from the bar and exports', async () => {
    const pending1 = order('01M5ORDER00000000000000001', 'pending');
    const pending2 = order('01M5ORDER00000000000000002', 'pending', { name: 'سارة', payment_method: 'card', payment_status: 'awaiting' });
    const shipped = order('01M5ORDER00000000000000003', 'shipped', { payment_method: 'card', payment_status: 'paid' });
    const calls = mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders`]: () => ({ status: 200, body: { items: [pending1, pending2, shipped] } }),
      [`GET /v1/stores/${store}/orders/counts`]: () => ({ status: 200, body: counts }),
      [`GET /v1/stores/${store}/shipping`]: () => ({
        status: 200,
        body: { currency: 'EGP', areas: [{ code: 'EG-ALX', name_ar: 'الإسكندرية', name_en: 'Alexandria' }], settings: { cod_fee: { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 }, version: 0 }, zones: [] },
      }),
      [`POST /v1/stores/${store}/orders/transitions`]: () => ({
        status: 200,
        body: { results: [{ order_id: pending1.id, ok: true, order: { ...pending1, status: 'confirmed' } }] },
      }),
    });
    renderAt(`/stores/${store}/orders`);
    const user = userEvent.setup();
    const table = await screen.findByRole('table');
    expect(await within(table).findByText('#00000001')).toBeInTheDocument();
    expect(within(table).getAllByText('349.50 ج.م')).toHaveLength(3);
    expect(within(table).getByText('تم الشحن')).toBeInTheDocument();
    // how each is paid, and where it goes by name
    expect(within(table).getByText('عند الاستلام')).toBeInTheDocument();
    expect(within(table).getByText('بانتظار الدفع')).toBeInTheDocument();
    expect(within(table).getByText('مدفوع بالبطاقة')).toBeInTheDocument();
    expect(await within(table).findAllByText('الإسكندرية')).toHaveLength(3);
    // the tabs carry the counts; "all" is chosen
    const tabs = screen.getByRole('tablist', { name: 'حالة الطلب' });
    expect(await within(tabs).findByRole('tab', { name: 'الكل 7' })).toHaveAttribute('aria-selected', 'true');
    expect(within(tabs).getByRole('tab', { name: 'قيد الانتظار 2' })).toHaveAttribute('aria-selected', 'false');
    // Only pending orders can be selected; the bar appears with them.
    expect(within(table).queryByLabelText('تحديد الطلب 00000003')).toBeNull();
    expect(screen.queryByRole('region', { name: 'إجراءات المحدد' })).toBeNull();
    await user.click(within(table).getByLabelText('تحديد الطلب 00000001'));
    const bar = screen.getByRole('region', { name: 'إجراءات المحدد' });
    expect(bar).toHaveTextContent('طلب واحد محدد');
    await user.click(within(bar).getByRole('button', { name: 'تأكيد' }));
    expect(await screen.findByText('تم تأكيد 1 طلب')).toBeInTheDocument();
    const bulk = calls.find((c) => c.method === 'POST' && c.path.endsWith('/orders/transitions'));
    expect(bulk?.body).toEqual({ order_ids: [pending1.id], status: 'confirmed', note: '' });
    // the counts are read again after the change
    await waitFor(() => {
      expect(calls.filter((c) => c.path.endsWith('/orders/counts')).length).toBeGreaterThanOrEqual(2);
    });

    // A tab and the filters go to the API and to the export link.
    await user.click(within(tabs).getByRole('tab', { name: /قيد الانتظار/ }));
    await user.type(screen.getByLabelText('رقم الموبايل'), '01012345678');
    await user.click(screen.getByRole('button', { name: 'تصفية' }));
    await waitFor(() => {
      const last = calls.filter((c) => c.method === 'GET' && c.path.endsWith('/orders')).at(-1);
      expect(last?.search.get('status')).toBe('pending');
      expect(last?.search.get('phone')).toBe('01012345678');
    });
    expect(within(tabs).getByRole('tab', { name: /قيد الانتظار/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('link', { name: 'تصدير CSV' })).toHaveAttribute('href', `/v1/stores/${store}/orders/export?status=pending`);
  });

  it('shows the problem when the list fails', async () => {
    mockAPI({ ...storeRoute, [`GET /v1/stores/${store}/orders`]: () => ({ status: 403, body: { title: 'Forbidden', status: 403, detail: 'not allowed' } }) });
    renderAt(`/stores/${store}/orders`);
    expect(await screen.findByRole('alert')).toHaveTextContent('not allowed');
  });
});

describe('OrderDetail', () => {
  it('shows the order and moves it with the version it read', async () => {
    const o = order('01M5ORDER00000000000000009', 'pending', { version: 3, notes: 'اتصلي قبل الوصول' });
    let current = o;
    const calls = mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders/${o.id}`]: () => ({
        status: 200,
        body: {
          order: current,
          lines: [{ variant_id: 'v', product_id: 'p', title: 'تيشيرت', variant_title: 'M', sku: '', qty: 2, unit_price_minor: 15000, total_minor: 30000 }],
          events: [{ id: 'e1', to: 'pending', note: '', at: '2026-10-22T10:00:00Z' }],
        },
      }),
      [`POST /v1/stores/${store}/orders/${o.id}/transitions`]: () => {
        current = { ...o, status: 'confirmed', version: 4 };
        return { status: 200, body: current };
      },
    });
    renderAt(`/stores/${store}/orders/${o.id}`);
    const user = userEvent.setup();
    expect(await screen.findByText('ملاحظة العميل: اتصلي قبل الوصول')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(`الطلب #${o.code}`);
    expect(screen.getByText('تيشيرت')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'المنتجات (2)' })).toBeInTheDocument();
    // a cash order waiting is a call to make before shipping
    expect(screen.getByRole('note')).toHaveTextContent('اتصل بالعميل لتأكيده قبل الشحن');
    expect(screen.getByRole('link', { name: 'اتصال' })).toHaveAttribute('href', 'tel:+201012345678');
    expect(screen.getByRole('link', { name: 'واتساب' })).toHaveAttribute('href', 'https://wa.me/201012345678');
    expect(screen.getByRole('link', { name: 'طلبات هذا العميل' })).toHaveAttribute('href', `/stores/${store}/orders?phone=%2B201012345678`);
    expect(screen.getByRole('button', { name: 'إلغاء' })).toHaveClass('danger');
    // cash is collected by the courier; shipping comes once the order is ready
    expect(screen.getByText('لم يُحصَّل')).toBeInTheDocument();
    expect(screen.getByText(/يحصّل المندوب/)).toBeInTheDocument();
    expect(screen.getByText('تُنشأ الشحنة بعد أن يصبح الطلب جاهزًا للشحن.')).toBeInTheDocument();
    expect(screen.getByText('وصل الطلب من المتجر')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'تأكيد الطلب' }));
    expect(await screen.findByRole('button', { name: 'جاهز للشحن' })).toBeInTheDocument();
    expect(screen.queryByRole('note')).toBeNull();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ status: 'confirmed', note: '', version: 3 });
  });

  it('shows what delivery adds to the total', async () => {
    const o = order('01M5ORDER0000000000000000C', 'pending', { subtotal_minor: 30000, shipping_minor: 6000, cod_fee_minor: 1500, total_minor: 37500 });
    mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders/${o.id}`]: () => ({
        status: 200,
        body: {
          order: o,
          lines: [{ variant_id: 'v', product_id: 'p', title: 'تيشيرت', variant_title: '', sku: '', qty: 2, unit_price_minor: 15000, total_minor: 30000 }],
          events: [],
        },
      }),
      [`GET /v1/stores/${store}/shipping`]: () => ({ status: 200, body: { areas: [{ code: 'EG-ALX', name_ar: 'الإسكندرية' }] } }),
    });
    renderAt(`/stores/${store}/orders/${o.id}`);
    // the governorate is named, not coded
    const ship = await screen.findByText('الشحن إلى الإسكندرية');
    expect(ship.nextElementSibling).toHaveTextContent('60');
    expect(screen.getByText('رسوم الدفع عند الاستلام').nextElementSibling).toHaveTextContent('15');
    expect(screen.getByText('الإجمالي').nextElementSibling).toHaveTextContent('375');
  });
});

describe('customer risk (ADR-0065)', () => {
  it('shows the band and the store\'s own history, and asks why an order is cancelled', async () => {
    const o = order('01M5ORDER0000000000000000R', 'pending', { version: 2 });
    let current = o;
    const calls = mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders/${o.id}`]: () => ({ status: 200, body: { order: current, lines: [], events: [] } }),
      [`GET /v1/stores/${store}/orders/${o.id}/risk`]: () => ({
        status: 200,
        body: { band: 'high', history: { orders: 3, delivered: 1, returned: 0, refused: 2, no_answer: 0, unconfirmed: 0, last_outcome_at: '2026-10-20T10:00:00Z' } },
      }),
      [`POST /v1/stores/${store}/orders/${o.id}/transitions`]: () => {
        current = { ...o, status: 'cancelled', version: 3, outcome_reason: 'refused' };
        return { status: 200, body: current };
      },
    });
    renderAt(`/stores/${store}/orders/${o.id}`);
    const user = userEvent.setup();
    const card = await screen.findByRole('region', { name: 'تقييم العميل' });
    expect(within(card).getByText('مخاطرة عالية')).toHaveClass('status', 'risk-high');
    expect(card).toHaveTextContent('طلباته في متجرك: 3');
    expect(card).toHaveTextContent('رفض 2');
    expect(card).not.toHaveTextContent('مرتجع');
    // cancelling asks why, and the answer travels with the move
    await user.click(screen.getByRole('button', { name: 'إلغاء' }));
    const pick = screen.getByRole('group', { name: /لماذا/ });
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
    await user.click(within(pick).getByRole('button', { name: 'العميل رفض الطلب' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ status: 'cancelled', note: '', version: 2, reason: 'refused' });
    });
    expect(screen.queryByRole('group', { name: /لماذا/ })).toBeNull();
  });

  it('cancels without a reason, or not at all', async () => {
    const o = order('01M5ORDER0000000000000000S', 'pending');
    const calls = mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders/${o.id}`]: () => ({ status: 200, body: { order: o, lines: [], events: [] } }),
      [`POST /v1/stores/${store}/orders/${o.id}/transitions`]: () => ({ status: 200, body: { ...o, status: 'cancelled' } }),
    });
    renderAt(`/stores/${store}/orders/${o.id}`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'إلغاء' }));
    await user.click(screen.getByRole('button', { name: 'تراجع' }));
    expect(screen.queryByRole('group', { name: /لماذا/ })).toBeNull();
    // no risk card when the band cannot be read
    expect(screen.queryByRole('region', { name: 'تقييم العميل' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'إلغاء' }));
    await user.click(screen.getByRole('button', { name: 'سبب آخر' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ status: 'cancelled', note: '', version: 1 });
    });
  });

  it('marks medium and high customers in the list, with one request per page', async () => {
    const a = order('01M5ORDER0000000000000000T', 'pending');
    const b = order('01M5ORDER0000000000000000U', 'pending', { name: 'سارة' });
    const c = order('01M5ORDER0000000000000000V', 'delivered', { name: 'هالة' });
    const calls = mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders`]: () => ({ status: 200, body: { items: [a, b, c] } }),
      [`POST /v1/stores/${store}/risk/bands`]: () => ({
        status: 200,
        body: { bands: [{ order_id: a.id, band: 'high' }, { order_id: b.id, band: 'low' }, { order_id: c.id, band: 'medium' }] },
      }),
    });
    renderAt(`/stores/${store}/orders`);
    const table = await screen.findByRole('table');
    expect(await within(table).findByText('مخاطرة عالية')).toBeInTheDocument();
    expect(within(table).getByText('مخاطرة متوسطة')).toBeInTheDocument();
    expect(within(table).queryByText('موثوق')).toBeNull();
    const asks = calls.filter((x) => x.path.endsWith('/risk/bands'));
    expect(asks).toHaveLength(1);
    expect(asks[0]?.body).toEqual({ order_ids: [a.id, b.id, c.id] });
  });
});
