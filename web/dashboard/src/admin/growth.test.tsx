import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI } from '../test/api';
import { Gate } from './Console';
import { change, churnStarts, collectedIn, conversionRows, line, plansIn, shownCurrencies, type GrowthData } from './growth';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const months = ['2025-12-01', '2026-01-01', '2026-02-01', '2026-03-01', '2026-04-01', '2026-05-01', '2026-06-01', '2026-07-01', '2026-08-01', '2026-09-01', '2026-10-01', '2026-11-01'];
const egp = [18200, 19900, 21400, 22800, 24100, 26900, 28300, 30700, 33500, 36200, 41200, 9800].map((v) => v * 100);
const created = [210, 245, 268, 301, 322, 350, 371, 402, 438, 471, 512, 96];
const converted = [40, 44, 46, 48, 52, 60, 71, 70, 68, 61, 44, 2];

const growth: GrowthData = {
  computed_at: '2026-11-04T12:00:00Z',
  months,
  collected: [
    { currency: 'EGP', months: months.map((m, i) => ({ month: m, invoices: 10, amount_minor: egp[i] ?? 0 })) },
    { currency: 'SAR', months: months.map((m, i) => ({ month: m, invoices: i === 10 ? 1 : 0, amount_minor: i === 10 ? 9900 : 0 })) },
  ],
  stores: months.map((m, i) => ({ month: m, created: created[i] ?? 0, converted: converted[i] ?? 0 })),
  mrr: [
    { day: '2026-11-04', currency: 'EGP', mrr_minor: 1190000, accounts: 2890 },
    { day: '2026-11-04', currency: 'SAR', mrr_minor: 9900, accounts: 1 },
    { day: '2026-11-18', currency: 'EGP', mrr_minor: 1245000, accounts: 2904 },
  ],
  churn: [],
  plans: [
    { currency: 'EGP', plan: 'merchant', name: 'تاجر', accounts: 1720, mrr_minor: 510000 },
    { currency: 'EGP', plan: 'pro', name: 'برو', accounts: 960, mrr_minor: 460000 },
    { currency: 'SAR', plan: 'pro', name: 'برو', accounts: 1, mrr_minor: 9900 },
  ],
  paying: 2904,
  errors: [],
};

