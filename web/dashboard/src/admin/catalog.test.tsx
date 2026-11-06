import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { details } from './lib';
import { byCode, draftFrom, facts, headline, otherPrices, saving, stores, toInput, type Feature, type Plan } from './plans';
import { AdminSessionContext, type AdminSession } from './session';

const admin = testAdmin({ id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' });
const store = '01M5STORE0000000000000000A';
const agency = '01M8AGENCY00000000000000AA';

function renderAt(path: string) {
  const state = { kind: 'signed-in', admin } as const;
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function plan(over: Partial<Plan>): Plan {
  return {
    id: '01M8PLAN0000000000000000AA',
    code: 'pro',
    version: 1,
    kind: 'subscription',
    name_ar: 'برو',
    name_en: 'Pro',
    trial_days: 14,
    public: true,
    sort: 40,
    active: true,
    subscribers: 0,
    prices: [
      { currency: 'EGP', cycle: 'monthly', amount_minor: 59900 },
      { currency: 'EGP', cycle: 'annual', amount_minor: 599000 },
      { currency: 'SAR', cycle: 'monthly', amount_minor: 9900 },
      { currency: 'AED', cycle: 'monthly', amount_minor: 9900 },
    ],
    grants: [
      { feature: 'orders_per_month', unlimited: false, value: 1500 },
      { feature: 'products', unlimited: true, value: 0 },
      { feature: 'coupons', unlimited: false, value: 1 },
    ],
    fees: [],
    ...over,
  };
}

const plans = [
  plan({ subscribers: 20 }),
  plan({ id: '01M8PLAN0000000000000000AB', version: 0, active: false, subscribers: 3 }),
  plan({ id: '01M8PLAN0000000000000000AC', code: 'free', kind: 'free', name_ar: 'المجانية', name_en: 'Free', trial_days: 0, prices: [], sort: 10 }),
  plan({ id: '01M8PLAN0000000000000000AD', code: 'agency', kind: 'agency', name_ar: 'وكالة', name_en: 'Agency' }),
];
const features = [
  { key: 'orders_per_month', kind: 'monthly', name_ar: 'الطلبات في الشهر', name_en: 'Orders', sort: 10, enforced: true },
  { key: 'products', kind: 'count', name_ar: 'المنتجات', name_en: 'Products', sort: 30, enforced: true },
  { key: 'coupons', kind: 'flag', name_ar: 'الكوبونات', name_en: 'Coupons', sort: 70, enforced: true },
  { key: 'live_chat', kind: 'flag', name_ar: 'دردشة', name_en: 'Chat', sort: 900, enforced: false },
];

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/plans': () => ({ status: 200, body: { items: plans } }),
    'GET /admin/v1/features': () => ({ status: 200, body: { items: features } }),
    ...over,
  });
}

