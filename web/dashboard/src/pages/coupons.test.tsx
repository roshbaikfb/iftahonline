import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { couponState, effectOn, emptyForm, isoToZoned, summaryLines, toInput, zonedToISO, type Coupon } from '../lib/coupons';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { CouponEditor } from './CouponEditor';
import { Coupons } from './Coupons';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const couponID = '01M6COUPON0000000000000001';

const session: Session = {
  state: { kind: 'signed-in', me: { user: { id: 'U1', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
  reload: vi.fn(() => Promise.resolve()),
  signOut: vi.fn(() => Promise.resolve()),
};

// coupon is a stored coupon; an override of undefined drops the field.
function coupon(over: { [K in keyof Coupon]?: Coupon[K] | undefined } = {}): Coupon {
  const c: Record<string, unknown> = {
    id: couponID,
    code: 'SUMMER10',
    kind: 'percent',
    percent: 10,
    min_subtotal_minor: 30000,
    max_uses: 200,
    max_per_customer: 1,
    ends_at: '2099-10-31T20:59:00Z',
    active: true,
    uses: 38,
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    ...over,
  };
  return Object.fromEntries(Object.entries(c).filter(([, v]) => v !== undefined)) as Coupon;
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP', locale: 'ar', timezone: 'Asia/Riyadh' } }),
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
          <Route path="/stores/:storeId/coupons" element={<Coupons />} />
          <Route path="/stores/:storeId/coupons/new" element={<CouponEditor />} />
          <Route path="/stores/:storeId/coupons/:couponId" element={<CouponEditor key="edit" />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Coupons', () => {
  it('lists the coupons with their discount, terms, uses, dates and state', async () => {
    routes({
      [`GET ${base}/coupons`]: () => ({
        status: 200,
        body: {
          items: [
            coupon(),
            coupon({ id: 'C2', code: 'WELCOME50', kind: 'fixed', percent: undefined, amount_minor: 5000, min_subtotal_minor: 0, max_uses: undefined, ends_at: undefined, uses: 112 }),
            coupon({ id: 'C3', code: 'VIP15', percent: 15, active: false, ends_at: undefined, max_uses: undefined, max_per_customer: undefined, min_subtotal_minor: 0, uses: 7 }),
            coupon({ id: 'C4', code: 'RAMADAN20', percent: 20, ends_at: '2026-03-30T20:59:00Z', max_uses: 240, uses: 240 }),
          ],
        },
      }),
    });
    renderAt(`/stores/${store}/coupons`);
    const row = (await screen.findByRole('link', { name: 'SUMMER10' })).closest('tr');
    if (!row) throw new Error('no row');
    expect(within(row).getByText('10% من المنتجات')).toBeInTheDocument();
    expect(within(row).getByText('من 300 ج.م · مرة لكل عميل')).toBeInTheDocument();
    expect(within(row).getByText('38 من 200')).toBeInTheDocument();
    expect(within(row).getByText('مفعّل')).toBeInTheDocument();
    const welcome = screen.getByRole('link', { name: 'WELCOME50' }).closest('tr');
    if (!welcome) throw new Error('no row');
    expect(within(welcome).getByText('50 ج.م')).toBeInTheDocument();
    expect(within(welcome).getByText('بلا حد')).toBeInTheDocument();
    expect(within(welcome).getByText('دائم')).toBeInTheDocument();
    expect(within(screen.getByRole('link', { name: 'VIP15' }).closest('tr') ?? document.body).getByText('متوقف')).toBeInTheDocument();
    const ended = screen.getByRole('link', { name: 'RAMADAN20' }).closest('tr');
    if (!ended) throw new Error('no row');
    expect(within(ended).getByText('منتهٍ')).toBeInTheDocument();
    expect(within(ended).getByText(/^انتهى 30 مارس/)).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: 'كوبون جديد' })[0]).toHaveAttribute('href', `/stores/${store}/coupons/new`);
  });

  it('reads the next page on demand', async () => {
    const calls = routes({
      [`GET ${base}/coupons`]: (c) =>
        c.search.get('cursor') === 'C2'
          ? { status: 200, body: { items: [coupon({ id: 'C3', code: 'THIRD' })] } }
          : { status: 200, body: { items: [coupon({ id: 'C1', code: 'FIRST' }), coupon({ id: 'C2', code: 'SECOND' })], next_cursor: 'C2' } },
    });
    renderAt(`/stores/${store}/coupons`);
    const user = userEvent.setup();
    expect(await screen.findByRole('link', { name: 'SECOND' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'المزيد' }));
    expect(await screen.findByRole('link', { name: 'THIRD' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'FIRST' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'المزيد' })).toBeNull();
    expect(calls.filter((c) => c.path === `${base}/coupons`).at(-1)?.search.get('cursor')).toBe('C2');
  });

  it('invites the first coupon when there is none', async () => {
    routes({ [`GET ${base}/coupons`]: () => ({ status: 200, body: { items: [] } }) });
    renderAt(`/stores/${store}/coupons`);
    expect(await screen.findByRole('heading', { name: 'لا كوبونات بعد' })).toBeInTheDocument();
    expect(screen.queryByRole('table')).toBeNull();
  });
});

