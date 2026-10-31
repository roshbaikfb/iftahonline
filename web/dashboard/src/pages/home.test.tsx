import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ago, change, count } from '../lib/home';
import { SessionContext, type Session } from '../session';
import { mockAPI } from '../test/api';
import { Overview } from './Overview';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

function session(): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: 'u', email: 'mona@example.com', name: 'منى أحمد', email_verified: true, mfa_enabled: false, created_at: '' },
        memberships: [{ store_id: store, role: 'owner' }],
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function daily(lastSales: number) {
  return Array.from({ length: 30 }, (_, i) => ({ date: `2026-10-${String(i + 1).padStart(2, '0')}`, sales_minor: i === 29 ? lastSales : 10000, orders: 1 }));
}

function stats(range: string, sales: number, previous: number) {
  return {
    range,
    currency: 'EGP',
    from: '2026-10-27T00:00:00Z',
    to: '2026-10-27T12:00:00Z',
    sales: { orders: 14, total_minor: sales },
    cod_orders: 9,
    card_orders: 5,
    previous: { orders: 10, total_minor: previous },
    cancelled: { orders: 2, total_minor: 90000 },
    returned: { orders: 1, total_minor: 45000 },
    daily: daily(sales),
    oldest_pending_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString(),
  };
}

function routes(over: Record<string, (c: { search: URLSearchParams }) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona' } }),
    [`GET ${base}/orders/stats`]: (c) =>
      c.search.get('range') === '7d' ? { status: 200, body: stats('7d', 2000000, 0) } : { status: 200, body: stats('today', 342000, 289800) },
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 7 } }),
    [`GET ${base}/shipping/cod`]: () => ({
      status: 200,
      body: { carriers: [{ currency: 'EGP', held_minor: 1280000, held_15_plus_minor: 210000 }], unmatched_lines: 0, unmatched_minor: 0 },
    }),
    [`GET ${base}/products`]: () => ({ status: 200, body: { items: [{ id: 'p', sample: true }] } }),
    [`GET ${base}/shipping/carriers`]: () => ({ status: 200, body: { carriers: [], connections: [{ status: 'active' }] } }),
    [`GET ${base}/shipping`]: () => ({ status: 200, body: { areas: [{ code: 'EG-C', name_ar: 'القاهرة' }], zones: [] } }),
    [`GET ${base}/payments/gateways`]: () => ({ status: 200, body: { gateways: [], connections: [] } }),
    [`GET ${base}/domains`]: () => ({ status: 200, body: { items: [] } }),
    [`GET ${base}/shipping/shipments`]: () => ({ status: 200, body: { items: [{ id: 's', failure: 'عنوان غير مكتمل' }] } }),
    [`GET ${base}/storefront/theme`]: () => ({ status: 200, body: { settings: { low_stock: 5 }, version: 1 } }),
    [`GET ${base}/inventory/low`]: () => ({ status: 200, body: { total: 3, items: [{ product_title: 'تيشيرت قطن', option_values: ['L'] }] } }),
    [`GET ${base}/orders`]: () => ({
      status: 200,
      body: {
        items: [
          {
            id: 'o1', code: 'A7B2C9D1', status: 'pending', name: 'أحمد حسن', phone: '+201022222222', city: 'مدينة نصر', governorate: 'EG-C',
            currency: 'EGP', total_minor: 89000, payment_method: 'cod', payment_status: 'unpaid', created_at: new Date(Date.now() - 22 * 60000).toISOString(),
          },
        ],
      },
    }),
    ...over,
  });
}