const body = (calls: Call[], method: string, path: string) => calls.find((c) => c.method === method && c.path === path)?.body;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('plan editor helpers', () => {
  it('turns a version into the next one’s form and back', () => {
    const d = draftFrom(plan({}));
    expect(d.prices['EGP-monthly']).toBe('599');
    expect(d.grants['products']).toEqual({ value: '', unlimited: true });
    d.prices['EGP-monthly'] = '649.50';
    d.grants['coupons'] = { value: '', unlimited: false }; // not granted
    expect(toInput(d, 'سعر جديد')).toEqual({
      code: 'pro',
      kind: 'subscription',
      name_ar: 'برو',
      name_en: 'Pro',
      trial_days: 14,
      sort: 40,
      public: true,
      reason: 'سعر جديد',
      prices: [
        { currency: 'EGP', cycle: 'monthly', amount_minor: 64950 },
        { currency: 'EGP', cycle: 'annual', amount_minor: 599000 },
        { currency: 'SAR', cycle: 'monthly', amount_minor: 9900 },
        { currency: 'AED', cycle: 'monthly', amount_minor: 9900 },
      ],
      grants: [
        { feature: 'orders_per_month', unlimited: false, value: 1500 },
        { feature: 'products', unlimited: true },
      ],
    });
    expect(toInput({ ...d, prices: { 'EGP-monthly': 'abc' } }, 'x')).toBe('سعر EGP الشهري غير صحيح');
    expect(toInput({ ...d, trial_days: '1.5' }, 'x')).toBe('أيام التجربة رقم صحيح');
    expect(toInput({ ...d, grants: { staff: { value: 'two', unlimited: false } } }, 'x')).toBe('قيمة «staff» رقم صحيح');
  });

  it('reads pay as you sell’s fees, and groups versions under the one on sale', () => {
    const pays = draftFrom(undefined, 'pays');
    pays.code = 'pays2';
    pays.fees = {
      EGP: { rate: '2.5', fixed: '', min: '1', max: '' },
      USD: { rate: '1', fixed: '0.04', min: '', max: '' },
      SAR: { rate: '3', fixed: '', min: '', max: '50' },
    };
    const input = toInput(pays, 'نسبة');
    expect(typeof input !== 'string' && input.fees).toEqual([
      { currency: 'EGP', rate_bps: 250, fixed_minor: 0, min_minor: 100 },
      { currency: 'USD', rate_bps: 100, fixed_minor: 4, min_minor: 0 },
      { currency: 'SAR', rate_bps: 300, fixed_minor: 0, min_minor: 0, max_minor: 5000 },
    ]);
    expect(toInput({ ...pays, fees: { USD: { rate: '1', fixed: 'four cents', min: '', max: '' } } }, 'x')).toBe('رسوم USD غير صحيحة');
    const head = plans[0];
    if (!head) throw new Error('no plan');
    expect(draftFrom({ ...head, kind: 'pays', fees: [{ currency: 'USD', rate_bps: 100, fixed_minor: 4, min_minor: 0 }] }).fees['USD']).toEqual({
      rate: '1',
      fixed: '0.04',
      min: '0',
      max: '',
    });
    expect(typeof input !== 'string' && input.prices).toBeUndefined();
    const groups = byCode(plans);
    expect(groups.map((g) => [g.code, g.head.version, g.versions.length])).toEqual([
      ['pro', 1, 2],
      ['free', 1, 1],
      ['agency', 1, 1],
    ]);
    expect(details({ slug: 'mona', reason: 'تذكرة 9', minutes: 30 })).toBe('reason: تذكرة 9 · minutes: 30 · slug: mona');
  });
});

describe('plan cards helpers', () => {
  it.each([
    [29900, 299000, 'يوفّر شهرين'],
    [10000, 110000, 'يوفّر شهرًا'],
    [10000, 90000, 'يوفّر 3 أشهر'],
    [10000, 116000, ''], // less than half a month
    [undefined, 299000, ''],
  ])('saving(%s, %s) = %j', (m, y, want) => {
    expect(saving(m, y)).toBe(want);
  });

  it('writes each kind’s price line and the first limits', () => {
    expect(headline(plan({}))).toEqual({ price: '599 ج.م', per: '/ شهر', sub: '5,990 ج.م سنويًا · يوفّر شهرين' });
    expect(headline(plan({ prices: plan({}).prices.filter((x) => x.cycle === 'monthly') })).sub).toBe('');
    expect(headline(plan({ kind: 'pays', prices: [], fees: [{ currency: 'EGP', rate_bps: 250, fixed_minor: 0, min_minor: 0 }] }))).toEqual({ price: '2.5%', per: 'من كل طلب مسلّم', sub: 'بلا حد أدنى ولا أقصى' });
    expect(headline(plan({ kind: 'pays', prices: [], fees: [{ currency: 'EGP', rate_bps: 300, fixed_minor: 0, min_minor: 500, max_minor: 5000 }] })).sub).toBe('حد أدنى 5 ج.م · حد أقصى 50 ج.م');
    expect(headline(plan({ kind: 'free', prices: [] }))).toEqual({ price: '0 ج.م', per: '', sub: 'بلا اشتراك' });
    expect(headline(plan({ kind: 'agency' })).sub).toBe('سعر واحد لكل متاجر الوكالة');
    expect(otherPrices(plan({}))).toBe('99 ر.س شهريًا · 99 د.إ شهريًا');
    expect(facts(plan({}), features as Feature[])).toEqual(['الطلبات في الشهر: 1,500', 'المنتجات: بلا حد', 'تجربة 14 يومًا']);
    expect(facts(plan({ trial_days: 0, grants: [] }), features as Feature[])).toEqual(['بلا تجربة']);
  });
});

