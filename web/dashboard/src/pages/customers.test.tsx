import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { initials, summarize, telLink, waLink, type Customer } from '../lib/customers';
import type { Order } from '../lib/orders';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { CustomerDetail } from './CustomerDetail';
import { Customers } from './Customers';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const mona = '01M5CUST000000000000000001';

const session: Session = {
  state: { kind: 'signed-in', me: { user: { id: 'U1', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
  reload: vi.fn(() => Promise.resolve()),
  signOut: vi.fn(() => Promise.resolve()),
};

function customer(over: Partial<Customer> = {}): Customer {
  return {
    id: mona,
    phone: '+201012345678',
    name: 'منى أحمد',
    orders_count: 7,
    last_order_at: '2026-11-02T10:00:00Z',
    blocked: false,
    block_reason: '',
    created_at: '2026-09-03T10:00:00Z',
    updated_at: '2026-11-02T10:00:00Z',
    ...over,
  };
}

function order(id: string, status: Order['status'], total: number): Order {
  return {
    id, code: id.slice(-8), status, version: 1, source: 'storefront', customer_id: mona,
    name: 'منى أحمد', phone: '+201012345678', governorate: 'EG-C', city: 'مدينة نصر', address: 'شارع عباس العقاد', notes: '',
    currency: 'EGP', subtotal_minor: total, shipping_minor: 0, cod_fee_minor: 0, payment_method: 'cod', payment_status: 'unpaid',
    total_minor: total, item_count: 1,
    created_at: '2026-10-22T10:00:00Z', updated_at: '2026-10-22T10:00:00Z', status_changed_at: '2026-10-22T10:00:00Z',
  };
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
    ...over,
  });
}

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/customers" element={<Customers />} />
          <Route path="/stores/:storeId/customers/:customerId" element={<CustomerDetail />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<p>صفحة الطلب</p>} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('customers helpers', () => {
  it.each([
    ['منى أحمد', 'مأ'],
    ['  كريم   حسن  علي ', 'كح'],
    ['سارة', 'س'],
    ['', ''],
  ])('initials(%j) = %j', (name, want) => {
    expect(initials(name)).toBe(want);
  });

  it('links a number to the phone and to WhatsApp', () => {
    expect(telLink('+201012345678')).toBe('tel:+201012345678');
    expect(waLink('+201012345678')).toBe('https://wa.me/201012345678');
  });

  it('sums the delivered orders and counts the refused or returned', () => {
    const s = summarize([order('A', 'delivered', 89000), order('B', 'returned', 45000), order('C', 'failed_delivery', 1000), order('D', 'delivered', 61000), order('E', 'pending', 51500)]);
    expect(s).toEqual({ delivered: 2, failed: 2, deliveredMinor: 150000 });
  });
});

describe('Customers', () => {
  it('lists the customers with their number, orders, last order, block and a way to reach them', async () => {
    const calls = routes({
      [`GET ${base}/customers`]: () => ({
        status: 200,
        body: { items: [customer(), customer({ id: 'C2', name: 'يوسف مراد', phone: '+201098765432', orders_count: 2, blocked: true })] },
      }),
    });
    renderAt(`/stores/${store}/customers`);

    const row = (await screen.findByRole('link', { name: 'منى أحمد' })).closest('tr');
    if (!row) throw new Error('no row');
    expect(within(row).getByRole('link', { name: 'منى أحمد' })).toHaveAttribute('href', `/stores/${store}/customers/${mona}`);
    expect(row).toHaveTextContent('+201012345678');
    expect(row).toHaveTextContent('7');
    expect(within(row).getByRole('link', { name: 'اتصال بـ منى أحمد' })).toHaveAttribute('href', 'tel:+201012345678');
    expect(within(row).getByRole('link', { name: 'واتساب منى أحمد' })).toHaveAttribute('href', 'https://wa.me/201012345678');
    expect(within(row).queryByText('محظور')).toBeNull();
    const blocked = screen.getByRole('link', { name: /^يوسف مراد/ }).closest('tr');
    expect(blocked).toHaveTextContent('محظور');

    const list = calls.find((c) => c.method === 'GET' && c.path === `${base}/customers`);
    expect(list?.search.get('blocked')).toBe('false');
    expect(list?.search.get('limit')).toBe('50');
    expect(list?.search.has('q')).toBe(false);
    expect(screen.getByRole('tab', { name: 'كل العملاء' })).toHaveAttribute('aria-selected', 'true');
  });

  it('searches by name or number, and says when nobody matches', async () => {
    const calls = routes({
      [`GET ${base}/customers`]: (c) => ({ status: 200, body: { items: c.search.get('q') ? [] : [customer()] } }),
    });
    renderAt(`/stores/${store}/customers`);
    await screen.findByRole('link', { name: 'منى أحمد' });

    await userEvent.type(screen.getByRole('searchbox', { name: 'ابحث بالاسم أو رقم الهاتف' }), '0109');
    await userEvent.click(screen.getByRole('button', { name: 'بحث' }));

    expect(await screen.findByText('لا يوجد عميل بهذا الاسم أو الرقم.')).toBeInTheDocument();
    expect(calls.filter((c) => c.path === `${base}/customers`).at(-1)?.search.get('q')).toBe('0109');
  });

  it('says so when nobody has ordered yet', async () => {
    routes({ [`GET ${base}/customers`]: () => ({ status: 200, body: { items: [] } }) });
    renderAt(`/stores/${store}/customers`);
    expect(await screen.findByText('لم يطلب أحد من متجرك بعد.')).toBeInTheDocument();
  });

  it('follows the cursor for more', async () => {
    const calls = routes({
      [`GET ${base}/customers`]: (c) =>
        c.search.get('cursor') === 'next1'
          ? { status: 200, body: { items: [customer({ id: 'C2', name: 'كريم حسن' })] } }
          : { status: 200, body: { items: [customer()], next_cursor: 'next1' } },
    });
    renderAt(`/stores/${store}/customers`);
    await userEvent.click(await screen.findByRole('button', { name: 'عرض المزيد' }));

    expect(await screen.findByRole('link', { name: 'كريم حسن' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'منى أحمد' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'عرض المزيد' })).toBeNull();
    expect(calls.filter((c) => c.path === `${base}/customers`)).toHaveLength(2);
  });

  it('shows the blocked in their tab with the reason, and unblocks one', async () => {
    const calls = routes({
      [`GET ${base}/customers`]: (c) =>
        c.search.get('blocked') === 'true'
          ? { status: 200, body: { items: [customer({ blocked: true, blocked_at: '2026-10-30T10:00:00Z', block_reason: 'رفض الاستلام مرتين' })] } }
          : { status: 200, body: { items: [customer()] } },
      [`POST ${base}/customers/${mona}/unblock`]: () => ({ status: 200, body: customer() }),
    });
    renderAt(`/stores/${store}/customers`);
    await screen.findByRole('link', { name: 'منى أحمد' });

    await userEvent.click(screen.getByRole('tab', { name: 'المحظورون' }));
    const row = (await screen.findByText('رفض الاستلام مرتين')).closest('tr');
    if (!row) throw new Error('no row');
    expect(screen.getByRole('tab', { name: 'المحظورون' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByRole('searchbox')).toBeNull();

    await userEvent.click(within(row).getByRole('button', { name: 'إلغاء الحظر' }));
    expect(await screen.findByRole('status')).toHaveTextContent('أُلغي حظر منى أحمد.');
    expect(screen.getByText('لا يوجد عملاء محظورون.')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST' && c.path === `${base}/customers/${mona}/unblock`)).toBe(true);
  });

  it('blocks a number that never ordered, and keeps the dialog open on a bad number', async () => {
    let tries = 0;
    const calls = routes({
      [`GET ${base}/customers`]: () => ({ status: 200, body: { items: [] } }),
      [`POST ${base}/customers/blocks`]: () =>
        ++tries === 1
          ? { status: 422, body: { title: 'Unprocessable', status: 422, detail: 'رقم هاتف غير صحيح' } }
          : { status: 200, body: customer({ id: 'C9', name: '', phone: '+201098765432', orders_count: 0, blocked: true, block_reason: 'رسائل مزعجة' }) },
    });
    renderAt(`/stores/${store}/customers`);
    await screen.findByText('لم يطلب أحد من متجرك بعد.');

    await userEvent.click(screen.getByRole('button', { name: 'حظر رقم' }));
    const dialog = screen.getByRole('dialog', { name: 'حظر رقم' });
    await userEvent.type(within(dialog).getByLabelText(/رقم الهاتف/), '123');
    await userEvent.type(within(dialog).getByLabelText('السبب (لك وحدك)'), 'رسائل مزعجة');
    await userEvent.click(within(dialog).getByRole('button', { name: 'حظر الرقم' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('رقم هاتف غير صحيح');

    await userEvent.clear(within(dialog).getByLabelText(/رقم الهاتف/));
    await userEvent.type(within(dialog).getByLabelText(/رقم الهاتف/), '01098765432');
    await userEvent.click(within(dialog).getByRole('button', { name: 'حظر الرقم' }));
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
    expect(screen.getByRole('status')).toHaveTextContent('حُظر الرقم +201098765432.');
    expect(calls.filter((c) => c.method === 'POST').at(-1)?.body).toEqual({ phone: '01098765432', reason: 'رسائل مزعجة' });
  });

  it('closes the block dialog on Escape without blocking', async () => {
    const calls = routes({ [`GET ${base}/customers`]: () => ({ status: 200, body: { items: [] } }) });
    renderAt(`/stores/${store}/customers`);
    await screen.findByText('لم يطلب أحد من متجرك بعد.');
    await userEvent.click(screen.getByRole('button', { name: 'حظر رقم' }));
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it('says why when the role may not see customers', async () => {
    routes({ [`GET ${base}/customers`]: () => ({ status: 403, body: { title: 'Forbidden', status: 403, detail: 'ليست لديك صلاحية' } }) });
    renderAt(`/stores/${store}/customers`);
    expect(await screen.findByRole('alert')).toHaveTextContent('ليست لديك صلاحية');
  });
});

describe('CustomerDetail', () => {
  const orders = [order('01M6ORDER00000000000000005', 'pending', 51500), order('01M6ORDER00000000000000004', 'delivered', 89000), order('01M6ORDER00000000000000003', 'returned', 45000), order('01M6ORDER00000000000000002', 'delivered', 61000)];

  it('shows the customer, their totals, their rating and their orders', async () => {
    const calls = routes({
      [`GET ${base}/customers/${mona}`]: () => ({ status: 200, body: customer({ orders_count: 4 }) }),
      [`GET ${base}/orders`]: () => ({ status: 200, body: { items: orders } }),
      [`GET ${base}/orders/${orders[0]?.id ?? ''}/risk`]: () => ({ status: 200, body: { band: 'low', history: { orders: 4, delivered: 2, returned: 1, refused: 0, no_answer: 0, unconfirmed: 0 } } }),
    });
    renderAt(`/stores/${store}/customers/${mona}`);

    expect(await screen.findByRole('heading', { level: 1, name: 'منى أحمد' })).toBeInTheDocument();
    expect(within(screen.getByRole('navigation', { name: 'مسار الصفحة' })).getByRole('link', { name: 'العملاء' })).toHaveAttribute('href', `/stores/${store}/customers`);
    expect(screen.getByRole('link', { name: /اتصال/ })).toHaveAttribute('href', 'tel:+201012345678');
    expect(screen.getByRole('link', { name: /واتساب/ })).toHaveAttribute('href', 'https://wa.me/201012345678');

    const tiles = screen.getByRole('region', { name: 'ملخص' });
    await waitFor(() => {
      expect(tiles).toHaveTextContent(/سُلّمت\s*2/);
    });
    expect(tiles).toHaveTextContent(/الطلبات\s*4/);
    expect(tiles).toHaveTextContent(/رُفضت أو رجعت\s*1/);
    expect(tiles).toHaveTextContent(/1,500/);
    expect(screen.queryByText(/الملخص من آخر/)).toBeNull();

    expect(await screen.findByRole('heading', { name: 'تقييم العميل' })).toBeInTheDocument();
    const table = screen.getByRole('region', { name: 'طلباته' });
    expect(within(table).getAllByRole('row')).toHaveLength(5);
    expect(within(table).getByRole('link', { name: '#00000005' })).toHaveAttribute('href', `/stores/${store}/orders/01M6ORDER00000000000000005`);

    const list = calls.find((c) => c.path === `${base}/orders`);
    expect(list?.search.get('customer_id')).toBe(mona);
    expect(list?.search.get('limit')).toBe('100');
  });

  it('labels the totals as the latest 100 when the customer has more', async () => {
    const many = Array.from({ length: 100 }, (_, i) => order(`01M6ORDER${String(i).padStart(17, '0')}`, 'delivered', 1000));
    routes({
      [`GET ${base}/customers/${mona}`]: () => ({ status: 200, body: customer({ orders_count: 130 }) }),
      [`GET ${base}/orders`]: () => ({ status: 200, body: { items: many } }),
    });
    renderAt(`/stores/${store}/customers/${mona}`);
    expect(await screen.findByText('الملخص من آخر 100 طلب.')).toBeInTheDocument();
  });

  it('blocks the customer with a reason, then unblocks', async () => {
    const calls = routes({
      [`GET ${base}/customers/${mona}`]: () => ({ status: 200, body: customer() }),
      [`GET ${base}/orders`]: () => ({ status: 200, body: { items: [] } }),
      [`POST ${base}/customers/${mona}/block`]: () => ({ status: 200, body: customer({ blocked: true, blocked_at: '2026-11-03T10:00:00Z', block_reason: 'طلبات وهمية' }) }),
      [`POST ${base}/customers/${mona}/unblock`]: () => ({ status: 200, body: customer() }),
    });
    renderAt(`/stores/${store}/customers/${mona}`);

    await userEvent.click(await screen.findByRole('button', { name: /حظر العميل/ }));
    const dialog = screen.getByRole('dialog', { name: 'حظر العميل' });
    expect(within(dialog).queryByLabelText(/رقم الهاتف/)).toBeNull();
    await userEvent.type(within(dialog).getByLabelText('السبب (لك وحدك)'), 'طلبات وهمية');
    await userEvent.click(within(dialog).getByRole('button', { name: 'حظر' }));

    expect(await screen.findByRole('note')).toHaveTextContent('طلبات وهمية');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ reason: 'طلبات وهمية' });

    await userEvent.click(screen.getByRole('button', { name: 'إلغاء الحظر' }));
    await waitFor(() => {
      expect(screen.queryByRole('note')).toBeNull();
    });
    expect(screen.getByRole('status')).toHaveTextContent('أُلغي الحظر.');
    expect(screen.getByRole('button', { name: /حظر العميل/ })).toBeInTheDocument();
  });

  it("shows not found for another store's customer", async () => {
    routes({
      [`GET ${base}/customers/${mona}`]: () => ({ status: 404, body: { title: 'Not Found', status: 404, detail: 'customer not found' } }),
      [`GET ${base}/orders`]: () => ({ status: 200, body: { items: [] } }),
    });
    renderAt(`/stores/${store}/customers/${mona}`);
    expect(await screen.findByRole('alert')).toHaveTextContent('customer not found');
    expect(screen.queryByRole('region', { name: 'ملخص' })).toBeNull();
  });
});
