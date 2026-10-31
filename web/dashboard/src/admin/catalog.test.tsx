import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { details } from './lib';
import { byCode, draftFrom, toInput, type Plan } from './plans';
import { AdminSessionContext, type AdminSession } from './session';

const admin = { id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' };
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
  plan({}),
  plan({ id: '01M8PLAN0000000000000000AB', version: 0, active: false }),
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
    pays.fees = { EGP: { rate: '2.5', min: '1', max: '' }, SAR: { rate: '3', min: '', max: '50' } };
    const input = toInput(pays, 'نسبة');
    expect(typeof input !== 'string' && input.fees).toEqual([
      { currency: 'EGP', rate_bps: 250, min_minor: 100 },
      { currency: 'SAR', rate_bps: 300, min_minor: 0, max_minor: 5000 },
    ]);
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

describe('plans screen', () => {
  it('publishes the next version, retires a plan, and adds a feature', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('توقف البيع');
    const calls = routes({
      'POST /admin/v1/plans': (c) => ({ status: 201, body: { ...plan({}), version: 2, ...(c.body as object) } }),
      'POST /admin/v1/plans/pro/retire': () => ({ status: 204 }),
      'PUT /admin/v1/features/live_chat': (c) => ({ status: 200, body: c.body }),
    });
    renderAt('/plans');
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: /برو/ }));
    const editor = screen.getByRole('form', { name: 'إصدار جديد' });
    expect(within(editor).getByRole('heading')).toHaveTextContent('برو — إصدار جديد (v2)');
    expect(within(editor).getByLabelText('دردشة')).toBeInTheDocument();
    const egp = within(editor).getByLabelText('EGP شهري');
    await user.clear(egp);
    await user.type(egp, '649');
    await user.type(within(editor).getByLabelText('سبب التغيير (إجباري)'), 'رفع السعر');
    await user.click(within(editor).getByRole('button', { name: 'انشر الإصدار' }));
    const sent = body(calls, 'POST', '/admin/v1/plans') as { prices: { amount_minor: number }[]; reason: string };
    expect(sent.prices[0]?.amount_minor).toBe(64900);
    expect(sent.reason).toBe('رفع السعر');
    expect(await screen.findByText(/نُشر برو v2/)).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'إيقاف بيع الباقة' }));
    expect(body(calls, 'POST', '/admin/v1/plans/pro/retire')).toEqual({ reason: 'توقف البيع' });

    const add = screen.getByRole('form', { name: 'ميزة جديدة' });
    await user.type(within(add).getByLabelText('المفتاح'), 'live_chat');
    await user.type(within(add).getByLabelText('الاسم بالعربية'), 'دردشة مباشرة');
    await user.type(within(add).getByLabelText('الاسم بالإنجليزية'), 'Live chat');
    await user.type(within(add).getByLabelText('السبب'), 'طلب المبيعات');
    await user.click(within(add).getByRole('button', { name: 'حفظ الميزة' }));
    expect(body(calls, 'PUT', '/admin/v1/features/live_chat')).toEqual({ kind: 'flag', name_ar: 'دردشة مباشرة', name_en: 'Live chat', sort: 0, reason: 'طلب المبيعات' });
  });

  it('offers no retiring of the free plan, and shows a refused version', async () => {
    routes({ 'POST /admin/v1/plans': () => ({ status: 409, body: { title: 'Conflict', status: 409, detail: 'a plan’s kind cannot change between versions' } }) });
    renderAt('/plans');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /المجانية/ }));
    expect(screen.queryByRole('button', { name: 'إيقاف بيع الباقة' })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('سبب التغيير (إجباري)'), 'تجربة');
    await user.click(screen.getByRole('button', { name: 'انشر الإصدار' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('cannot change');
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
    expect(await screen.findByText('store.impersonated')).toBeInTheDocument();
    expect(screen.getAllByText('reason: تذكرة 9')).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'المزيد' }));
    expect(await screen.findByText('store.suspended')).toBeInTheDocument();
    expect(calls.at(-1)?.search.get('cursor')).toBe('n1');
    expect(screen.queryByRole('button', { name: 'المزيد' })).not.toBeInTheDocument();
  });
});