describe('subscribers', () => {
  it.each([
    [0, 'لا مشتركين'],
    [1, 'مشترك واحد'],
    [2, 'مشتركان'],
    [7, '7 مشتركين'],
    [23, '23 مشتركًا'],
    [1200, '1,200 مشتركًا'],
  ])('stores(%i) = %s', (n, want) => {
    expect(stores(n)).toBe(want);
  });

  it('counts the stores on every version of a plan, and says so where it matters', async () => {
    routes();
    renderAt('/plans');
    const pro = (await screen.findByRole('heading', { name: /برو/ })).closest('li');
    expect(pro).toHaveTextContent('23 مشتركًا');
  });
});

describe('plans screen', () => {
  it('shows a card per plan as merchants see them, in their order', async () => {
    routes();
    renderAt('/plans');
    const pro = (await screen.findByRole('heading', { name: /برو/ })).closest('li');
    if (!pro) throw new Error('no card');
    expect(pro).toHaveTextContent('599 ج.م');
    expect(pro).toHaveTextContent('معروضة');
    expect(pro).toHaveTextContent('الإصدار v1');
    expect(within(pro).getByRole('link', { name: 'تعديل برو' })).toHaveAttribute('href', '/plans/pro');
    const names = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent);
    expect(names[0]).toMatch(/المجانية/); // sort 10 before 40
    expect(screen.getByRole('link', { name: 'باقة جديدة' })).toHaveAttribute('href', '/plans/new');
  });

  it('publishes the next version from a plan’s page, with switches for features', async () => {
    const calls = routes({ 'POST /admin/v1/plans': (c) => ({ status: 201, body: { ...plan({}), version: 2, ...(c.body as object) } }) });
    renderAt('/plans/pro');
    const user = userEvent.setup();
    const editor = await screen.findByRole('form', { name: 'إصدار جديد من برو' });
    expect(within(editor).getByText('يوفّر شهرين')).toBeInTheDocument();
    expect(editor).toHaveTextContent('على الباقة الآن 23 مشتركًا. ينتقل كلٌّ منهم إلى v2 عند تجديده القادم');
    expect(screen.getByText('سجل الإصدارات').closest('details')).toHaveTextContent('v1 · 20 مشتركًا');
    // The pound and the dollar first, each with whom it bills; the riyal
    // and the dirham, for older accounts, folded under them (ADR-0090).
    const prices = within(editor).getByRole('region', { name: 'الأسعار' });
    expect(prices).toHaveTextContent('باقي البلاد · مطلوبة');
    const legacy = within(prices).getByText('الريال والدرهم (الحسابات القديمة فقط)').closest('details');
    expect(legacy).not.toHaveAttribute('open');
    expect(legacy).toContainElement(within(prices).getByLabelText('SAR شهري'));
    const egp = within(editor).getByLabelText('EGP شهري');
    await user.clear(egp);
    await user.type(egp, '649');
    const coupons = within(editor).getByRole('switch', { name: 'الكوبونات' });
    expect(coupons).toBeChecked();
    await user.click(coupons);
    expect(within(editor).getByRole('switch', { name: 'دردشة' })).not.toBeChecked();
    await user.type(within(editor).getByLabelText('سبب التغيير'), 'رفع السعر');
    await user.click(within(editor).getByRole('button', { name: 'انشر الإصدار v2' }));
    const sent = body(calls, 'POST', '/admin/v1/plans') as { prices: { amount_minor: number }[]; grants: { feature: string; value?: number }[]; reason: string };
    expect(sent.prices[0]?.amount_minor).toBe(64900);
    expect(sent.grants.find((g) => g.feature === 'coupons')).toEqual({ feature: 'coupons', unlimited: false, value: 0 });
    expect(sent.reason).toBe('رفع السعر');
    expect(await screen.findByRole('status')).toHaveTextContent('نُشر برو v2');
    expect(screen.getByRole('button', { name: 'انشر الإصدار v3' })).toBeInTheDocument();
  });

  it('retires a plan from its danger zone, with a reason', async () => {
    const calls = routes({ 'POST /admin/v1/plans/pro/retire': () => ({ status: 204 }) });
    renderAt('/plans/pro');
    const user = userEvent.setup();
    await user.click(await screen.findByText('منطقة خطرة: إيقاف بيع الباقة'));
    const zone = screen.getByRole('form', { name: 'إيقاف بيع الباقة' });
    expect(zone).toHaveTextContent('من عليها الآن (23 مشتركًا) يبقون عليها');
    await user.type(within(zone).getByLabelText('السبب'), 'توقف البيع');
    await user.click(within(zone).getByRole('button', { name: 'أوقف بيع «برو»' }));
    expect(body(calls, 'POST', '/admin/v1/plans/pro/retire')).toEqual({ reason: 'توقف البيع' });
    expect(await screen.findByRole('status')).toHaveTextContent('أُوقف بيع الباقة');
    expect(screen.getByText('متوقفة عن البيع')).toBeInTheDocument();
    expect(screen.queryByText('منطقة خطرة: إيقاف بيع الباقة')).toBeNull();
  });

  it('offers no retiring of the free plan, shows a refused version, and says when a plan is unknown', async () => {
    routes({ 'POST /admin/v1/plans': () => ({ status: 409, body: { title: 'Conflict', status: 409, detail: 'a plan’s kind cannot change between versions' } }) });
    renderAt('/plans/free');
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('سبب التغيير'), 'تجربة');
    expect(screen.queryByText('منطقة خطرة: إيقاف بيع الباقة')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'انشر الإصدار v2' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('cannot change');
  });

  it('says when no plan has the code', async () => {
    routes();
    renderAt('/plans/gold');
    expect(await screen.findByText(/لا توجد باقة بهذا الكود/)).toBeInTheDocument();
  });

  it('starts a new plan with its code and kind, then opens its page', async () => {
    const gold = plan({ id: '01M8PLAN0000000000000000AE', code: 'gold', name_ar: 'ذهبية', name_en: 'Gold' });
    let created = false;
    const calls = routes({
      'GET /admin/v1/plans': () => ({ status: 200, body: { items: created ? [...plans, gold] : plans } }),
      'POST /admin/v1/plans': (c) => {
        created = true;
        return { status: 201, body: { ...gold, ...(c.body as object) } };
      },
    });
    renderAt('/plans/new');
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('الكود'), 'gold');
    await user.type(screen.getByLabelText('الاسم بالعربية'), 'ذهبية');
    await user.type(screen.getByLabelText('الاسم بالإنجليزية'), 'Gold');
    await user.type(screen.getByLabelText('EGP شهري'), '999');
    await user.type(screen.getByLabelText('سبب التغيير'), 'باقة جديدة');
    await user.click(screen.getByRole('button', { name: 'انشر الباقة' }));
    expect((body(calls, 'POST', '/admin/v1/plans') as { code: string; kind: string }).code).toBe('gold');
    expect(await screen.findByRole('heading', { level: 1, name: 'ذهبية' })).toBeInTheDocument();
  });

  it('lists the features in their tab and adds one from a dialog', async () => {
    const calls = routes({ 'PUT /admin/v1/features/live_chat': (c) => ({ status: 200, body: c.body }) });
    renderAt('/plans?tab=features');
    const user = userEvent.setup();
    const table = await screen.findByRole('region', { name: 'كتالوج المميزات' });
    expect(within(table).getByText('orders_per_month')).toBeInTheDocument();
    expect(within(table).getByText('عرض فقط')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'ميزة جديدة' }));
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();

    await user.click(screen.getByRole('button', { name: 'ميزة جديدة' }));
    const dialog = screen.getByRole('dialog', { name: 'ميزة جديدة' });
    await user.type(within(dialog).getByLabelText('الاسم بالعربية'), 'دردشة مباشرة');
    await user.type(within(dialog).getByLabelText('الاسم بالإنجليزية'), 'Live chat');
    await user.type(within(dialog).getByLabelText('المفتاح'), 'live_chat');
    await user.click(within(dialog).getByLabelText(/عدد/));
    await user.type(within(dialog).getByLabelText('السبب'), 'طلب المبيعات');
    await user.click(within(dialog).getByRole('button', { name: 'حفظ الميزة' }));
    expect(body(calls, 'PUT', '/admin/v1/features/live_chat')).toEqual({ kind: 'count', name_ar: 'دردشة مباشرة', name_en: 'Live chat', sort: 910, reason: 'طلب المبيعات' });
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت الميزة «دردشة مباشرة».');
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('admin log filters', () => {
  const entry = (id: string, over: object = {}) => ({
    id,
    occurred_at: new Date().toISOString(),
    admin_id: '01M5ADMIN000000000000000BB',
    admin_name: 'سارة',
    action: 'store.suspended',
    target_type: 'store',
    target_id: store,
    store_name: 'متجر الأناقة',
    ip_prefix: '10.0.0.0/24',
    metadata: { reason: 'بلاغ' },
    ...over,
  });
  const admins = { items: [{ id: admin.id, name: 'منى', email: 'ops@iftahonline.com', active: true }, { id: '01M5ADMIN000000000000000BB', name: 'سارة', email: 's@x.com', active: false }] };

  it('names who did it and the store, and narrows by kind, admin, dates and store', async () => {
    const calls = mockAPI({
      'GET /admin/v1/audit-log': () => ({ status: 200, body: { items: [entry('E1')] } }),
      'GET /admin/v1/admins': () => ({ status: 200, body: admins }),
    });
    renderAt('/audit');
    const user = userEvent.setup();
    const row = (await screen.findByText('أوقف متجرًا')).closest('li');
    if (!row) throw new Error('no entry');
    expect(within(row).getByRole('link', { name: 'متجر الأناقة' })).toHaveAttribute('href', `/stores/${store}`);
    expect(row).toHaveTextContent('سارة');
    const last = () => calls.filter((c) => c.path === '/admin/v1/audit-log').at(-1)?.search;

    await user.click(screen.getByRole('button', { name: 'المتاجر' }));
    expect(screen.getByRole('button', { name: 'المتاجر' })).toHaveAttribute('aria-pressed', 'true');
    await waitFor(() => {
      expect(last()?.get('family')).toBe('store');
    });

    const filters = screen.getByRole('region', { name: 'تصفية السجل' });
    expect(within(filters).getByRole('option', { name: 'منى (أنت)' })).toBeInTheDocument();
    expect(within(filters).getByRole('option', { name: 'سارة — موقوف' })).toBeInTheDocument();
    await user.selectOptions(within(filters).getByLabelText('الأدمن'), '01M5ADMIN000000000000000BB');
    await user.type(within(filters).getByLabelText('من'), '2026-11-01');
    await user.type(within(filters).getByLabelText('إلى'), '2026-11-03');
    await user.click(within(filters).getByRole('button', { name: 'تصفية' }));
    await waitFor(() => {
      expect(last()?.get('admin')).toBe('01M5ADMIN000000000000000BB');
    });
    expect(last()?.get('from')).toBe('2026-11-01');
    expect(last()?.get('to')).toBe('2026-11-03');
    expect(last()?.get('family')).toBe('store');

    await user.click(screen.getByRole('button', { name: 'مسح التصفية' }));
    await waitFor(() => {
      expect(last()?.has('admin')).toBe(false);
    });
    expect(last()?.has('family')).toBe(false);
  });

  it('shows one store’s history from its address, and says when nothing matches', async () => {
    const calls = mockAPI({
      'GET /admin/v1/audit-log': (c) => ({ status: 200, body: { items: c.search.get('family') ? [] : [entry('E1')] } }),
      'GET /admin/v1/admins': () => ({ status: 200, body: admins }),
    });
    renderAt(`/audit?store=${store}`);
    const user = userEvent.setup();
    const filters = await screen.findByRole('region', { name: 'تصفية السجل' });
    expect(await within(filters).findByRole('link', { name: 'متجر الأناقة' })).toHaveAttribute('href', `/stores/${store}`);
    expect(calls.find((c) => c.path === '/admin/v1/audit-log')?.search.get('store')).toBe(store);
    await user.click(screen.getByRole('button', { name: 'المحتوى' }));
    expect(await screen.findByText('لا شيء يطابق هذه التصفية.')).toBeInTheDocument();
    await user.click(within(filters).getByRole('button', { name: 'كل المتاجر' }));
    await waitFor(() => {
      expect(calls.filter((c) => c.path === '/admin/v1/audit-log').at(-1)?.search.has('store')).toBe(false);
    });
  });
});

