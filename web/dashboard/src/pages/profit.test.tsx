import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { flow, lastDays, margin, returnRate, signed, type ProfitFigures } from '../lib/profit';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Profit } from './Profit';
import { ProfitSettings } from './ProfitSettings';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

function session(): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'U1', email: 'a@b.c', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/profit" element={<Profit />} />
          <Route path="/stores/:storeId/profit/settings" element={<ProfitSettings />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

function figures(over: Partial<ProfitFigures> = {}): ProfitFigures {
  return {
    orders: 412, delivered: 318, returned: 41, cancelled: 37, open: 16, cost_missing: 12,
    revenue_minor: 28620000, delivery_paid_minor: 2067000, product_cost_minor: 11840000, shipping_cost_minor: 1908000,
    cod_fees_minor: 286000, return_cost_minor: 492000, ad_spend_minor: 3850000, profit_minor: 12311000, ...over,
  };
}

const shell = {
  [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona' } }),
  [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
  [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
  [`GET ${base}/shipping`]: () => ({ status: 200, body: { areas: [{ code: 'EG-AST', name_ar: 'أسيوط' }, { code: 'EG-C', name_ar: 'القاهرة' }] } }),
};

function report(call: Call) {
  const by = call.search.get('by') ?? 'product';
  const rows = {
    product: [
      { key: 'P1', label: 'ساعة كلاسيك جلد', figures: figures({ delivered: 96, returned: 6, cost_missing: 0, profit_minor: 5294000, revenue_minor: 11520000, delivery_paid_minor: 0 }) },
      { key: 'P2', label: 'حزام جلد بني', figures: figures({ delivered: 35, returned: 12, cost_missing: 3, product_cost_minor: 0, ad_spend_minor: 0 }) },
    ],
    campaign: [{ key: '', label: '', figures: figures({ ad_spend_minor: 0 }) }, { key: 'eid-sale', label: '', figures: figures() }],
    governorate: [{ key: 'EG-AST', label: '', figures: figures({ delivered: 21, returned: 9, profit_minor: -124000 }) }],
  }[by];
  return {
    status: 200,
    body: {
      currency: 'EGP', from: call.search.get('from'), to: call.search.get('to'), by, totals: figures(),
      days: [{ day: '2026-10-15', orders: 20, revenue_minor: 900000, profit_minor: 520000 }, { day: '2026-10-16', orders: 22, revenue_minor: 1100000, profit_minor: 740000 }, { day: '2026-10-17', orders: 9, revenue_minor: 200000, profit_minor: -160000 }],
      rows,
    },
  };
}

describe('profit helpers', () => {
  it('reads figures as the mockup does', () => {
    const f = figures();
    expect(margin(f)).toBe('40%');
    expect(margin(figures({ revenue_minor: 0, delivery_paid_minor: 0 }))).toBe('—');
    expect(signed(-12000, 'EGP')).toMatch(/^−120/);
    expect(flow(f).map((r) => r.minor).reduce((a, b) => a + b, 0) - f.profit_minor).toBe(f.profit_minor);
    expect(returnRate(figures({ delivered: 21, returned: 9 }))).toBeCloseTo(0.3);
    expect(returnRate(figures({ delivered: 0, returned: 0 }))).toBe(0);
    expect(lastDays(7, new Date(2026, 10, 1))).toEqual({ from: '2026-10-26', to: '2026-11-01' });
  });
});

describe('Profit', () => {
  it('shows the totals, where profit came from, the days and the breakdowns', async () => {
    const calls = mockAPI({ ...shell, [`GET ${base}/analytics/profit`]: report });
    renderAt(`/stores/${store}/profit`);
    const user = userEvent.setup();
    const kpis = await screen.findByRole('region', { name: 'الملخص' });
    expect(await within(kpis).findByText('123,110 ج.م')).toBeInTheDocument();
    expect(within(kpis).getByText('40%')).toBeInTheDocument();
    expect(screen.getByRole('note')).toHaveTextContent('12 طلبًا مسلّمًا فيها منتجات بلا تكلفة مسجّلة.');
    const where = screen.getByRole('region', { name: 'من أين جاء الربح' });
    expect(within(where).getByText('تكلفة المنتجات').nextElementSibling?.nextElementSibling).toHaveTextContent('−118,400');
    expect(screen.getByRole('region', { name: 'الربح يومًا بيوم' })).toHaveTextContent('أعلى يوم: 16 أكتوبر');
    // the last 30 days by product first
    const first = calls.find((c) => c.path.endsWith('/analytics/profit'));
    expect(first?.search.get('by')).toBe('product');
    expect(first?.search.get('from')).toBe(lastDays(30).from);
    const table = within(screen.getByRole('region', { name: 'الربح حسب' })).getByRole('table');
    expect(within(table).getByText('حزام جلد بني')).toBeInTheDocument();
    expect(within(table).getByText('التكلفة غير مسجّلة')).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'الحملات' }));
    expect(await screen.findByText('بدون حملة')).toBeInTheDocument();
    expect(screen.getByText('eid-sale')).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'المحافظات' }));
    expect(await screen.findByText('أسيوط')).toBeInTheDocument();
    expect(screen.getByText('نسبة المرتجعات 30%')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '7 أيام' }));
    await waitFor(() => {
      expect(calls.filter((c) => c.path.endsWith('/analytics/profit')).at(-1)?.search.get('from')).toBe(lastDays(7).from);
    });
    await user.click(screen.getByRole('button', { name: 'مخصّص' }));
    expect(screen.getByLabelText('من')).toBeInTheDocument();
  });

  it('is locked on a plan without the dashboard', async () => {
    mockAPI({ ...shell, [`GET ${base}/analytics/profit`]: () => ({ status: 403, body: { title: 'Forbidden', status: 403, code: 'plan_limit', detail: 'not in plan' } }) });
    renderAt(`/stores/${store}/profit`);
    expect(await screen.findByRole('heading', { name: 'اعرف كم ربحت فعلًا' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'اعرض الباقات' })).toHaveAttribute('href', `/stores/${store}/billing/plans`);
  });

  it('says why it cannot show the figures', async () => {
    mockAPI({ ...shell, [`GET ${base}/analytics/profit`]: () => ({ status: 403, body: { title: 'Forbidden', status: 403, code: 'forbidden', detail: 'not allowed' } }) });
    renderAt(`/stores/${store}/profit`);
    expect(await screen.findByRole('alert')).toHaveTextContent('not allowed');
  });
});

