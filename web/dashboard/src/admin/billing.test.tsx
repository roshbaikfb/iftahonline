import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { attemptsText, daysText, exportHref, invoiceNote, invoiceQuery, paymentQuery, sums, type Invoice, type InvoiceDetail, type Payment, type Summary } from './billing';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const admin = testAdmin({ id: '01M5ADMIN000000000000000AA', email: 'owner@iftahonline.com', name: 'أحمد' });
const store = '01M5STORE0000000000000000A';
const agency = '01M5AGNCY0000000000000000A';
const now = new Date('2026-11-11T10:00:00Z');

const late: Invoice = {
  id: '01M5INV00000000000000000AA',
  number: 'INV-2026-000184',
  account_id: '01M5ACCT00000000000000000A',
  account_kind: 'store',
  account_name: 'متجر الأناقة',
  store_id: store,
  period: '2026-11-01',
  currency: 'EGP',
  subtotal_minor: 59900,
  tax_bps: 1400,
  tax_minor: 8386,
  total_minor: 68286,
  status: 'open',
  overdue: true,
  issued_at: '2026-11-01T07:00:00Z',
  due_at: '2026-11-08T07:00:00Z',
  attempts: 2,
  last_error: 'البطاقة مرفوضة',
  next_attempt_at: '2026-11-12T07:00:00Z',
};
const agencyInvoice: Invoice = {
  ...late,
  id: '01M5INV00000000000000000AB',
  number: 'INV-2026-000183',
  account_id: agency,
  account_kind: 'agency',
  account_name: 'وكالة النيل',
  overdue: false,
  attempts: 0,
  due_at: '2026-11-15T07:00:00Z',
};
delete (agencyInvoice as Partial<Invoice>).last_error;
const paid: Invoice = { ...late, id: '01M5INV00000000000000000AC', number: 'INV-2026-000182', overdue: false, status: 'paid', paid_at: '2026-11-07T09:00:00Z', attempts: 0 };
const declined: Payment = {
  id: '01M5PAY00000000000000000AA',
  created_at: '2026-11-10T07:00:00Z',
  invoice_id: late.id,
  invoice_number: late.number,
  account_kind: 'store',
  account_name: 'متجر الأناقة',
  store_id: store,
  gateway: 'paymob',
  outcome: 'declined',
  reason: 'رصيد غير كافٍ',
  reference: '9041',
  amount_minor: 68286,
  currency: 'EGP',
};
const duplicate: Payment = { ...declined, id: '01M5PAY00000000000000000AB', outcome: 'duplicate', reference: '8810' };
delete (duplicate as Partial<Payment>).reason;
const summary: Summary = {
  open: [
    { currency: 'EGP', count: 20, total_minor: 1482000, tax_minor: 0 },
    { currency: 'SAR', count: 3, total_minor: 129000, tax_minor: 0 },
  ],
  overdue: [{ currency: 'EGP', count: 6, total_minor: 342000, tax_minor: 0 }],
  collected: [{ currency: 'EGP', count: 60, total_minor: 4125000, tax_minor: 506600 }],
  month_start: '2026-10-31T22:00:00Z',
  failed: { count: 9, reasons: [{ outcome: 'declined', reason: 'البطاقة مرفوضة', count: 5 }] },
};

function renderAt(path: string) {
  const state: AdminState = { kind: 'signed-in', admin };
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/billing/summary': () => ({ status: 200, body: summary }),
    'GET /admin/v1/invoices': (c) => ({ status: 200, body: c.search.get('cursor') ? { items: [paid] } : { items: [late, agencyInvoice], next_cursor: 'c1' } }),
    'GET /admin/v1/payments': () => ({ status: 200, body: { items: [declined, duplicate] } }),
    ...over,
  });
}

afterEach(() => {
  vi.useRealTimers();
});

describe('billing helpers', () => {
  it('words days, attempts and an invoice’s note', () => {
    expect([1, 2, 3, 11].map(daysText)).toEqual(['يوم', 'يومين', '3 أيام', '11 يومًا']);
    expect([1, 2, 4, 12].map(attemptsText)).toEqual(['محاولة', 'محاولتان', '4 محاولات', '12 محاولة']);
    expect(invoiceNote(late, now)).toBe('متأخرة 3 أيام · محاولتان · البطاقة مرفوضة');
    expect(invoiceNote(agencyInvoice, now)).toBe('مستحقة 15 نوفمبر');
    expect(invoiceNote(paid, now)).toBe('دُفعت 7 نوفمبر');
    expect(invoiceNote({ ...late, status: 'void', overdue: false }, now)).toBe('');
  });

  it('keeps currencies apart', () => {
    expect(sums(summary.open)).toBe('14,820 ج.م · 1,290 ر.س');
    expect(sums(summary.collected, (c) => c.tax_minor)).toBe('5,066 ج.م');
  });

  it('reads only known filters from the address, and exports with them', () => {
    expect(invoiceQuery(new URLSearchParams('status=overdue&currency=USD&q=%20النيل%20&tab=x'))).toEqual({ status: 'overdue', q: 'النيل' });
    expect(paymentQuery(new URLSearchParams('outcome=lost&gateway=Pay Mob'))).toEqual({});
    expect(paymentQuery(new URLSearchParams('outcome=duplicate&gateway=paymob'))).toEqual({ outcome: 'duplicate', gateway: 'paymob' });
    expect(exportHref({})).toBe('/admin/v1/invoices/export');
    expect(exportHref({ status: 'paid', currency: 'EGP', q: 'INV-2026' })).toBe('/admin/v1/invoices/export?status=paid&currency=EGP&q=INV-2026');
  });
});