describe('agencies and the log', () => {
  it('opens an agency, lists its stores by name, and takes one out', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('طلب الوكالة');
    const ag = { id: agency, name: 'وكالة النيل', currency: 'EGP', plan_code: 'agency', stores: 1, created_at: '2026-10-31T10:00:00Z' };
    const calls = routes({
      'GET /admin/v1/agencies': () => ({ status: 200, body: { items: [] } }),
      'POST /admin/v1/agencies': () => ({ status: 201, body: { agency: { ...ag, stores: 0 }, store_ids: [] } }),
      [`GET /admin/v1/agencies/${agency}`]: () => ({ status: 200, body: { agency: ag, store_ids: [store] } }),
      [`GET /admin/v1/stores/${store}`]: () => ({ status: 200, body: { store: { id: store, name: 'متجر الأناقة' }, domains: [] } }),
      [`POST /admin/v1/agencies/${agency}/stores/${store}/remove`]: () => ({ status: 200, body: { agency: ag, store_ids: [] } }),
    });
    renderAt('/agencies');
    const user = userEvent.setup();
    expect(await screen.findByText('لا توجد وكالات بعد.')).toBeInTheDocument();
    const form = screen.getByRole('form', { name: 'وكالة جديدة' });
    await user.type(within(form).getByLabelText('الاسم'), 'وكالة النيل');
    await user.selectOptions(within(form).getByLabelText('العملة'), 'EGP');
    await user.type(within(form).getByLabelText('السبب'), 'عقد جديد');
    await user.click(within(form).getByRole('button', { name: 'إنشاء الوكالة' }));
    expect(body(calls, 'POST', '/admin/v1/agencies')).toEqual({ name: 'وكالة النيل', currency: 'EGP', plan_code: 'agency', reason: 'عقد جديد' });

    expect(await screen.findByRole('heading', { name: 'وكالة النيل' })).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'متجر الأناقة' })).toHaveAttribute('href', `/stores/${store}`);
    await user.click(screen.getByRole('button', { name: 'إخراج من الوكالة' }));
    expect(body(calls, 'POST', `/admin/v1/agencies/${agency}/stores/${store}/remove`)).toEqual({ reason: 'طلب الوكالة' });
  });

  it('pages through the admin log', async () => {
    const entry = (id: string, action: string) => ({
      id,
      occurred_at: '2026-10-31T10:00:00Z',
      admin_id: admin.id,
      action,
      target_type: 'store',
      target_id: store,
      ip_prefix: '10.0.0.0/24',
      metadata: { reason: 'تذكرة 9' },
    });
    const calls = mockAPI({
      'GET /admin/v1/audit-log': (c) =>
        c.search.get('cursor') ? { status: 200, body: { items: [entry('E2', 'store.suspended')] } } : { status: 200, body: { items: [entry('E1', 'store.impersonated')], next_cursor: 'n1' } },
    });
    renderAt('/audit');
    const user = userEvent.setup();
    const first = (await screen.findByText('فتح جلسة دعم في متجر')).closest('li');
    if (!first) throw new Error('no entry');
    expect(within(first).getByRole('link', { name: 'صفحة المتجر' })).toHaveAttribute('href', `/stores/${store}`);
    expect(first).toHaveTextContent('السبب: تذكرة 9');
    expect(first).toHaveTextContent('أنت');
    expect(screen.getAllByText('السبب: تذكرة 9')).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'عرض الأقدم' }));
    expect(await screen.findByText('أوقف متجرًا')).toBeInTheDocument();
    expect(calls.at(-1)?.search.get('cursor')).toBe('n1');
    expect(screen.queryByRole('button', { name: 'عرض الأقدم' })).not.toBeInTheDocument();
  });
});
