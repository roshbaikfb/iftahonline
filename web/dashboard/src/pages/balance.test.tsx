import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { entryText, feeOf, leave, rateText, signedMoney, topUpAmounts, type BalanceEntry } from '../lib/billing';
import type { Role } from '../lib/settings';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Balance } from './Balance';
import { Billing } from './Billing';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

function session(role: Role): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: 'U1', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' },
        memberships: [{ store_id: store, role }],
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const invoice = {
  id: '01M7INVOICE000000000000001',
  number: 'INV-2026-000184',
  period: '2026-11-01',
  currency: 'EGP',
  total_minor: 34086,
  due_at: '2026-11-04T00:00:00Z',
  attempts: 2,
};

const pastDue = {
  plan_name_ar: 'تاجر',
  plan_name_en: 'Merchant',
  status: 'past_due',
  grace_until: '2026-11-11T00:00:00Z',
  closes_at: '2026-12-11T00:00:00Z',
  open_invoices: [invoice],
  switch_offer: { rate_bps: 150, offered: ['balance', 'card'] },
};

const balance = {
  currency: 'EGP',
  balance_minor: 27880,
  minimum_minor: 30000,
  level: 'ok',
  collection: 'balance',
  offered: ['balance', 'card'],
  entries: [
    { id: '01M9E3', kind: 'fee_refund', amount_minor: 1275, balance_after_minor: 27880, created_at: '2026-11-04T10:00:00Z' },
    { id: '01M9E2', kind: 'fee', amount_minor: -1275, balance_after_minor: 26605, created_at: '2026-11-03T10:00:00Z' },
    { id: '01M9E1', kind: 'topup', amount_minor: 30000, balance_after_minor: 30000, reference: 'txn-1', created_at: '2026-11-03T09:00:00Z' },
  ],
};

function routes(billing: unknown, over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر نور', slug: 'noor', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
    [`GET ${base}/billing`]: () => ({ status: 200, body: billing }),
    [`GET ${base}/billing/plans`]: () => ({ status: 200, body: { currency: 'EGP', features: [], plans: [] } }),
    [`GET ${base}/usage`]: () => ({ status: 200, body: { items: [], ad_free: true } }),
    [`GET ${base}/billing/balance`]: () => ({ status: 200, body: balance }),
    ...over,
  });
}

function renderAt(path: string, role: Role = 'owner') {
  return render(
    <SessionContext.Provider value={session(role)}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/billing" element={<Billing />} />
          <Route path="/stores/:storeId/billing/balance" element={<Balance />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the unpaid store’s banner (ADR-0087)', () => {
  it('offers to pay or to switch, and switches to the balance', async () => {
    const calls = routes(pastDue, {
      [`POST ${base}/billing/switch-to-pays`]: () => ({
        status: 200,
        body: { ...pastDue, status: 'active', plan_name_ar: 'حسب مبيعاتك', switch_offer: undefined, open_invoices: [], collection: 'balance' },
      }),
    });
    renderAt(`/stores/${store}/billing`);
    const user = userEvent.setup();

    const banner = await screen.findByRole('alert');
    expect(banner).toHaveTextContent('لم ننجح في تحصيل فاتورة نوفمبر 2026');
    expect(within(banner).getByRole('button', { name: 'ادفع الآن 340.86 ج.م' })).toBeInTheDocument();
    await user.click(within(banner).getByRole('button', { name: 'حوّل إلى «ادفع حسب مبيعاتك»' }));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('1.5% من كل طلب يُسلَّم');
    expect(dialog).toHaveTextContent('12.75 ج.م');
    expect(dialog).toHaveTextContent('تُلغى');
    // No card saved: the card is shown, not offered.
    expect(within(dialog).getByRole('radio', { name: /البطاقة المحفوظة/ })).toBeDisabled();
    await user.click(within(dialog).getByRole('checkbox'));
    await user.click(within(dialog).getByRole('button', { name: 'حوّل واشحن الرصيد' }));

    expect(await screen.findByRole('heading', { name: 'الرصيد' })).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith('/switch-to-pays'))?.body).toEqual({ collection: 'balance' });
  });

  it('tells an agency’s store to talk to the platform, and a balance that ran out to top up', async () => {
    routes({ ...pastDue, agency: true, switch_offer: undefined });
    const first = renderAt(`/stores/${store}/billing`);
    const banner = await screen.findByRole('alert');
    expect(banner).toHaveTextContent('تواصل مع فريق المنصة');
    expect(within(banner).queryByRole('button', { name: /حوّل/ })).not.toBeInTheDocument();
    first.unmount();
    vi.restoreAllMocks();

    routes({ ...pastDue, plan_name_ar: 'حسب مبيعاتك', open_invoices: [], switch_offer: undefined, collection: 'balance' });
    renderAt(`/stores/${store}/billing`);
    const ranOut = await screen.findByRole('alert');
    expect(ranOut).toHaveTextContent('نفد رصيدك');
    expect(within(ranOut).getByRole('link', { name: 'اشحن الرصيد' })).toHaveAttribute('href', `/stores/${store}/billing/balance`);
  });

  it('is not shown to a manager', async () => {
    const calls = routes(pastDue);
    renderAt(`/stores/${store}/billing`, 'manager');
    expect(await screen.findByText('الفوترة لمالك المتجر فقط.')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(calls.some((c) => c.path === `${base}/billing`)).toBe(false);
  });
});