describe('billing page', () => {
  it('shows the summary per currency, the invoices with their state, and more on request', async () => {
    vi.useFakeTimers({ now, toFake: ['Date'] });
    const calls = routes();
    renderAt('/billing');
    const cards = await screen.findByRole('region', { name: 'ملخص' });
    expect(within(cards).getByText('14,820 ج.م · 1,290 ر.س')).toBeInTheDocument();
    expect(within(cards).getByText('منها ضريبة: 5,066 ج.م')).toBeInTheDocument();
    expect(within(cards).getByText('أكثر سبب: البطاقة مرفوضة')).toBeInTheDocument();
    expect(within(cards).getByRole('link', { name: /متأخرة/ })).toHaveAttribute('href', '/billing?status=overdue');

    const list = screen.getByRole('region', { name: 'الفواتير' });
    expect(await within(list).findByRole('link', { name: late.number })).toHaveAttribute('href', `/billing/invoices/${late.id}`);
    expect(within(list).getByRole('link', { name: 'متجر الأناقة' })).toHaveAttribute('href', `/stores/${store}`);
    expect(within(list).getByRole('link', { name: 'وكالة النيل' })).toHaveAttribute('href', `/agencies/${agency}`);
    expect(within(list).getByText('متأخرة 3 أيام · محاولتان · البطاقة مرفوضة')).toBeInTheDocument();
    expect(within(list).getByRole('link', { name: 'تصدير CSV' })).toHaveAttribute('href', '/admin/v1/invoices/export');

    await userEvent.click(within(list).getByRole('button', { name: 'عرض المزيد' }));
    expect(await within(list).findByRole('link', { name: paid.number })).toBeInTheDocument();
    expect(calls.filter((c) => c.path === '/admin/v1/invoices').at(-1)?.search.get('cursor')).toBe('c1');
    expect(within(list).queryByRole('button', { name: 'عرض المزيد' })).not.toBeInTheDocument();
  });

  it('filters by status, currency and search in the address, and exports the same', async () => {
    const calls = routes();
    renderAt('/billing?status=overdue');
    const list = await screen.findByRole('region', { name: 'الفواتير' });
    expect(within(list).getByRole('button', { name: 'متأخرة' })).toHaveAttribute('aria-pressed', 'true');
    await waitFor(() => {
      expect(calls.find((c) => c.path === '/admin/v1/invoices')?.search.get('status')).toBe('overdue');
    });
    await userEvent.selectOptions(within(list).getByLabelText('العملة'), 'EGP');
    await userEvent.type(within(list).getByRole('searchbox', { name: 'بحث' }), 'النيل');
    await userEvent.click(within(list).getByRole('button', { name: 'بحث' }));
    await waitFor(() => {
      const last = calls.filter((c) => c.path === '/admin/v1/invoices').at(-1);
      expect(Object.fromEntries(last?.search ?? [])).toEqual({ status: 'overdue', currency: 'EGP', q: 'النيل' });
    });
    expect(within(list).getByRole('link', { name: 'تصدير CSV' })).toHaveAttribute('href', `/admin/v1/invoices/export?status=overdue&currency=EGP&q=${encodeURIComponent('النيل')}`);
    await userEvent.click(within(list).getByRole('button', { name: 'الكل' }));
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/admin/v1/invoices').at(-1)?.search.get('status')).toBeNull();
    });
  });

  it('says when nothing matches, and shows a refused list', async () => {
    routes({ 'GET /admin/v1/invoices': () => ({ status: 200, body: { items: [] } }) });
    renderAt('/billing?q=zzz');
    expect(await screen.findByText('لا فواتير تطابق هذه التصفية.')).toBeInTheDocument();
  });

  it('lists payments, filters them, and records a duplicate’s refund', async () => {
    const calls = routes({ 'POST /admin/v1/billing/duplicates/*': () => ({ status: 204 }) });
    renderAt('/billing?tab=payments');
    const list = await screen.findByRole('region', { name: 'المدفوعات' });
    expect(screen.getByRole('tab', { name: 'المدفوعات' })).toHaveAttribute('aria-selected', 'true');
    expect(await within(list).findByText('رصيد غير كافٍ')).toBeInTheDocument();
    expect(within(list).getAllByRole('cell', { name: /^Paymob/ })).toHaveLength(2);

    await userEvent.click(within(list).getByRole('button', { name: 'مكررة' }));
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/admin/v1/payments').at(-1)?.search.get('outcome')).toBe('duplicate');
    });
    await userEvent.selectOptions(within(list).getByLabelText('البوابة'), 'manual');
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/admin/v1/payments').at(-1)?.search.get('gateway')).toBe('manual');
    });

    await userEvent.click(within(list).getByRole('button', { name: 'تم الاسترداد' }));
    const dialog = screen.getByRole('dialog', { name: 'سجّل الاسترداد' });
    await userEvent.type(within(dialog).getByLabelText('رقم عملية الاسترداد'), 'RF-1');
    await userEvent.type(within(dialog).getByLabelText('السبب'), 'refunded on Paymob');
    await userEvent.click(within(dialog).getByRole('button', { name: 'سجّل' }));
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({
      path: `/admin/v1/billing/duplicates/${duplicate.id}/refunded`,
      body: { reference: 'RF-1', reason: 'refunded on Paymob' },
    });
    expect(within(list).queryByRole('button', { name: 'تم الاسترداد' })).not.toBeInTheDocument();
  });
});

