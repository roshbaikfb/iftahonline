import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { byCountry, countryName, percent, rate, toBps, toMicros } from './countries';
import { AdminSessionContext, type Admin, type AdminSession } from './session';

function renderTab(tab: string, role: Admin['role'] = 'owner') {
  const value: AdminSession = { state: { kind: 'signed-in', admin: testAdmin({}, role) }, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[`/collection?tab=${tab}`]}>
        <Gate state={value.state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

const rules = {
  gateways: ['paymob', 'stripe'],
  items: [
    { country: 'ZZ', billing_gateway: 'stripe', store_gateways: ['stripe'] },
    { country: 'EG', billing_gateway: 'paymob', store_gateways: ['paymob'] },
  ],
};
const taxes = {
  items: [
    { country: 'ZZ', rate_bps: 0, name_ar: 'لا ضريبة', name_en: 'No tax' },
    { country: 'EG', rate_bps: 1400, name_ar: 'ضريبة القيمة المضافة', name_en: 'VAT' },
  ],
};

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/alerts': () => ({ status: 200, body: { email: true, resolved: [], open: [] } }),
    'GET /admin/v1/billing/payment-rules': () => ({ status: 200, body: rules }),
    'GET /admin/v1/billing/taxes': () => ({ status: 200, body: taxes }),
    'GET /admin/v1/billing/fx-rates': () => ({
      status: 200,
      body: {
        items: [
          { currency: 'SAR', per_usd_micros: 3_750_000 },
          { currency: 'AED', per_usd_micros: 3_672_500 },
        ],
      },
    }),
    ...over,
  });
}

const sent = (calls: Call[], path: string) => calls.find((c) => c.method !== 'GET' && c.path === path)?.body;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the country helpers', () => {
  it('reads and writes rates, percentages and names', () => {
    expect(toMicros('48.5')).toBe(48_500_000);
    expect(toMicros('3.6725')).toBe(3_672_500);
    expect(toMicros('0.000001')).toBe(1);
    for (const bad of ['0', '', '1.2345678', '-1', 'x']) expect(toMicros(bad)).toBeNaN();
    expect(rate(3_750_000)).toBe('3.75');
    expect(toBps('14')).toBe(1400);
    expect(toBps('2.5')).toBe(250);
    expect(toBps('1.234')).toBeNaN();
    expect(percent(1500)).toBe('15');
    expect(countryName('ZZ')).toBe('باقي البلاد');
    expect(countryName('EG')).toBe('مصر');
    expect(countryName('SA')).toBe('السعودية');
    expect(countryName('KW')).toBe('الكويت');
    expect(countryName('Q1')).toBe('Q1');
    expect(byCountry(rules.items).map((r) => r.country)).toEqual(['EG', 'ZZ']);
  });
});

