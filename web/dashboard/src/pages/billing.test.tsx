import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { annualSaving, grantText, leave, meterLevel, meterPercent, meterText, planPoints, planPrice, sold, type BillingPlan, type BillingPlans } from '../lib/billing';
import type { Role } from '../lib/settings';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Billing } from './Billing';
import { Plans } from './Plans';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

function session(role: Role): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'U1', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const features: BillingPlans['features'] = [
  { key: 'orders_per_month', kind: 'monthly', name_ar: 'الطلبات في الشهر', name_en: 'Orders a month' },
  { key: 'products', kind: 'count', name_ar: 'المنتجات', name_en: 'Products' },
  { key: 'coupons', kind: 'flag', name_ar: 'الكوبونات', name_en: 'Coupons' },
];

function plan(over: Partial<BillingPlan>): BillingPlan {
  return { code: 'pro', kind: 'subscription', name_ar: 'برو', name_en: 'Pro', trial_days: 14, current: false, trial_available: false, grants: [], ...over };
}

const plans: BillingPlans = {
  currency: 'EGP',
  features,
  plans: [
    plan({ code: 'free', kind: 'free', name_ar: 'المجانية', name_en: 'Free', trial_days: 0, grants: [{ feature: 'orders_per_month', unlimited: false, value: 30 }] }),
    plan({ code: 'pays', kind: 'pays', name_ar: 'حسب مبيعاتك', name_en: 'Pay as you sell', trial_days: 0, fee: { rate_bps: 250, min_minor: 0 } }),
    plan({
      code: 'pro',
      current: true,
      monthly_minor: 59900,
      annual_minor: 599000,
      grants: [
        { feature: 'orders_per_month', unlimited: false, value: 1500 },
        { feature: 'products', unlimited: true, value: 0 },
        { feature: 'coupons', unlimited: false, value: 1 },
      ],
    }),
    plan({ code: 'brand', name_ar: 'براند', name_en: 'Brand', monthly_minor: 149900, annual_minor: 1499000, trial_available: true }),
  ],
};

const trialBilling = {
  plan_name_ar: 'برو',
  plan_name_en: 'Pro',
  status: 'trialing',
  trial_ends_at: new Date(Date.now() + 9 * 86_400_000).toISOString(),
  open_invoices: [],
};
const activeBilling = {
  plan_name_ar: 'برو',
  plan_name_en: 'Pro',
  status: 'active',
  cycle: 'monthly',
  period_end: '2026-12-01T00:00:00Z',
  card: 'MasterCard •••• 2346',
  open_invoices: [
    { id: '01M7INVOICE000000000000001', number: 'INV-2026-000184', period: '2026-10-01', currency: 'EGP', total_minor: 68286, due_at: '2026-11-08T00:00:00Z', attempts: 1 },
  ],
};
const meters = {
  items: [
    { feature: 'orders_per_month', kind: 'monthly', used: 1140, limit: 1500, unlimited: false },
    { feature: 'messages', kind: 'monthly', used: 1710, limit: 2000, unlimited: false },
    { feature: 'products', kind: 'count', used: 412, unlimited: true },
  ],
};

function routes(billing: unknown, over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
    [`GET ${base}/billing`]: () => ({ status: 200, body: billing }),
    [`GET ${base}/billing/plans`]: () => ({ status: 200, body: plans }),
    [`GET ${base}/usage`]: () => ({ status: 200, body: meters }),
    ...over,
  });
}

