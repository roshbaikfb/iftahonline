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

describe('Orders', () => {
  it('lists orders, filters them, confirms the selected ones and exports', async () => {
    const pending1 = order('01M5ORDER00000000000000001', 'pending');
    const pending2 = order('01M5ORDER00000000000000002', 'pending', { name: 'سارة' });
    const shipped = order('01M5ORDER00000000000000003', 'shipped');
    const calls = mockAPI({
      ...storeRoute,
      [`GET /v1/stores/${store}/orders`]: () => ({ status: 200, body: { items: [pending1, pending2, shipped] } }),
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
    // Only pending orders can be selected.
    expect(within(table).queryByLabelText('تحديد الطلب 00000003')).toBeNull();

    await user.click(within(table).getByLabelText('تحديد الطلب 00000001'));
    await user.click(screen.getByRole('button', { name: 'تأكيد المحدد (1)' }));
    expect(await screen.findByText('تم تأكيد 1 طلب')).toBeInTheDocument();
    const bulk = calls.find((c) => c.method === 'POST');
    expect(bulk?.body).toEqual({ order_ids: [pending1.id], status: 'confirmed', note: '' });

    // Filters go to the API and to the export link.
    await user.selectOptions(screen.getByLabelText('الحالة'), 'pending');
    await user.type(screen.getByLabelText('رقم الموبايل'), '01012345678');
    await user.click(screen.getByRole('button', { name: 'تصفية' }));
    await waitFor(() => {
      const last = calls.filter((c) => c.method === 'GET' && c.path.endsWith('/orders')).at(-1);
      expect(last?.search.get('status')).toBe('pending');
      expect(last?.search.get('phone')).toBe('01012345678');
    });
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
    expect(await screen.findByText('اتصلي قبل الوصول')).toBeInTheDocument();
    expect(screen.getByText('تيشيرت')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'إلغاء' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'تأكيد' }));
    expect(await screen.findByRole('button', { name: 'جاهز للشحن' })).toBeInTheDocument();
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
    });
    renderAt(`/stores/${store}/orders/${o.id}`);
    expect(await screen.findByText('الشحن')).toBeInTheDocument();
    expect(screen.getByText('رسوم الدفع عند الاستلام')).toBeInTheDocument();
    expect(screen.getByText('الإجمالي').closest('tr')).toHaveTextContent('375');
  });
});