describe('payment rules and taxes by country in the console (ADR-0089)', () => {
  it("lists each country's rule, the others last, and edits a rule with a reason", async () => {
    const calls = routes({ 'PUT /admin/v1/billing/payment-rules/EG': (c) => ({ status: 200, body: c.body }) });
    renderTab('countries');
    const user = userEvent.setup();

    const table = await screen.findByRole('region', { name: 'قواعد الدفع' });
    const rows = await within(table).findAllByRole('row');
    expect(rows[1]).toHaveTextContent('مصر');
    expect(rows[1]).toHaveTextContent('Paymob · جنيه');
    expect(rows[2]).toHaveTextContent('باقي البلاد');
    expect(rows[2]).toHaveTextContent('Stripe · دولار');
    // The others' row is changed, never removed.
    expect(within(table).queryByRole('button', { name: 'حذف قاعدة باقي البلاد' })).toBeNull();

    await user.click(within(table).getByRole('button', { name: 'تعديل قاعدة مصر' }));
    const dialog = screen.getByRole('dialog', { name: 'تعديل قاعدة مصر' });
    expect(within(dialog).getByRole('radio', { name: 'Paymob' })).toBeChecked();
    await user.click(within(dialog).getByRole('checkbox', { name: 'Stripe' }));
    await user.type(within(dialog).getByLabelText('السبب (إجباري، يُحفظ في سجل الأدمن)'), 'تجربة Stripe');
    await user.click(within(dialog).getByRole('button', { name: 'احفظ' }));

    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت قاعدة مصر.');
    expect(sent(calls, '/admin/v1/billing/payment-rules/EG')).toEqual({
      billing_gateway: 'paymob',
      store_gateways: ['paymob', 'stripe'],
      reason: 'تجربة Stripe',
    });
  });

  it("adds a country's rule, refuses a bad code, and removes one", async () => {
    const calls = routes({
      'PUT /admin/v1/billing/payment-rules/SA': (c) => ({ status: 200, body: c.body }),
      'POST /admin/v1/billing/payment-rules/EG/remove': () => ({ status: 204 }),
    });
    renderTab('countries');
    const user = userEvent.setup();
    const table = await screen.findByRole('region', { name: 'قواعد الدفع' });
    await within(table).findAllByRole('row');

    await user.click(within(table).getByRole('button', { name: 'أضف بلدًا' }));
    const add = screen.getByRole('dialog', { name: 'قاعدة بلد جديد' });
    await user.type(within(add).getByLabelText('رمز البلد (ISO، حرفان)'), 'sa');
    await user.click(within(add).getByRole('checkbox', { name: 'Paymob' }));
    await user.type(within(add).getByLabelText('السبب (إجباري، يُحفظ في سجل الأدمن)'), 'Paymob مؤقتًا');
    await user.click(within(add).getByRole('button', { name: 'احفظ' }));
    expect(await screen.findByText(`حُفظت قاعدة ${countryName('SA')}.`)).toBeInTheDocument();
    expect(sent(calls, '/admin/v1/billing/payment-rules/SA')).toEqual({ billing_gateway: 'stripe', store_gateways: ['paymob'], reason: 'Paymob مؤقتًا' });

    await user.click(within(table).getByRole('button', { name: 'حذف قاعدة مصر' }));
    const remove = screen.getByRole('dialog', { name: 'حذف قاعدة مصر' });
    expect(remove).toHaveTextContent('سيتبع مصر صف باقي البلاد.');
    await user.type(within(remove).getByLabelText('السبب (إجباري، يُحفظ في سجل الأدمن)'), 'توحيد');
    await user.click(within(remove).getByRole('button', { name: 'احذف' }));
    expect(await screen.findByRole('status')).toHaveTextContent('مصر يتبع الآن صف باقي البلاد.');
    expect(sent(calls, '/admin/v1/billing/payment-rules/EG/remove')).toEqual({ reason: 'توحيد' });
  });

  it("sets a country's tax and shows a refusal in the dialog", async () => {
    const calls = routes({
      'PUT /admin/v1/billing/taxes/EG': (c) => ({ status: 200, body: c.body }),
      'PUT /admin/v1/billing/taxes/SA': () => ({ status: 422, body: { title: 'Unprocessable', status: 422, detail: 'rate out of range' } }),
    });
    renderTab('countries');
    const user = userEvent.setup();
    const table = await screen.findByRole('region', { name: 'الضرائب على فواتير المنصة' });
    expect(await within(table).findByText('14%')).toBeInTheDocument();

    await user.click(within(table).getByRole('button', { name: 'تعديل ضريبة مصر' }));
    const edit = screen.getByRole('dialog', { name: 'ضريبة مصر' });
    const r = within(edit).getByLabelText('النسبة %');
    await user.clear(r);
    await user.type(r, '14.5');
    await user.type(within(edit).getByLabelText('السبب (إجباري، يُحفظ في سجل الأدمن)'), 'قانون جديد');
    await user.click(within(edit).getByRole('button', { name: 'احفظ' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت ضريبة مصر.');
    expect(sent(calls, '/admin/v1/billing/taxes/EG')).toEqual({ rate_bps: 1450, name_ar: 'ضريبة القيمة المضافة', name_en: 'VAT', reason: 'قانون جديد' });

    await user.click(within(table).getByRole('button', { name: 'أضف بلدًا' }));
    const add = screen.getByRole('dialog', { name: 'ضريبة بلد جديد' });
    await user.type(within(add).getByLabelText('رمز البلد (ISO، حرفان)'), 'SA');
    await user.type(within(add).getByLabelText('النسبة %'), '15');
    await user.type(within(add).getByLabelText('السبب (إجباري، يُحفظ في سجل الأدمن)'), 'السعودية');
    await user.click(within(add).getByRole('button', { name: 'احفظ' }));
    expect(await within(add).findByRole('alert')).toHaveTextContent('rate out of range');
  });

  it('is read-only for a role that may not change billing', async () => {
    routes();
    renderTab('countries', 'viewer');
    const table = await screen.findByRole('region', { name: 'قواعد الدفع' });
    await within(table).findAllByRole('row');
    expect(screen.getByRole('note')).toHaveTextContent('عرض فقط');
    expect(within(table).queryByRole('button', { name: 'تعديل قاعدة مصر' })).toBeNull();
  });
});

describe('exchange rates in the console (ADR-0090)', () => {
  it('warns of the unset pound, sets it with a reason, and refuses zero', async () => {
    const calls = routes({ 'PUT /admin/v1/billing/fx-rates/EGP': (c) => ({ status: 200, body: { currency: 'EGP', ...(c.body as object) } }) });
    renderTab('fx');
    const user = userEvent.setup();

    expect(await screen.findByText(/سعر الجنيه المصري غير محدد/)).toBeInTheDocument();
    const table = screen.getByRole('region', { name: 'كم وحدة من كل عملة تساوي دولارًا واحدًا' });
    expect(within(table).getByText('3.75')).toBeInTheDocument();
    expect(within(table).getByText('3.6725')).toBeInTheDocument();

    await user.click(within(table).getByRole('button', { name: 'حدد السعر سعر الجنيه المصري' }));
    const dialog = screen.getByRole('dialog', { name: 'سعر الجنيه المصري' });
    const input = within(dialog).getByLabelText(/1 دولار = \(بالجنيه\)/);
    await user.type(input, '0');
    await user.type(within(dialog).getByLabelText('السبب (إجباري، يُحفظ مع السعر السابق)'), 'سعر البنك المركزي');
    await user.click(within(dialog).getByRole('button', { name: 'احفظ السعر' }));
    expect(within(dialog).getByRole('alert')).toHaveTextContent('أكبر من صفر');

    await user.clear(input);
    await user.type(input, '48.5');
    await user.click(within(dialog).getByRole('button', { name: 'احفظ السعر' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظ سعر الصرف.');
    expect(sent(calls, '/admin/v1/billing/fx-rates/EGP')).toEqual({ per_usd_micros: 48_500_000, reason: 'سعر البنك المركزي' });
  });
});