function renderAt(path: string, role: Role = 'owner') {
  return render(
    <SessionContext.Provider value={session(role)}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/billing" element={<Billing />} />
          <Route path="/stores/:storeId/billing/plans" element={<Plans />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('billing helpers', () => {
  it('warns at 80% and at the limit, and shows unlimited', () => {
    expect(meterLevel({ feature: 'messages', kind: 'monthly', used: 1710, limit: 2000, unlimited: false })).toBe('nearly');
    expect(meterLevel({ feature: 'messages', kind: 'monthly', used: 2000, limit: 2000, unlimited: false })).toBe('reached');
    expect(meterLevel({ feature: 'messages', kind: 'monthly', used: 0, limit: 0, unlimited: false })).toBe('reached');
    expect(meterLevel({ feature: 'orders_per_month', kind: 'monthly', used: 10, limit: 30, unlimited: false })).toBe('ok');
    expect(meterLevel({ feature: 'products', kind: 'count', used: 9000, unlimited: true })).toBe('ok');
    expect(meterText({ feature: 'products', kind: 'count', used: 9000, unlimited: true })).toBe('9,000 / بلا حد');
    expect(meterPercent({ feature: 'staff', kind: 'count', used: 5, limit: 2, unlimited: false })).toBe(100);
    expect(meterPercent({ feature: 'messages', kind: 'monthly', used: 0, limit: 0, unlimited: false })).toBe(100);
  });

  it('prices by cycle, shows the fee and the free plan, and the grants', () => {
    const [free, pays, pro] = plans.plans as [BillingPlan, BillingPlan, BillingPlan];
    const [, products, coupons] = features as [BillingPlans['features'][number], BillingPlans['features'][number], BillingPlans['features'][number]];
    expect(planPrice(pro, 'monthly', 'EGP')).toEqual({ price: '599 ج.م', note: 'شهريًا' });
    expect(planPrice(pro, 'annual', 'EGP').price).toBe('5,990 ج.م');
    expect(planPrice(pays, 'monthly', 'EGP')).toEqual({ price: '2.5%', note: 'من الطلبات المسلّمة فقط' });
    expect(planPrice(free, 'annual', 'EGP').price).toBe('0 ج.م');
    expect(planPrice(plan({ monthly_minor: 100 }), 'annual', 'EGP')).toEqual({ price: '—', note: 'غير متاحة سنويًا' });
    expect(sold(plan({ monthly_minor: 100 }), 'annual')).toBe(false);
    expect(sold(pays, 'annual')).toBe(true);
    expect(annualSaving(pro)).toBe(2);
    expect(annualSaving(free)).toBe(0);
    expect(grantText(pro, products)).toBe('بلا حد');
    expect(grantText(pro, coupons)).toBe('✓');
    expect(grantText(free, coupons)).toBe('—');
    expect(planPoints(pro, features)).toEqual(['الطلبات في الشهر: 1,500', 'المنتجات: بلا حد', 'الكوبونات']);
  });
});

describe('Billing', () => {
  it('shows a trial, the meters and the way to choose', async () => {
    routes(trialBilling);
    renderAt(`/stores/${store}/billing`);
    expect(await screen.findByText(/أنت تجرّب باقة «برو» مجانًا — باقي 9 أيام/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'اختر باقتك' })).toHaveAttribute('href', `/stores/${store}/billing/plans`);
    const messages = await screen.findByRole('meter', { name: 'رسائل واتساب وSMS' });
    expect(messages).toHaveClass('nearly');
    expect(screen.getByText('9,000 / بلا حد'.replace('9,000', '412'))).toBeInTheDocument();
    expect(screen.getByText('لا توجد فواتير مستحقة.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'إلغاء التجديد' })).not.toBeInTheDocument();
  });

  it('pays an open invoice on Paymob, switches to annual, cancels and takes it back', async () => {
    const goTo = vi.spyOn(leave, 'goTo').mockImplementation(() => undefined);
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    let current: Record<string, unknown> = { ...activeBilling };
    const calls = routes(activeBilling, {
      [`GET ${base}/billing`]: () => ({ status: 200, body: current }),
      [`POST ${base}/billing/invoices/01M7INVOICE000000000000001/pay`]: () => ({ status: 200, body: { checkout_url: 'https://accept.paymob.com/pay/x' } }),
      [`POST ${base}/billing/subscription`]: () => {
        current = { ...current, next_cycle: 'annual' };
        return { status: 200, body: { action: 'schedule' } };
      },
      [`POST ${base}/billing/cancel`]: () => ({ status: 200, body: { ...activeBilling, cancel_at_period_end: true } }),
      [`POST ${base}/billing/resume`]: () => ({ status: 200, body: activeBilling }),
    });
    renderAt(`/stores/${store}/billing`);
    const user = userEvent.setup();

    const row = (await screen.findByText('INV-2026-000184')).closest('tr') as HTMLElement;
    expect(within(row).getByText('682.86 ج.م')).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: 'ادفع الآن' }));
    expect(goTo).toHaveBeenCalledWith('https://accept.paymob.com/pay/x');

    await user.click(screen.getByRole('button', { name: 'التحويل للسنوي — وفّر شهرين' }));
    expect(calls.find((c) => c.method === 'POST' && c.path.endsWith('/subscription'))?.body).toEqual({ plan_code: 'pro', cycle: 'annual' });
    expect(await screen.findByText(/دورة سنوية/)).toBeInTheDocument();

    current = { ...activeBilling };
    await user.click(screen.getByRole('button', { name: 'إلغاء التجديد' }));
    expect(await screen.findByText(/ينتهي اشتراكك في 1 ديسمبر 2026، ثم ينتقل متجرك لباقة «المجانية»/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'تراجع' }));
    await waitFor(() => expect(screen.queryByText(/ينتهي اشتراكك/)).not.toBeInTheDocument());
    expect(calls.some((c) => c.path.endsWith('/billing/resume'))).toBe(true);
  });

  it('shows a past-due store how to pay', async () => {
    routes({ ...activeBilling, status: 'past_due', grace_until: '2026-11-15T00:00:00Z' });
    renderAt(`/stores/${store}/billing`);
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('لم ننجح في تحصيل فاتورة أكتوبر 2026');
    expect(within(alert).getByRole('button', { name: 'ادفع الآن 682.86 ج.م' })).toBeInTheDocument();
  });

  it('is the owner’s alone', () => {
    routes(activeBilling);
    renderAt(`/stores/${store}/billing`, 'manager');
    expect(screen.getByText('الفوترة لمالك المتجر فقط.')).toBeInTheDocument();
  });
});

