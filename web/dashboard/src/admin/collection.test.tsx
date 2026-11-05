import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI } from '../test/api';
import { BalanceCard } from './BalanceCard';
import { Gate } from './Console';
import { entryLabel, signed, toMinor, type BalanceEntry, type StoreBalance } from './collection';
import { AdminSessionContext, type Admin, type AdminSession } from './session';

const store = '01M9STORE00000000000000001';

function session(role: Admin['role']): AdminSession {
  return { state: { kind: 'signed-in', admin: testAdmin({}, role) }, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
}

function renderPage(role: Admin['role'] = 'owner') {
  const value = session(role);
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={['/collection']}>
        <Gate state={value.state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function renderCard(role: Admin['role'] = 'owner') {
  render(
    <AdminSessionContext.Provider value={session(role)}>
      <MemoryRouter>
        <BalanceCard storeId={store} storeName="متجر نور للعطور" />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

const settings = {
  prepaid_enabled: false,
  card_enabled: false,
  minimums: [
    { currency: 'EGP', minimum_minor: 30000 },
    { currency: 'SAR', minimum_minor: 10000 },
    { currency: 'AED', minimum_minor: 10000 },
  ],
};

function entry(over: Partial<BalanceEntry> = {}): BalanceEntry {
  return { id: '01M9ENTRY00000000000000001', kind: 'manual', amount_minor: 50000, balance_after_minor: 50000, created_at: '2026-11-04T10:00:00Z', ...over };
}

function balance(over: Partial<StoreBalance> = {}): StoreBalance {
  return { currency: 'EGP', balance_minor: -4230, minimum_minor: 30000, level: 'debt', entries: [], ...over };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the collection settings in the console', () => {
  it('turns both ways on, changes a minimum and saves with a reason', async () => {
    const calls = mockAPI({
      'GET /admin/v1/billing/collection': () => ({ status: 200, body: settings }),
      'PUT /admin/v1/billing/collection': (c) => ({ status: 200, body: c.body }),
    });
    renderPage();
    const user = userEvent.setup();

    const ways = await screen.findAllByRole('switch');
    expect(ways).toHaveLength(2);
    for (const w of ways) expect(w).not.toBeChecked();
    expect(screen.getByLabelText('مصر · جنيه')).toHaveValue('300');

    for (const w of ways) await user.click(w);
    const egp = screen.getByLabelText('مصر · جنيه');
    await user.clear(egp);
    await user.type(egp, '450.5');
    await user.type(screen.getByLabelText('السبب (إجباري)'), 'فتح الطريقتين');
    await user.click(screen.getByRole('button', { name: 'احفظ' }));

    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت إعدادات التحصيل.');
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({
      prepaid_enabled: true,
      card_enabled: true,
      minimums: [
        { currency: 'EGP', minimum_minor: 45050 },
        { currency: 'SAR', minimum_minor: 10000 },
        { currency: 'AED', minimum_minor: 10000 },
      ],
      reason: 'فتح الطريقتين',
    });
  });

  it('is read-only for a role that may not change billing', async () => {
    mockAPI({ 'GET /admin/v1/billing/collection': () => ({ status: 200, body: settings }) });
    renderPage('viewer');
    expect(await screen.findByRole('note')).toHaveTextContent('عرض فقط');
    expect(screen.getByRole('button', { name: 'احفظ' })).toBeDisabled();
  });
});

describe("a store's balance in the console", () => {
  it('shows a debt and adds a confirmed transfer', async () => {
    const calls = mockAPI({
      [`GET /admin/v1/stores/${store}/balance`]: () => ({
        status: 200,
        body: balance({ collection: 'balance', entries: [entry({ kind: 'fee', amount_minor: -4230, balance_after_minor: -4230 })] }),
      }),
      [`POST /admin/v1/stores/${store}/balance/credits`]: () => ({
        status: 200,
        body: balance({
          balance_minor: 45770,
          level: 'ok',
          entries: [entry({ reason: 'transfer', reference: 'CIB 7741', balance_after_minor: 45770 }), entry({ kind: 'fee', amount_minor: -4230 })],
        }),
      }),
    });
    renderCard();
    const user = userEvent.setup();

    expect(await screen.findByText('بالسالب أو نفد')).toBeInTheDocument();
    expect(screen.getByText('-42.30 ج.م')).toBeInTheDocument();
    expect(screen.getByText('رصيد مسبق')).toBeInTheDocument();
    expect(screen.getByText('رسوم طلب مسلَّم')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'إضافة رصيد' }));
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('يُخصم المستحق (42.30 ج.م) أولًا');
    await user.type(within(dialog).getByLabelText('المبلغ (EGP)'), '500');
    await user.type(within(dialog).getByLabelText('المرجع (رقم التحويل أو ملاحظة قصيرة)'), 'CIB 7741');
    await user.type(within(dialog).getByLabelText('ملاحظة لسجل التدقيق'), 'التحويل ظاهر في كشف الحساب');
    await user.click(within(dialog).getByRole('button', { name: 'أضف الرصيد' }));

    expect(await screen.findByRole('status')).toHaveTextContent('أُضيف 500 ج.م إلى الرصيد.');
    expect(screen.getByText('457.70 ج.م')).toBeInTheDocument();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      amount_minor: 50000,
      reason: 'transfer',
      reference: 'CIB 7741',
      note: 'التحويل ظاهر في كشف الحساب',
    });
  });

  it('offers no credit to a role that may not change billing, and none to an agency', async () => {
    mockAPI({ [`GET /admin/v1/stores/${store}/balance`]: () => ({ status: 200, body: balance({ balance_minor: 0 }) }) });
    renderCard('viewer');
    expect(await screen.findByText('لم يختر بعد')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'إضافة رصيد' })).not.toBeInTheDocument();
    vi.restoreAllMocks();

    mockAPI({
      [`GET /admin/v1/stores/${store}/balance`]: () => ({ status: 409, body: { title: 'Conflict', status: 409, code: 'agency_managed' } }),
    });
    renderCard();
    expect(await screen.findByText(/متجر تابع لوكالة/)).toBeInTheDocument();
  });
});

describe('collection helpers', () => {
  it('reads amounts and labels entries', () => {
    expect(toMinor('300')).toBe(30000);
    expect(toMinor(' 12.5 ')).toBe(1250);
    expect(toMinor('12.345')).toBeNaN();
    expect(toMinor('-5')).toBeNaN();
    expect(signed(-2120)).toBe('−21.20');
    expect(signed(30000)).toBe('+300.00');
    expect(entryLabel(entry({ kind: 'topup' }))).toBe('شحن بالبطاقة');
    expect(entryLabel(entry({ kind: 'fee_refund' }))).toBe('رد رسوم طلب مرتجع');
    expect(entryLabel(entry({ kind: 'invoice', amount_minor: -2800 }))).toBe('ضريبة الفاتورة الشهرية');
    expect(entryLabel(entry({ reason: 'compensation' }))).toBe('إضافة يدوية: تعويض');
  });
});