function renderHome() {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[`/stores/${store}/home`]}>
        <Routes>
          <Route path="/stores/:storeId/home" element={<Overview />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

describe('Overview', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows the period, what is left to set up, what needs attention and the latest orders', async () => {
    // Midday: "3 hours ago" stays today whenever the test runs.
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-10-27T12:00:00'));
    const calls = routes();
    renderHome();
    const user = userEvent.setup();
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/الخير يا منى$/);

    const salesCard = await screen.findByRole('link', { name: /^مبيعات اليوم/ });
    await waitFor(() => {
      expect(salesCard).toHaveTextContent('3,420 ج.م+18% عن أمس');
    });
    expect(screen.getByText('9 عند الاستلام · 5 بالبطاقة')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /بانتظار التأكيد\s*7/ })).toHaveAttribute('href', `/stores/${store}/orders?status=pending`);
    expect(await screen.findByText('12,800 ج.م')).toBeInTheDocument();
    expect(screen.getByText('2,100 ج.م منذ أكثر من 15 يومًا')).toBeInTheDocument();
    // cancelled and returned are apart from sales (the owner, 2026-10-27)
    expect(screen.getByRole('link', { name: 'ملغاة: طلبان · 900 ج.م' })).toHaveAttribute('href', `/stores/${store}/orders?status=cancelled`);
    expect(screen.getByRole('link', { name: 'مرتجعة: طلب واحد · 450 ج.م' })).toBeInTheDocument();

    // setup: a carrier only; samples are not "a first product"; screens to come say so
    const setup = await screen.findByRole('region', { name: 'أكمل إعداد متجرك' });
    expect(within(setup).getByText('1 من 5 خطوات')).toBeInTheDocument();
    expect(within(setup).getByRole('link', { name: 'حدّد مناطق الشحن' })).toHaveAttribute('href', `/stores/${store}/shipping`);
    expect(within(setup).queryByRole('link', { name: /اربط شركة شحن/ })).toBeNull();
    expect(within(setup).getByRole('link', { name: 'أضف أول منتج' })).toHaveAttribute('href', `/stores/${store}/products`);
    expect(within(setup).getByText('اربط الدومين الخاص').closest('.step')).toHaveTextContent('قريبًا');

    const attention = screen.getByRole('region', { name: 'يحتاج انتباهك' });
    expect(within(attention).getByText('7 طلبات تنتظر التأكيد')).toBeInTheDocument();
    expect(within(attention).getByText('أقدمها منذ 3 ساعات')).toBeInTheDocument();
    expect(await within(attention).findByText('شحنة رفضتها شركة الشحن')).toBeInTheDocument();
    expect(within(attention).getByText('عنوان غير مكتمل')).toBeInTheDocument();
    expect(await within(attention).findByText('3 منتجات قاربت على النفاد')).toBeInTheDocument();
    expect(within(attention).getByText('تيشيرت قطن L وغيره')).toBeInTheDocument();

    const recent = screen.getByRole('region', { name: 'آخر الطلبات' });
    expect(await within(recent).findByRole('link', { name: '#A7B2C9D1' })).toHaveAttribute('href', `/stores/${store}/orders/o1`);
    expect(within(recent).getByText('منذ 22 دقيقة')).toBeInTheDocument();
    expect(within(recent).getByText('مدينة نصر، القاهرة')).toBeInTheDocument();
    expect(screen.getByRole('table', { name: 'المبيعات يومًا بيوم لآخر 30 يوم' })).toBeInTheDocument();

    // another period: the figures are asked again; nothing to compare with
    await user.click(screen.getByRole('button', { name: '7 أيام' }));
    const weekCard = await screen.findByRole('link', { name: /^مبيعات 7 أيام/ });
    await waitFor(() => {
      expect(weekCard).toHaveTextContent('20,000 ج.م');
    });
    expect(screen.queryByText(/عن الأيام السبعة السابقة/)).toBeNull();
    expect(calls.filter((c) => c.path.endsWith('/orders/stats')).map((c) => c.search.get('range'))).toEqual(['today', '7d']);
  });

  it('hides a finished setup, asks nothing about stock when the merchant turned it off, and says when all is calm', async () => {
    const calls = routes({
      [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
      [`GET ${base}/products`]: () => ({ status: 200, body: { items: [{ id: 'p' }] } }),
      [`GET ${base}/shipping`]: () => ({ status: 200, body: { areas: [], zones: [{ id: 'z' }] } }),
      [`GET ${base}/payments/gateways`]: () => ({ status: 200, body: { gateways: [], connections: [{ status: 'active' }] } }),
      [`GET ${base}/domains`]: () => ({ status: 200, body: { items: [{ kind: 'custom', status: 'verified' }] } }),
      [`GET ${base}/shipping/shipments`]: () => ({ status: 200, body: { items: [] } }),
      [`GET ${base}/storefront/theme`]: () => ({ status: 200, body: { settings: { low_stock: 0 }, version: 1 } }),
      [`GET ${base}/shipping/cod`]: () => ({ status: 403, body: { title: 'Forbidden', status: 403 } }),
    });
    renderHome();
    expect(await screen.findByText('لا شيء يحتاج انتباهك الآن.')).toBeInTheDocument();
    await waitFor(() => {
      expect(calls.some((c) => c.path.endsWith('/domains'))).toBe(true);
    });
    expect(screen.queryByRole('region', { name: 'أكمل إعداد متجرك' })).toBeNull();
    expect(calls.some((c) => c.path.endsWith('/inventory/low'))).toBe(false);
    // without finance.read the held amount is left out, not an error
    expect(screen.queryByText('لدى شركات الشحن')).toBeNull();
  });

  it('tells a free store that it shows ads and links to the plans; a paid store sees no notice', async () => {
    routes({ [`GET ${base}/usage`]: () => ({ status: 200, body: { items: [], ad_free: false } }) });
    const { unmount } = renderHome();
    expect(await screen.findByText('متجرك على الباقة المجانية، فتظهر فيه إعلانات')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'أزل الإعلانات' })).toHaveAttribute('href', `/stores/${store}/billing/plans`);
    unmount();
    vi.restoreAllMocks();

    const calls = routes({ [`GET ${base}/usage`]: () => ({ status: 200, body: { items: [], ad_free: true } }) });
    renderHome();
    await waitFor(() => {
      expect(calls.some((c) => c.path.endsWith('/usage'))).toBe(true);
    });
    expect(screen.queryByText(/فتظهر فيه إعلانات/)).toBeNull();
  });
});

describe('home helpers', () => {
  it('counts, dates and compares the Arabic way', () => {
    expect(count(1, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')).toBe('طلب واحد');
    expect(count(2, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')).toBe('طلبان');
    expect(count(7, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')).toBe('7 طلبات');
    expect(count(14, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')).toBe('14 طلبًا');
    const now = new Date('2026-10-27T15:00:00');
    expect(ago('2026-10-27T14:59:40', now)).toBe('الآن');
    expect(ago('2026-10-27T14:55:00', now)).toBe('منذ 5 دقائق');
    expect(ago('2026-10-27T13:00:00', now)).toBe('منذ ساعتين');
    expect(ago('2026-10-26T20:00:00', now)).toBe('أمس');
    expect(ago('2026-10-20T20:00:00', now)).toBe('20 أكتوبر');
    expect(change(342000, 289800)).toBe('+18%');
    expect(change(80, 100)).toBe('−20%');
    expect(change(100, 0)).toBeNull();
  });
});