describe('ProfitSettings', () => {
  it('totals every entry of the month, past the 500 listed (P8 review)', async () => {
    const items = [
      { id: 'S1', day: '2026-10-20', platform: 'meta', campaign: 'eid-sale', currency: 'EGP', amount_minor: 120000, source: 'import', note: '', created_at: '' },
    ];
    mockAPI({
      ...shell,
      [`GET ${base}/analytics/settings`]: () => ({ status: 200, body: { cod_fee_bp: 0 } }),
      [`GET ${base}/analytics/ad-spend`]: () => ({ status: 200, body: { items, count: 3, total_minor: 300000 } }),
    });
    renderAt(`/stores/${store}/profit/settings`);
    expect(await screen.findByRole('note')).toHaveTextContent('تظهر أحدث 1 سطرًا فقط، و2 غيرها محسوبة في الإجمالي.');
    expect(screen.getByText('الإجمالي هذا الشهر').nextElementSibling).toHaveTextContent('3,000');
  });

  it('saves the estimates and adds and deletes ad spend', async () => {
    let items = [
      { id: 'S1', day: '2026-10-20', platform: 'meta', campaign: 'eid-sale', currency: 'EGP', amount_minor: 120000, source: 'manual', note: '', created_at: '' },
    ];
    const calls = mockAPI({
      ...shell,
      [`GET ${base}/analytics/settings`]: () => ({ status: 200, body: { delivery_cost_minor: 4500, cod_fee_bp: 150 } }),
      [`PUT ${base}/analytics/settings`]: (c) => ({ status: 200, body: c.body }),
      [`GET ${base}/analytics/ad-spend`]: () => ({ status: 200, body: { items } }),
      [`POST ${base}/analytics/ad-spend`]: (c) => {
        const item = { ...(c.body as (typeof items)[number]), id: 'S2', currency: 'EGP', source: 'manual', created_at: '' };
        items = [item, ...items];
        return { status: 201, body: item };
      },
      [`DELETE ${base}/analytics/ad-spend/*`]: () => {
        items = items.filter((i) => i.id !== 'S1');
        return { status: 204 };
      },
    });
    renderAt(`/stores/${store}/profit/settings`);
    const user = userEvent.setup();
    const delivery = await screen.findByLabelText(/تكلفة توصيل الطرد/);
    await waitFor(() => {
      expect(delivery).toHaveValue('45');
    });
    expect(screen.getByLabelText(/رسوم شركة الشحن/)).toHaveValue('1.5');
    await user.clear(screen.getByLabelText(/تكلفة رجوع الطرد/));
    await user.type(screen.getByLabelText(/تكلفة رجوع الطرد/), '30');
    await user.click(screen.getByRole('button', { name: 'حفظ التقديرات' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت التقديرات');
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ cod_fee_bp: 150, delivery_cost_minor: 4500, return_cost_minor: 3000 });

    expect(await screen.findByText('eid-sale')).toBeInTheDocument();
    await user.type(screen.getByLabelText('الحملة'), 'tiktok-oct');
    await user.selectOptions(screen.getByLabelText('المنصة'), 'tiktok');
    await user.type(screen.getByLabelText('المبلغ'), '٤٥٠');
    await user.click(screen.getByRole('button', { name: 'إضافة' }));
    expect(await screen.findByText('tiktok-oct')).toBeInTheDocument();
    const post = calls.find((c) => c.method === 'POST')?.body as Record<string, unknown>;
    expect(post).toMatchObject({ platform: 'tiktok', campaign: 'tiktok-oct', amount_minor: 45000, note: '' });

    await user.click(screen.getByRole('button', { name: /حذف إنفاق .* eid-sale/ }));
    await waitFor(() => {
      expect(screen.queryByText('eid-sale')).toBeNull();
    });
  });
});