describe('Plans', () => {
  it('switches the cycle, starts a trial, and sends a paid choice to Paymob', async () => {
    const goTo = vi.spyOn(leave, 'goTo').mockImplementation(() => undefined);
    const calls = routes(trialBilling, {
      [`POST ${base}/billing/trial`]: () => ({ status: 200, body: trialBilling }),
      [`POST ${base}/billing/subscription`]: (c) =>
        (c.body as { plan_code: string }).plan_code === 'pays'
          ? { status: 200, body: { action: 'apply' } }
          : { status: 200, body: { action: 'pay', checkout_url: 'https://accept.paymob.com/pay/y' } },
    });
    renderAt(`/stores/${store}/billing/plans`);
    const user = userEvent.setup();

    const pro = await screen.findByRole('region', { name: 'برو' });
    expect(within(pro).getByText('باقتك الحالية')).toBeInTheDocument();
    expect(within(pro).getByText('599 ج.م')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'سنوي — شهران مجانًا' }));
    expect(within(pro).getByText('5,990 ج.م')).toBeInTheDocument();

    await user.click(within(pro).getByRole('button', { name: 'الاشتراك في برو' }));
    expect(calls.find((c) => c.path.endsWith('/subscription'))?.body).toEqual({ plan_code: 'pro', cycle: 'annual' });
    expect(goTo).toHaveBeenCalledWith('https://accept.paymob.com/pay/y');

    const table = screen.getByRole('table');
    expect(within(table).getAllByRole('row')).toHaveLength(features.length + 1);

    await user.click(within(screen.getByRole('region', { name: 'براند' })).getByRole('button', { name: 'جرّب 14 يومًا مجانًا' }));
    expect(calls.find((c) => c.path.endsWith('/billing/trial'))?.body).toEqual({ plan_code: 'brand' });
    expect(await screen.findByText('بدأت تجربتك المجانية.')).toBeInTheDocument();
  });

  it('applies pay as you sell at once and shows the refusal of a choice', async () => {
    routes(trialBilling, {
      [`POST ${base}/billing/subscription`]: () => ({ status: 409, body: { title: 'Conflict', status: 409, detail: 'pay the open invoice first', code: 'settle_first' } }),
    });
    renderAt(`/stores/${store}/billing/plans`);
    const user = userEvent.setup();
    await user.click(within(await screen.findByRole('region', { name: 'حسب مبيعاتك' })).getByRole('button', { name: 'اختيار حسب مبيعاتك' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
  });
});
