import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { waited } from './lib';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const store = '01M5STORE0000000000000000A';
const invoice = '01M7INVOICE000000000000001';
const admin = testAdmin({ id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' });

function renderConsole(state: AdminState, path = '/', session: Partial<AdminSession> = {}) {
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()), ...session };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
  return value;
}

const health = {
  checked_at: '2026-10-31T13:05:00Z',
  database: true,
  redis: false,
  errors: ['redis'],
  stores: { active: 4812, held: 37, suspended: 6, closed: 141, total: 4996 },
  billing: { trialing: 512, active: 2904, past_due: 58, suspended: 3, cancelled: 1, invoices_open: 214, invoices_overdue: 61 },
  jobs: { available: 23, running: 8, retryable: 4, discarded_24h: 1, oldest_available_seconds: 12 },
};
const storeRow = {
  store: { id: store, slug: 'alanaqa', name: 'متجر الأناقة', status: 'active', plan: 'pro', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo', created_at: '2026-10-28T10:00:00Z', updated_at: '' },
  primary_host: 'alanaqa.iftahonline.com',
};

function consoleRoutes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/health': () => ({ status: 200, body: health }),
    // The server's open alert for the Redis the health finds down.
    'GET /admin/v1/alerts': () => ({
      status: 200,
      body: { email: true, resolved: [], open: [{ id: 'AL1', kind: 'redis', severity: 'red', value: 0, title: 'Redis لا يرد', body: '', path: '/', opened_at: '', last_seen_at: '' }] },
    }),
    'GET /admin/v1/stores': (c) => ({ status: 200, body: c.search.get('cursor') ? { items: [] } : { items: [storeRow], next_cursor: 'c1' } }),
    [`GET /admin/v1/stores/${store}`]: () => ({ status: 200, body: { store: storeRow.store, domains: [{ id: 'D1', host: 'alanaqa.iftahonline.com', kind: 'subdomain', status: 'verified', is_primary: true, created_at: '' }] } }),
    [`GET /admin/v1/stores/${store}/balance`]: () => ({
      status: 200,
      body: { currency: 'EGP', balance_minor: 0, minimum_minor: 30000, level: 'debt', entries: [] },
    }),
    [`GET /admin/v1/stores/${store}/billing`]: () => ({
      status: 200,
      body: {
        plan_name_ar: 'برو',
        plan_name_en: 'Pro',
        status: 'past_due',
        cycle: 'monthly',
        grace_until: '2026-11-15T00:00:00Z',
        period_end: '2026-11-01T00:00:00Z',
        open_invoices: [{ id: invoice, number: 'INV-2026-000184', period: '2026-10-01', currency: 'EGP', total_minor: 68286, due_at: '2026-11-08T00:00:00Z', attempts: 2 }],
      },
    }),
    [`GET /admin/v1/stores/${store}/entitlements`]: () => ({
      status: 200,
      body: {
        plan: [{ feature: 'coupons', unlimited: false, value: 1 }],
        overrides: [{ feature: 'coupons', unlimited: false, value: 0, reason: 'إساءة استخدام', active: true, updated_at: '' }],
        effective: [{ feature: 'coupons', unlimited: false, value: 0 }],
      },
    }),
    'GET /admin/v1/features': () => ({
      status: 200,
      body: {
        items: [
          { key: 'coupons', kind: 'flag', name_ar: 'الكوبونات', name_en: 'Coupons', sort: 70, enforced: true },
          { key: 'live_chat', kind: 'flag', name_ar: 'دردشة', name_en: 'Chat', sort: 900, enforced: false },
        ],
      },
    }),
    'GET /admin/v1/founding': () => ({ status: 200, body: { program: { enabled: true, seats: 100, discount_bps: 2000 }, members: [] } }),
    // The store's team card (C1).
    [`GET /admin/v1/stores/${store}/team`]: () => ({ status: 200, body: { members: [], invites: [], requests: [] } }),
    [`GET /admin/v1/stores/${store}/profile`]: () => ({
      status: 200,
      body: {
        owner: { name: 'منى أحمد', email: 'mona@example.com', email_verified: false, phone: '+201012345678', last_seen_at: '2026-11-02T10:00:00Z' },
        orders_30d: 37,
        last_order_at: '2026-11-03T08:00:00Z',
        products: 1200,
      },
    }),
    'GET /admin/v1/agencies': () => ({ status: 200, body: { items: [{ id: 'AG1', name: 'وكالة النيل', currency: 'EGP', plan_code: 'agency', stores: 12, created_at: '' }] } }),
    ...over,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('console', () => {
  it('signs in with the password and the authenticator code in one step', async () => {
    const calls = mockAPI({ 'POST /admin/v1/auth/login': () => ({ status: 200, body: { admin, expires_at: '' } }) });
    const session = renderConsole({ kind: 'signed-out' });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('البريد الإلكتروني'), 'ops@iftahonline.com');
    await user.type(screen.getByLabelText('كلمة المرور'), 'a long admin passphrase');
    await user.type(screen.getByLabelText('رمز تطبيق المصادقة'), '123456');
    await user.click(screen.getByRole('button', { name: 'دخول' }));
    expect(calls[0]?.body).toEqual({ email: 'ops@iftahonline.com', password: 'a long admin passphrase', code: '123456' });
    expect(session.signedIn).toHaveBeenCalledWith(admin);
  });

  it('shows the platform’s health, a section it could not read, and the stores', async () => {
    const calls = consoleRoutes();
    renderConsole({ kind: 'signed-in', admin });
    const systems = await screen.findByRole('region', { name: 'الأنظمة' });
    expect(within(systems).getByText('لا ترد')).toBeInTheDocument();
    const needs = screen.getByRole('region', { name: /يحتاج انتباهك/ });
    expect(within(needs).getByText('Redis لا يرد')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(within(systems).getByRole('button', { name: 'كل الأرقام' }));
    const subs = screen.getByRole('region', { name: 'الاشتراكات والفواتير' });
    expect(within(subs).getByText('58')).toHaveClass('warn');
    expect(screen.getByRole('link', { name: /متجر الأناقة/ })).toHaveAttribute('href', `/stores/${store}`);

    await user.click(screen.getByRole('link', { name: 'كل المتاجر ←' }));
    await user.type(await screen.findByRole('searchbox', { name: 'ابحث باسم المتجر أو عنوانه' }), 'alanaqa');
    await user.click(screen.getByRole('button', { name: 'بحث' }));
    await user.click(await screen.findByRole('tab', { name: /نشطة/ }));
    await waitFor(() => {
      const last = calls.filter((c) => c.path === '/admin/v1/stores').at(-1);
      expect(last?.search.get('query')).toBe('alanaqa');
      expect(last?.search.get('status')).toBe('active');
    });
    await user.click(await screen.findByRole('button', { name: 'عرض المزيد' }));
    expect(calls.filter((c) => c.path === '/admin/v1/stores').at(-1)?.search.get('cursor')).toBe('c1');
    expect(waited(12)).toBe('12 ث');
    expect(waited(7200)).toBe('2 س');
  });

  it('lists the payments to refund, records a refund, and shows nothing once none waits', async () => {
    vi.spyOn(window, 'prompt').mockReturnValueOnce('PMB-RF-77').mockReturnValueOnce('دُفعت مرتين');
    const dup = { id: 'DP1', store_id: store, invoice_number: 'INV-2026-000184', currency: 'EGP', amount_minor: 68286, gateway: 'paymob', reference: '9041', created_at: '2026-10-31T10:00:00Z' };
    const calls = consoleRoutes({
      'GET /admin/v1/billing/duplicates': () => ({ status: 200, body: { items: [dup, { ...dup, id: 'DP0', refunded_at: '2026-10-30T10:00:00Z', refund_reference: 'R0' }] } }),
      'POST /admin/v1/billing/duplicates/DP1/refunded': () => ({ status: 204 }),
    });
    renderConsole({ kind: 'signed-in', admin });
    const section = await screen.findByRole('region', { name: 'دفعات زائدة تحتاج استردادًا (1)' });
    expect(within(section).getByText('INV-2026-000184')).toBeInTheDocument();
    expect(within(section).getByText('682.86 ج.م')).toBeInTheDocument();
    expect(within(section).getByRole('link', { name: 'صفحة المتجر' })).toHaveAttribute('href', `/stores/${store}`);
    await userEvent.setup().click(within(section).getByRole('button', { name: 'تم الاسترداد' }));
    expect(calls.find((c) => c.path === '/admin/v1/billing/duplicates/DP1/refunded')?.body).toEqual({ reference: 'PMB-RF-77', reason: 'دُفعت مرتين' });
    await waitFor(() => {
      expect(screen.queryByRole('region', { name: /دفعات زائدة/ })).toBeNull();
    });
  });

  it('opens a support session, settles a transfer, sets and clears a flag, and suspends', async () => {
    const calls = consoleRoutes({
      [`POST /admin/v1/stores/${store}/impersonate`]: () => ({
        status: 201,
        body: { code: 'C', code_expires_at: '', expires_at: '2026-10-31T13:35:00Z', url: 'https://app.iftahonline.com/impersonate#code=C' },
      }),
      [`POST /admin/v1/stores/${store}/invoices/${invoice}/settle`]: () => ({ status: 200, body: {} }),
      [`PUT /admin/v1/stores/${store}/entitlements/coupons`]: () => ({ status: 200, body: {} }),
      [`POST /admin/v1/stores/${store}/entitlements/coupons/clear`]: () => ({ status: 200, body: {} }),
      [`POST /admin/v1/stores/${store}/suspend`]: () => ({ status: 200, body: {} }),
    });
    renderConsole({ kind: 'signed-in', admin }, `/stores/${store}`);
    const user = userEvent.setup();
    const body = (path: string) => calls.find((c) => c.method !== 'GET' && c.path === path)?.body;

    const support = await screen.findByRole('region', { name: 'جلسة دعم بحساب صاحب المتجر' });
    await user.type(within(support).getByLabelText('السبب'), 'تذكرة 4821');
    await user.click(within(support).getByRole('button', { name: 'افتح جلسة الدعم' }));
    expect(body(`/admin/v1/stores/${store}/impersonate`)).toEqual({ reason: 'تذكرة 4821', minutes: 30, write: false });
    expect(await screen.findByRole('link', { name: 'افتح جلسة الدعم في الداشبورد' })).toHaveAttribute('href', 'https://app.iftahonline.com/impersonate#code=C');

    const owner = screen.getByRole('region', { name: 'صاحب المتجر' });
    expect(within(owner).getByText('منى أحمد')).toBeInTheDocument();
    expect(within(owner).getByRole('link', { name: 'mona@example.com' })).toHaveAttribute('href', 'mailto:mona@example.com');
    expect(within(owner).getByText(/غير مؤكد/)).toBeInTheDocument();
    expect(within(owner).getByRole('link', { name: 'اتصال' })).toHaveAttribute('href', 'tel:+201012345678');
    expect(within(owner).getByRole('link', { name: 'واتساب' })).toHaveAttribute('href', 'https://wa.me/201012345678');
    const numbers = screen.getByRole('region', { name: 'أرقام المتجر' });
    expect(numbers).toHaveTextContent(/طلبات آخر 30 يومًا\s*37/);
    expect(numbers).toHaveTextContent(/المنتجات\s*1,200/);
    const history = screen.getByRole('region', { name: 'آخر إجراءات الأدمن على هذا المتجر' });
    expect(within(history).getByRole('link', { name: 'كل سجله ←' })).toHaveAttribute('href', `/audit?store=${store}`);
    expect(calls.find((c) => c.path === '/admin/v1/audit-log')?.search.get('store')).toBe(store);

    // The open invoice keeps the store out of agencies until it is settled.
    expect(screen.getByRole('button', { name: 'ضمّه للوكالة' })).toBeDisabled();
    const settle = screen.getByRole('form', { name: 'تسجيل INV-2026-000184 مدفوعة' });
    await user.type(within(settle).getByLabelText('رقم التحويل'), 'TRX-1');
    await user.type(within(settle).getByLabelText('السبب'), 'وصل التحويل');
    await user.click(within(settle).getByRole('button', { name: 'سجّل كمدفوعة' }));
    expect(body(`/admin/v1/stores/${store}/invoices/${invoice}/settle`)).toEqual({ reference: 'TRX-1', reason: 'وصل التحويل' });
    expect(await screen.findByText('سُجّلت الفاتورة مدفوعة.')).toBeInTheDocument();

    // Only enforced features are listed and offered; a flag is a switch.
    const flags = screen.getByRole('region', { name: 'مميزات المتجر' });
    expect(within(flags).queryByText('دردشة')).not.toBeInTheDocument();
    await user.click(within(flags).getByRole('button', { name: 'استثناء جديد' }));
    const add = screen.getByRole('dialog', { name: 'استثناء جديد لـ «متجر الأناقة»' });
    expect(within(add).queryByRole('option', { name: /دردشة/ })).toBeNull();
    expect(within(add).getByRole('switch', { name: 'متاحة لهذا المتجر' })).toBeChecked();
    await user.type(within(add).getByLabelText('حتى (اختياري)'), '2026-11-30');
    await user.type(within(add).getByLabelText('السبب'), 'شهر ترويجي');
    await user.click(within(add).getByRole('button', { name: 'حفظ الاستثناء' }));
    expect(body(`/admin/v1/stores/${store}/entitlements/coupons`)).toEqual({ reason: 'شهر ترويجي', unlimited: false, value: 1, expires_at: '2026-11-30T23:59:59.000Z' });
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });

    await user.click(within(flags).getByRole('button', { name: 'إزالة' }));
    const clear = screen.getByRole('dialog', { name: 'إزالة استثناء «الكوبونات»' });
    await user.type(within(clear).getByLabelText('السبب'), 'انتهت الإساءة');
    await user.click(within(clear).getByRole('button', { name: 'أزل الاستثناء' }));
    expect(body(`/admin/v1/stores/${store}/entitlements/coupons/clear`)).toEqual({ reason: 'انتهت الإساءة' });

    await user.click(screen.getByText('منطقة خطرة: إيقاف المتجر'));
    const suspend = screen.getByRole('form', { name: 'إيقاف المتجر' });
    await user.type(within(suspend).getByLabelText('السبب'), 'بلاغ احتيال');
    await user.click(within(suspend).getByRole('button', { name: 'أوقف «متجر الأناقة»' }));
    expect(body(`/admin/v1/stores/${store}/suspend`)).toEqual({ reason: 'بلاغ احتيال' });
  });

  it('reactivates a suspended store, and sets a limit with no limit', async () => {
    const calls = consoleRoutes({
      [`GET /admin/v1/stores/${store}`]: () => ({ status: 200, body: { store: { ...storeRow.store, status: 'suspended' }, domains: [] } }),
      [`GET /admin/v1/stores/${store}/entitlements`]: () => ({ status: 200, body: { plan: [{ feature: 'products', unlimited: false, value: 50 }], overrides: [], effective: [] } }),
      'GET /admin/v1/features': () => ({ status: 200, body: { items: [{ key: 'products', kind: 'count', name_ar: 'المنتجات', name_en: 'Products', sort: 30, enforced: true }] } }),
      [`PUT /admin/v1/stores/${store}/entitlements/products`]: () => ({ status: 200, body: {} }),
      [`POST /admin/v1/stores/${store}/reactivate`]: () => ({ status: 200, body: {} }),
      [`GET /admin/v1/stores/${store}/profile`]: () => ({ status: 200, body: { owner: { name: 'منى', email: 'm@x.com', email_verified: true }, orders_30d: 0, products: 0 } }),
    });
    renderConsole({ kind: 'signed-in', admin }, `/stores/${store}`);
    const user = userEvent.setup();
    const body = (path: string) => calls.find((c) => c.method !== 'GET' && c.path === path)?.body;

    // No phone, no sign-in, no order yet: each said so.
    const owner = await screen.findByRole('region', { name: 'صاحب المتجر' });
    expect(await within(owner).findByText('لم يضف رقمًا')).toBeInTheDocument();
    expect(within(owner).getByText('لم يدخل بعد')).toBeInTheDocument();
    expect(within(owner).queryByText(/غير مؤكد/)).toBeNull();
    expect(screen.getByRole('region', { name: 'أرقام المتجر' })).toHaveTextContent('لا طلبات بعد');

    await user.click(await screen.findByRole('button', { name: 'استثناء جديد' }));
    const add = screen.getByRole('dialog', { name: 'استثناء جديد لـ «متجر الأناقة»' });
    expect(within(add).getByRole('option', { name: 'المنتجات — من الباقة 50' })).toBeInTheDocument();
    await user.click(within(add).getByLabelText('بلا حد'));
    expect(within(add).getByLabelText('الحد الجديد')).toBeDisabled();
    await user.type(within(add).getByLabelText('السبب'), 'عميل كبير');
    await user.click(within(add).getByRole('button', { name: 'حفظ الاستثناء' }));
    expect(body(`/admin/v1/stores/${store}/entitlements/products`)).toEqual({ reason: 'عميل كبير', unlimited: true });

    await user.click(screen.getByText('إعادة تفعيل المتجر'));
    const back = screen.getByRole('form', { name: 'إعادة تفعيل المتجر' });
    await user.type(within(back).getByLabelText('السبب'), 'سُوّي البلاغ');
    await user.click(within(back).getByRole('button', { name: 'أعد تفعيل «متجر الأناقة»' }));
    expect(body(`/admin/v1/stores/${store}/reactivate`)).toEqual({ reason: 'سُوّي البلاغ' });
    expect(await screen.findByText('أُعيد تفعيل المتجر.')).toBeInTheDocument();
  });

  it("changes the store's country with a reason (ADR-0088)", async () => {
    const calls = consoleRoutes({
      [`GET /admin/v1/stores/${store}`]: () => ({ status: 200, body: { store: { ...storeRow.store, country: 'EG' }, domains: [] } }),
      [`PUT /admin/v1/stores/${store}/country`]: () => ({ status: 200, body: { ...storeRow.store, country: 'SA' } }),
    });
    renderConsole({ kind: 'signed-in', admin }, `/stores/${store}`);
    const user = userEvent.setup();

    await user.click(await screen.findByRole('button', { name: 'غيّر البلد' }));
    const dialog = screen.getByRole('dialog', { name: 'تغيير بلد المتجر' });
    const country = within(dialog).getByLabelText('البلد الجديد');
    // The store's own country is not offered.
    expect(within(country).queryByRole('option', { name: 'مصر' })).toBeNull();
    await user.selectOptions(country, 'SA');
    expect(within(dialog).getByRole('list', { name: 'ما لا يتغير' })).toHaveTextContent('حساب الفوترة المفتوح يحتفظ بعملته');
    await user.type(within(dialog).getByLabelText('السبب (إجباري، يُحفظ في سجل الأدمن)'), 'نقل نشاطه للرياض');
    await user.click(within(dialog).getByRole('button', { name: 'غيّر البلد' }));

    expect(await screen.findByText('غُيّر بلد المتجر.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PUT' && c.path === `/admin/v1/stores/${store}/country`)?.body).toEqual({
      country: 'SA',
      reason: 'نقل نشاطه للرياض',
    });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows a refused change', async () => {
    consoleRoutes({ [`POST /admin/v1/stores/${store}/impersonate`]: () => ({ status: 422, body: { title: 'Unprocessable', status: 422, detail: 'reason too short' } }) });
    renderConsole({ kind: 'signed-in', admin }, `/stores/${store}`);
    const user = userEvent.setup();
    const support = await screen.findByRole('region', { name: 'جلسة دعم بحساب صاحب المتجر' });
    await user.type(within(support).getByLabelText('السبب'), 'abc');
    await user.click(within(support).getByRole('button', { name: 'افتح جلسة الدعم' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('reason too short');
  });
});

describe('roles (ADR-0077)', () => {
  const as = (role: Parameters<typeof testAdmin>[1]): AdminState => ({ kind: 'signed-in', admin: testAdmin({}, role) });
  const nav = () => within(screen.getByRole('navigation', { name: 'قائمة الكونسول' }));

  it('shows each role only its sections, and says so on a page it may not open', async () => {
    const calls = consoleRoutes({ 'GET /admin/v1/metrics': () => ({ status: 200, body: { computed_at: '', errors: [] } }) });
    renderConsole(as('content'), '/');
    expect(await screen.findByRole('heading', { level: 1 })).toBeInTheDocument();
    expect(nav().getByRole('link', { name: 'الموقع التعريفي' })).toBeInTheDocument();
    for (const name of ['المتاجر', 'الفواتير والمدفوعات', 'الباقات والمميزات', 'سجل الأدمن']) {
      expect(nav().queryByRole('link', { name })).not.toBeInTheDocument();
    }
    // The home does not ask for what the role may not read.
    await waitFor(() => {
      expect(calls.some((c) => c.path === '/admin/v1/health')).toBe(true);
    });
    expect(calls.map((c) => c.path)).not.toEqual(expect.arrayContaining(['/admin/v1/billing/duplicates']));
    expect(calls.some((c) => ['/admin/v1/founding', '/admin/v1/stores'].includes(c.path))).toBe(false);
  });

  it('refuses a page to a role without it', async () => {
    consoleRoutes();
    renderConsole(as('viewer'), '/audit');
    expect(await screen.findByRole('heading', { name: 'لا تملك صلاحية هذه الصفحة' })).toBeInTheDocument();
    expect(nav().queryByRole('link', { name: 'سجل الأدمن' })).not.toBeInTheDocument();
  });

  it('shows finance the store’s billing actions but not its owner or support sessions', async () => {
    consoleRoutes();
    renderConsole(as('finance'), `/stores/${store}`);
    expect(await screen.findByRole('form', { name: 'تسجيل INV-2026-000184 مدفوعة' })).toBeInTheDocument();
    expect(screen.getByText('بيانات التواصل للمالك وفريق العمليات فقط.')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'جلسة دعم بحساب صاحب المتجر' })).not.toBeInTheDocument();
    expect(screen.queryByText('منطقة خطرة: إيقاف المتجر')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'استثناء جديد' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'ضمّه للوكالة' })).toBeInTheDocument();
  });

  it('shows operations the store’s actions but not its billing ones', async () => {
    consoleRoutes();
    renderConsole(as('operations'), `/stores/${store}`);
    expect(await screen.findByRole('region', { name: 'جلسة دعم بحساب صاحب المتجر' })).toBeInTheDocument();
    expect(await screen.findByText('mona@example.com')).toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'تسجيل INV-2026-000184 مدفوعة' })).not.toBeInTheDocument();
    expect(screen.getByText('ضمّ المتجر لوكالة للمالك والمالية.')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'آخر إجراءات الأدمن على هذا المتجر' })).not.toBeInTheDocument();
  });

  it('lets a viewer read an editor page with its form disabled', async () => {
    consoleRoutes();
    renderConsole(as('viewer'), '/founding');
    expect(await screen.findByRole('note')).toHaveTextContent('عرض فقط');
    const form = await screen.findByRole('form', { name: /البرنامج|التجار المؤسسون/ });
    for (const b of within(form).getAllByRole('button')) expect(b).toBeDisabled();
  });
});