describe('invoice page', () => {
  const detail: InvoiceDetail = {
    invoice: late,
    lines: [{ kind: 'subscription', description: 'متجر الأناقة: باقة برو (شهري)', quantity: 1, amount_minor: 59900, store_id: store }],
    payments: [declined],
  };

  it('shows the lines and VAT, the attempts, and settles a transfer', async () => {
    vi.useFakeTimers({ now, toFake: ['Date'] });
    const settled: InvoiceDetail = {
      invoice: { ...late, status: 'paid', overdue: false, paid_at: '2026-11-11T10:00:00Z' },
      lines: detail.lines,
      payments: [{ ...declined, id: 'P2', outcome: 'manual', gateway: 'manual', reference: 'TRX-5512' }, declined],
    };
    const calls = routes({
      [`GET /admin/v1/invoices/${late.id}`]: () => ({ status: 200, body: detail }),
      [`POST /admin/v1/invoices/${late.id}/settle`]: () => ({ status: 200, body: settled }),
    });
    renderAt(`/billing/invoices/${late.id}`);
    expect(await screen.findByRole('heading', { level: 1, name: /INV-2026-000184/ })).toHaveTextContent('متأخرة 3 أيام');
    const lines = screen.getByRole('region', { name: 'البنود' });
    expect(within(lines).getByText('ضريبة القيمة المضافة 14%')).toBeInTheDocument();
    expect(within(lines).getAllByText('682.86 ج.م')).toHaveLength(1);
    expect(screen.getByRole('region', { name: 'محاولات الدفع' })).toHaveTextContent('المحاولة القادمة 12 نوفمبر');

    const settle = screen.getByRole('region', { name: 'سجّل كمدفوعة بتحويل' });
    await userEvent.type(within(settle).getByLabelText('رقم التحويل'), 'TRX-5512');
    await userEvent.type(within(settle).getByLabelText('السبب'), 'وصل التحويل');
    await userEvent.click(within(settle).getByRole('button', { name: 'سجّل كمدفوعة' }));
    expect(await screen.findByRole('status')).toHaveTextContent('سُجّلت الفاتورة مدفوعة.');
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ reference: 'TRX-5512', reason: 'وصل التحويل' });
    expect(screen.queryByRole('region', { name: 'سجّل كمدفوعة بتحويل' })).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'محاولات الدفع' })).toHaveTextContent('تحويل يدوي');
  });

  it('shows a refused settlement', async () => {
    routes({
      [`GET /admin/v1/invoices/${late.id}`]: () => ({ status: 200, body: detail }),
      [`POST /admin/v1/invoices/${late.id}/settle`]: () => ({ status: 404, body: { title: 'Not Found', status: 404, detail: 'no open invoice with this ID' } }),
    });
    renderAt(`/billing/invoices/${late.id}`);
    const settle = await screen.findByRole('region', { name: 'سجّل كمدفوعة بتحويل' });
    await userEvent.type(within(settle).getByLabelText('رقم التحويل'), 'T-1');
    await userEvent.type(within(settle).getByLabelText('السبب'), 'وصل التحويل');
    await userEvent.click(within(settle).getByRole('button', { name: 'سجّل كمدفوعة' }));
    expect(await within(settle).findByRole('alert')).toHaveTextContent('لم تعد الفاتورة مفتوحة. حدّث الصفحة.');
  });

  it('says so for an unknown invoice', async () => {
    routes({ 'GET /admin/v1/invoices/nope': () => ({ status: 404, body: { title: 'Not Found', status: 404, detail: 'no invoice with this ID' } }) });
    renderAt('/billing/invoices/nope');
    expect(await screen.findByRole('alert')).toHaveTextContent('لا فاتورة بهذا الرقم.');
    expect(screen.getByRole('link', { name: 'كل الفواتير' })).toHaveAttribute('href', '/billing');
  });
});