function renderAt(path: string, role: 'owner' | 'finance' | 'content' = 'owner') {
  const state: AdminState = { kind: 'signed-in', admin: testAdmin({}, role) };
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function routes(body: GrowthData = growth) {
  return mockAPI({
    'GET /admin/v1/growth': () => ({ status: 200, body }),
    'GET /admin/v1/alerts': () => ({ status: 200, body: { open: [], resolved: [], email: true } }),
  });
}

describe('growth helpers', () => {
  it('lays each currency over the months, and lists those with figures', () => {
    expect(shownCurrencies(growth)).toEqual(['EGP', 'SAR']);
    expect(shownCurrencies({ ...growth, collected: [], mrr: [], plans: [] })).toEqual(['EGP']);
    expect(collectedIn(growth, 'SAR')[10]).toBe(9900);
    expect(collectedIn(growth, 'AED')).toEqual(Array(12).fill(0));
  });

  it('reads conversion for the six months before this one, and plan shares per currency', () => {
    const rows = conversionRows(growth);
    expect(rows.map((r) => r.month)).toEqual(months.slice(5, 11));
    expect(rows.at(-3)).toEqual({ month: '2026-08-01', created: 438, converted: 68, pct: 16 });
    expect(plansIn(growth, 'EGP').map((p) => p.share)).toEqual([53, 47]);
  });

  it('says how the revenue moved, when churn starts, and draws a line', () => {
    expect(change([{ minor: 1190000 }, { minor: 1245000 }])).toBe('+4.6%');
    expect(change([{ minor: 1 }])).toBeNull();
    expect(churnStarts(growth)).toBe('2026-12-01');
    expect(churnStarts({ ...growth, mrr: [{ day: '2026-12-02', currency: 'EGP', mrr_minor: 1, accounts: 1 }] })).toBe('2026-12-01');
    expect(churnStarts({ ...growth, mrr: [] })).toBeNull();
    expect(line([1, 3], 100, 50)).toBe('M0.0 45.0 L100.0 5.0');
  });
});

describe('growth page', () => {
  it('shows this month in four numbers and every chart, in Egyptian pounds first', async () => {
    routes();
    renderAt('/growth');

    const kpis = await screen.findByRole('region', { name: 'أرقام هذا الشهر' });
    expect(kpis).toHaveTextContent(/الإيراد الشهري المتكرر\s*12,450 ج\.م/);
    expect(kpis).toHaveTextContent('+4.6% منذ 4 نوفمبر');
    expect(kpis).toHaveTextContent(/محصّل هذا الشهر\s*9,800 ج\.م/);
    expect(kpis).toHaveTextContent('أكتوبر كله: 41,200 ج.م');
    expect(kpis).toHaveTextContent(/متاجر جديدة هذا الشهر\s*96/);
    expect(kpis).toHaveTextContent(/تحويل متاجر أغسطس\s*16%/);

    const collected = screen.getByRole('region', { name: 'الإيراد المحصّل شهريًا' });
    expect(within(collected).getByRole('img', { name: /الإيراد المحصّل \(جنيه مصري\)/ })).toBeInTheDocument();
    expect(within(collected).getAllByRole('row')).toHaveLength(12);
    expect(screen.getByRole('region', { name: 'التحويل إلى الدفع' })).toHaveTextContent('68 من 438 (16%)');
    expect(screen.getByRole('region', { name: 'المشتركون والإلغاء' })).toHaveTextContent(/يبدأ في ديسمبر 2026.*اليوم: 2,904 مشترك دافع/);
    const mix = screen.getByRole('region', { name: 'توزيع الباقات اليوم' });
    expect(mix).toHaveTextContent(/تاجر\s*1,720 حساب · 53% من الإيراد/);
  });

  it('switches currency without converting', async () => {
    routes();
    renderAt('/growth');
    const sar = await screen.findByRole('button', { name: 'ريال سعودي' });
    expect(screen.getByRole('button', { name: 'جنيه مصري' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.queryByRole('button', { name: 'درهم إماراتي' })).toBeNull();

    await userEvent.click(sar);
    expect(sar).toHaveAttribute('aria-pressed', 'true');
    const kpis = screen.getByRole('region', { name: 'أرقام هذا الشهر' });
    expect(kpis).toHaveTextContent(/الإيراد الشهري المتكرر\s*99 ر\.س/);
    expect(screen.getByRole('region', { name: 'توزيع الباقات اليوم' })).toHaveTextContent(/برو\s*1 حساب · 100% من الإيراد/);
  });

  it('shows churn once a month is complete, and names a section it could not read', async () => {
    routes({ ...growth, churn: [{ month: '2026-12-01', start: 200, joined: 12, lost: 7, rate_bps: 350 }], errors: ['collected'] });
    renderAt('/growth');
    const churn = await screen.findByRole('region', { name: 'المشتركون والإلغاء' });
    expect(within(churn).getByRole('row', { name: /ديسمبر 2026\s*200\s*12\s*7\s*3.5%/ })).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'الإيراد المحصّل شهريًا' })).toHaveTextContent('تعذّرت القراءة');
  });

  it('says when the recurring revenue is not recorded yet', async () => {
    routes({ ...growth, mrr: [] });
    renderAt('/growth');
    expect(await screen.findByText(/لم يُسجَّل بعد/)).toBeInTheDocument();
    expect(screen.getByRole('region', { name: 'المشتركون والإلغاء' })).toHaveTextContent('يبدأ مع أول تسجيل');
  });

  it('is in the menu for finance, not for content, and content cannot open it', async () => {
    routes();
    renderAt('/growth', 'finance');
    expect(await screen.findByRole('heading', { level: 1, name: 'النمو' })).toBeInTheDocument();
    expect(within(screen.getByRole('navigation', { name: 'قائمة الكونسول' })).getByRole('link', { name: 'النمو' })).toHaveAttribute('href', '/growth');
  });

  it('keeps it from content', () => {
    routes();
    renderAt('/growth', 'content');
    expect(screen.queryByRole('heading', { level: 1, name: 'النمو' })).toBeNull();
    expect(within(screen.getByRole('navigation', { name: 'قائمة الكونسول' })).queryByRole('link', { name: 'النمو' })).toBeNull();
  });
});