describe('the balance page (ADR-0084, ADR-0085)', () => {
  it('shows the balance and its entries, tops it up on Paymob and changes the way', async () => {
    const goTo = vi.spyOn(leave, 'goTo').mockImplementation(() => undefined);
    const calls = routes(
      { ...pastDue, status: 'active', open_invoices: [], switch_offer: undefined, collection: 'balance' },
      {
        [`POST ${base}/billing/balance/topups`]: () => ({ status: 200, body: { checkout_url: 'https://pay.example.com/bc_1' } }),
        [`PUT ${base}/billing/collection`]: () => ({ status: 200, body: { ...pastDue, status: 'active', collection: 'card' } }),
      },
    );
    renderAt(`/stores/${store}/billing/balance`);
    const user = userEvent.setup();

    expect(await screen.findByText('278.80 ج.م', { selector: '.balance-now' })).toBeInTheDocument();
    expect(screen.getByText('كافٍ')).toBeInTheDocument();
    for (const name of ['300 ج.م', '500 ج.م', '1,000 ج.م']) expect(screen.getByRole('button', { name })).toBeInTheDocument();
    expect(screen.getByText('رد رسوم طلب مرتجع')).toBeInTheDocument();
    expect(screen.getByText('−12.75 ج.م')).toBeInTheDocument();

    // Below the minimum: refused here, nothing sent.
    await user.type(screen.getByLabelText(/أو مبلغ آخر/), '250');
    await user.click(screen.getByRole('button', { name: /بالبطاقة$/ }));
    expect(await screen.findByRole('alert')).toHaveTextContent('أقل مبلغ للشحن 300 ج.م');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);

    await user.click(screen.getByRole('button', { name: '500 ج.م' }));
    await user.click(screen.getByRole('button', { name: 'ادفع 500 ج.م بالبطاقة' }));
    expect(goTo).toHaveBeenCalledWith('https://pay.example.com/bc_1');
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ amount_minor: 50000 });

    await user.click(screen.getByRole('button', { name: 'البطاقة المحفوظة' }));
    expect(await screen.findByRole('status')).toHaveTextContent('صارت الرسوم تُدفع من البطاقة المحفوظة.');
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ collection: 'card' });
  });
});

describe('balance helpers', () => {
  it('computes the fee, the top-up amounts and the entries’ words', () => {
    expect(rateText(150)).toBe('1.5%');
    expect(feeOf(85000, 150)).toBe(1275);
    expect(feeOf(124000, 150)).toBe(1860);
    expect(topUpAmounts(30000)).toEqual([30000, 50000, 100000]);
    expect(topUpAmounts(10000)).toEqual([10000, 17000, 33000]);
    expect(signedMoney(-1275, 'EGP')).toBe('−12.75 ج.م');
    const e = (kind: BalanceEntry['kind'], reference?: string): BalanceEntry => ({
      id: '1',
      kind,
      amount_minor: 1,
      balance_after_minor: 1,
      created_at: '',
      ...(reference ? { reference } : {}),
    });
    expect(entryText(e('invoice', 'INV-2026-000200'))).toBe('ضريبة الفاتورة INV-2026-000200');
    expect(entryText(e('manual'))).toBe('إضافة من فريق المنصة');
    expect(entryText(e('topup'))).toBe('شحن بالبطاقة');
  });
});