describe('CouponEditor', () => {
  it('makes a fixed coupon with terms and dates in the store\'s time zone', async () => {
    const calls = routes({
      [`POST ${base}/coupons`]: (c) => ({ status: 201, body: { ...coupon(), ...(c.body as object) } }),
      [`GET ${base}/coupons`]: () => ({ status: 200, body: { items: [] } }),
    });
    renderAt(`/stores/${store}/coupons/new`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('الكود'), 'eid-50');
    await user.click(screen.getByRole('button', { name: 'مبلغ ثابت' }));
    await user.type(screen.getByLabelText('المبلغ'), '50');
    await user.type(screen.getByLabelText('أقل مجموع للمنتجات (اختياري)'), '300');
    await user.type(screen.getByLabelText('عدد مرات الاستخدام كله (اختياري)'), '100');
    await user.type(screen.getByLabelText('ينتهي (اختياري)'), '2026-11-15T23:59');
    expect(screen.getByText('بتوقيت متجرك (الرياض).')).toBeInTheDocument();
    const summary = screen.getByTestId('coupon-summary');
    expect(within(summary).getByText('خصم 50 ج.م على المنتجات')).toBeInTheDocument();
    expect(within(summary).getByText('للطلبات التي منتجاتها 300 ج.م أو أكثر')).toBeInTheDocument();
    expect(within(summary).getByText('100 استخدامًا كحد أقصى')).toBeInTheDocument();
    expect(within(summary).getByText('حتى 15 نوفمبر')).toBeInTheDocument();
    expect(screen.getByText('−50 ج.م')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'حفظ الكوبون' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
        code: 'EID-50',
        kind: 'fixed',
        amount_minor: 5000,
        min_subtotal_minor: 30000,
        max_uses: 100,
        max_per_customer: 1,
        ends_at: '2026-11-15T20:59:00.000Z',
        active: true,
      });
    });
    expect(await screen.findByRole('heading', { name: 'الكوبونات' })).toBeInTheDocument();
  });

  it('checks the fields before sending, and shows a taken code on its field', async () => {
    const calls = routes({
      [`POST ${base}/coupons`]: () => ({ status: 409, body: { type: 'x', title: 'Conflict', status: 409, code: 'coupon_code_taken' } }),
    });
    renderAt(`/stores/${store}/coupons/new`);
    const user = userEvent.setup();
    const code = await screen.findByLabelText('الكود');
    await user.type(code, 'ab');
    await user.type(screen.getByLabelText('يبدأ (اختياري)'), '2026-11-15T10:00');
    await user.type(screen.getByLabelText('ينتهي (اختياري)'), '2026-11-14T10:00');
    await user.click(screen.getByRole('button', { name: 'حفظ الكوبون' }));
    expect(screen.getByText('من 3 إلى 32 حرفًا إنجليزيًا أو رقمًا أو - أو _.')).toBeInTheDocument();
    expect(screen.getByText('بعد موعد البدء.')).toBeInTheDocument();
    expect(code).toHaveAttribute('aria-invalid', 'true');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);

    await user.clear(code);
    await user.type(code, 'SUMMER10');
    await user.clear(screen.getByLabelText('ينتهي (اختياري)'));
    await user.click(screen.getByRole('button', { name: 'حفظ الكوبون' }));
    expect(await screen.findByText('كوبون آخر يحمل هذا الكود.')).toBeInTheDocument();
  });

  it('edits a coupon, and deletes one no order used', async () => {
    const calls = routes({
      [`GET ${base}/coupons/${couponID}`]: () => ({ status: 200, body: coupon({ uses: 0 }) }),
      [`PUT ${base}/coupons/${couponID}`]: (c) => ({ status: 200, body: { ...coupon(), ...(c.body as object) } }),
      [`DELETE ${base}/coupons/${couponID}`]: () => ({ status: 204 }),
      [`GET ${base}/coupons`]: () => ({ status: 200, body: { items: [] } }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/coupons/${couponID}`);
    const user = userEvent.setup();
    expect(await screen.findByLabelText('الكود')).toHaveValue('SUMMER10');
    expect(screen.getByLabelText('ينتهي (اختياري)')).toHaveValue('2099-10-31T23:59');
    expect(screen.getByRole('button', { name: 'نسبة مئوية' })).toHaveAttribute('aria-pressed', 'true');
    await user.click(screen.getByRole('button', { name: 'مفعّل' }));
    await user.click(screen.getByRole('button', { name: 'حفظ الكوبون' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ code: 'SUMMER10', kind: 'percent', percent: 10, active: false, ends_at: '2099-10-31T20:59:00.000Z' });
    });

    renderAt(`/stores/${store}/coupons/${couponID}`);
    await user.click(await screen.findByRole('button', { name: 'حذف الكوبون' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
    });
  });

  it('keeps a used coupon: no delete', async () => {
    routes({ [`GET ${base}/coupons/${couponID}`]: () => ({ status: 200, body: coupon() }) });
    renderAt(`/stores/${store}/coupons/${couponID}`);
    expect(await screen.findByText(/استُخدم في 38 طلب/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'حذف الكوبون' })).toBeNull();
  });
});

describe('coupon helpers', () => {
  it('reads and writes dates in the store\'s time zone', () => {
    expect(zonedToISO('2026-10-31T23:59', 'Asia/Riyadh')).toBe('2026-10-31T20:59:00.000Z');
    expect(zonedToISO('2026-10-31T23:59', 'Asia/Dubai')).toBe('2026-10-31T19:59:00.000Z');
    expect(isoToZoned('2026-10-31T20:59:00Z', 'Asia/Riyadh')).toBe('2026-10-31T23:59');
    // Cairo keeps summer time until the last Thursday of October.
    expect(zonedToISO('2026-07-01T12:00', 'Africa/Cairo')).toBe('2026-07-01T09:00:00.000Z');
    expect(zonedToISO('2026-12-01T12:00', 'Africa/Cairo')).toBe('2026-12-01T10:00:00.000Z');
  });

  it('builds the request and refuses what the server would', () => {
    expect(toInput({ ...emptyForm, code: ' summer10 ', percent: '10', perCustomer: '' }, 'Asia/Riyadh')).toEqual({
      input: { code: 'SUMMER10', kind: 'percent', percent: 10, active: true },
      errors: {},
    });
    expect(toInput({ ...emptyForm, code: 'OK1', percent: '101' }, 'Asia/Riyadh').errors).toHaveProperty('percent');
    expect(toInput({ ...emptyForm, code: 'OK1', kind: 'fixed', amount: '0' }, 'Asia/Riyadh').errors).toHaveProperty('amount_minor');
    expect(toInput({ ...emptyForm, code: 'OK1', maxUses: '0' }, 'Asia/Riyadh').errors).toHaveProperty('max_uses');
    expect(toInput({ ...emptyForm, code: 'نص' }, 'Asia/Riyadh').errors).toHaveProperty('code');
  });

  it('says what a coupon does and when', () => {
    const now = Date.parse('2026-10-29T12:00:00Z');
    expect(couponState(coupon({ starts_at: '2026-11-01T00:00:00Z' }), 'Asia/Riyadh', now).label).toBe('يبدأ 1 نوفمبر');
    expect(couponState(coupon({ uses: 200 }), 'Asia/Riyadh', now).label).toBe('استُنفد');
    expect(effectOn({ ...emptyForm, percent: '10' }, 50000, 'EGP')).toBe('−50 ج.م');
    expect(effectOn({ ...emptyForm, kind: 'fixed', amount: '700' }, 50000, 'EGP')).toBe('−500 ج.م');
    expect(effectOn({ ...emptyForm, kind: 'free_shipping' }, 50000, 'EGP')).toBe('شحن مجاني');
    expect(summaryLines({ ...emptyForm, perCustomer: '' }, 'EGP', 'Asia/Riyadh')).toEqual(['خصم 10% على المنتجات', 'لكل الطلبات', 'بلا حد لعدد الاستخدامات', 'بلا تاريخ انتهاء']);
  });
});
