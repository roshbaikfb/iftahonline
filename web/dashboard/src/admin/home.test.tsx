import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { attention, greeting, initial, type AdminHealth } from './lib';
import { refreshEvery } from './Overview';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const admin = { id: '01M5ADMIN000000000000000AA', email: 'owner@iftahonline.com', name: 'أحمد' };
const store = '01M5STORE0000000000000000A';

// healthy is a platform where nothing needs the owner.
const stores = { active: 4812, held: 0, suspended: 6, closed: 141, total: 4959 };
const billing = { trialing: 512, active: 2904, past_due: 0, suspended: 0, cancelled: 1, invoices_open: 214, invoices_overdue: 0 };
const jobs = { available: 23, running: 8, retryable: 0, discarded_24h: 0, oldest_available_seconds: 12 };
const healthy: AdminHealth = { checked_at: '2026-11-03T09:00:00Z', database: true, redis: true, errors: [], stores, billing, jobs };
const storeRow = {
  store: { id: store, slug: 'alanaqa', name: 'متجر الأناقة', status: 'active', plan: 'pro', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo', created_at: '2026-10-28T10:00:00Z', updated_at: '' },
  primary_host: 'alanaqa.iftahonline.com',
};

function renderAt(path = '/') {
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
    'GET /admin/v1/health': () => ({ status: 200, body: healthy }),
    'GET /admin/v1/billing/duplicates': () => ({ status: 200, body: { items: [] } }),
    'GET /admin/v1/founding': () => ({ status: 200, body: { program: { enabled: true, seats: 100, discount_bps: 2000 }, members: Array.from({ length: 12 }, (_, i) => ({ store_id: `S${String(i)}` })) } }),
    'GET /admin/v1/stores': (c) => ({ status: 200, body: c.search.get('cursor') ? { items: [] } : { items: [storeRow], next_cursor: 'c1' } }),
    [`GET /admin/v1/stores/${store}`]: () => ({ status: 200, body: { store: storeRow.store, domains: [] } }),
    ...over,
  });
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('attention', () => {
  it('is empty when everything works', () => {
    expect(attention(healthy, 0)).toEqual([]);
  });

  it.each<[string, Partial<AdminHealth>, number, string, 'red' | 'amber']>([
    ['database', { database: false }, 0, 'قاعدة البيانات لا ترد', 'red'],
    ['redis', { redis: false }, 0, 'Redis لا يرد', 'red'],
    ['duplicates', {}, 2, 'دفعات زائدة (2)', 'red'],
    ['discarded', { jobs: { ...jobs, discarded_24h: 3 } }, 0, 'مهام فشلت نهائيًا (3)', 'red'],
    ['unread', { errors: ['billing', 'jobs'] }, 0, 'تعذّرت قراءة: الاشتراكات والفواتير، طابور المهام', 'amber'],
    ['held', { stores: { ...stores, held: 37 } }, 0, 'متاجر موقوفة لفاتورة (37)', 'amber'],
    ['overdue', { billing: { ...billing, invoices_overdue: 1200 } }, 0, 'فواتير متأخرة (1,200)', 'amber'],
    ['past_due', { billing: { ...billing, past_due: 58 } }, 0, 'اشتراكات متأخرة الدفع (58)', 'amber'],
    ['slow', { jobs: { ...jobs, oldest_available_seconds: 900 } }, 0, 'طابور المهام متأخر', 'amber'],
    ['retryable', { jobs: { ...jobs, retryable: 4 } }, 0, 'مهام تُعاد (4)', 'amber'],
  ])('raises %s', (key, change, duplicates, title, tone) => {
    const got = attention({ ...healthy, ...change }, duplicates);
    expect(got).toHaveLength(1);
    expect(got[0]).toMatchObject({ key, title, tone });
  });

  it('lists the urgent first, and does not repeat a down Redis as an unread section', () => {
    const got = attention({ ...healthy, redis: false, errors: ['redis'], billing: { ...billing, past_due: 2 }, jobs: { ...jobs, discarded_24h: 1 } }, 1);
    expect(got.map((a) => a.key)).toEqual(['redis', 'duplicates', 'discarded', 'past_due']);
    expect(got.find((a) => a.key === 'duplicates')?.link).toEqual({ href: '#h-duplicates', label: 'راجعها بالأسفل' });
  });

  it('greets by the hour and marks a store by its first letter', () => {
    expect(greeting(new Date(2026, 10, 3, 9))).toBe('صباح الخير');
    expect(greeting(new Date(2026, 10, 3, 15))).toBe('مساء الخير');
    expect(initial('  متجر الأناقة')).toBe('م');
  });
});

describe('console home', () => {
  it('says all is well, and shows the platform in four numbers and its newest stores', async () => {
    const calls = routes();
    renderAt();

    expect(await screen.findByText('كل شيء يعمل. لا شيء ينتظرك الآن.')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(/الخير، أحمد/);
    const kpis = screen.getByRole('region', { name: 'أرقام سريعة' });
    expect(kpis).toHaveTextContent(/المتاجر النشطة\s*4,812 من 4,959/);
    expect(kpis).toHaveTextContent('6 موقوف من المنصة');
    expect(kpis).toHaveTextContent(/في الفترة التجريبية\s*512/);
    expect(kpis).toHaveTextContent(/اشتراكات مدفوعة\s*2,904/);
    await waitFor(() => {
      expect(within(kpis).getByRole('img', { name: '12 من 100 مقعد' })).toBeInTheDocument();
    });

    const systems = screen.getByRole('region', { name: 'الأنظمة' });
    expect(systems).toHaveTextContent(/قاعدة البيانات\s*تعمل/);
    expect(systems).toHaveTextContent(/طابور المهام\s*23 تنتظر/);
    expect(screen.queryByRole('region', { name: 'المتاجر' })).toBeNull();

    const latest = screen.getByRole('region', { name: 'أحدث المتاجر' });
    expect(within(latest).getByRole('link', { name: /متجر الأناقة/ })).toHaveAttribute('href', `/stores/${store}`);
    expect(within(latest).getByRole('link', { name: 'كل المتاجر ←' })).toHaveAttribute('href', '/stores');
    expect(calls.find((c) => c.path === '/admin/v1/stores')?.search.get('limit')).toBe('5');
    expect(screen.queryByRole('region', { name: /دفعات زائدة/ })).toBeNull();
  });

  it('says when the founding program is off', async () => {
    routes({ 'GET /admin/v1/founding': () => ({ status: 200, body: { program: { enabled: false, seats: 100, discount_bps: 2000 }, members: [] } }) });
    renderAt();
    expect(await screen.findByText('البرنامج متوقف')).toBeInTheDocument();
  });

  it('reads the health again on «تحديث الآن» and every minute', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    routes({ 'GET /admin/v1/health': () => ({ status: 200, body: ++n > 1 ? { ...healthy, redis: false } : healthy }) });
    renderAt();
    await screen.findByText('كل شيء يعمل. لا شيء ينتظرك الآن.');

    await act(async () => {
      await vi.advanceTimersByTimeAsync(refreshEvery);
    });
    expect(await screen.findByText('Redis لا يرد')).toBeInTheDocument();
    expect(n).toBe(2);

    await userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) }).click(screen.getByRole('button', { name: 'تحديث الآن' }));
    await waitFor(() => {
      expect(n).toBe(3);
    });
  });
});

describe('console frame', () => {
  it('groups the screens and marks the stores for a store page', async () => {
    routes();
    renderAt(`/stores/${store}`);
    const nav = screen.getByRole('navigation', { name: 'قائمة الكونسول' });
    expect(within(nav).getByText('الإيراد')).toBeInTheDocument();
    expect(within(nav).getByRole('link', { name: 'المتاجر' })).toHaveAttribute('aria-current', 'page');
    expect(within(nav).getByRole('link', { name: 'الرئيسية' })).not.toHaveAttribute('aria-current');
    expect(within(nav).getByRole('link', { name: 'سجل الأدمن' })).toHaveAttribute('href', '/audit');
    expect(await screen.findByRole('link', { name: '→ المتاجر' })).toHaveAttribute('href', '/stores');
  });

  it('opens the menu as a drawer, and closes it on Escape, its button, or a link', async () => {
    routes();
    renderAt();
    const user = userEvent.setup();
    const toggle = screen.getByRole('button', { name: 'القائمة' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard('{Escape}');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);
    await user.click(screen.getByRole('button', { name: 'إغلاق القائمة' }));
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    await user.click(toggle);
    await user.click(screen.getByRole('link', { name: 'المتاجر' }));
    expect(screen.getByRole('button', { name: 'القائمة' })).toHaveAttribute('aria-expanded', 'false');
    expect(await screen.findByRole('heading', { level: 1, name: 'المتاجر' })).toBeInTheDocument();
  });
});

describe('stores page', () => {
  it('counts each status in its tab and reads the tab from the address', async () => {
    const calls = routes();
    renderAt('/stores?status=suspended');
    await screen.findByRole('link', { name: /متجر الأناقة/ });
    await screen.findByText('4,959');
    expect(screen.getByRole('tab', { name: 'موقوفة 6' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: 'الكل 4,959' })).toHaveAttribute('aria-selected', 'false');
    expect(calls.find((c) => c.path === '/admin/v1/stores')?.search.get('status')).toBe('suspended');
  });

  it('ignores an unknown status in the address', async () => {
    const calls = routes();
    renderAt('/stores?status=deleted');
    expect(await screen.findByRole('tab', { name: /الكل/ })).toHaveAttribute('aria-selected', 'true');
    await screen.findByRole('link', { name: /متجر الأناقة/ });
    expect(calls.find((c) => c.path === '/admin/v1/stores')?.search.has('status')).toBe(false);
  });

  it('says when no store matches the search', async () => {
    routes({ 'GET /admin/v1/stores': (c) => ({ status: 200, body: { items: c.search.get('query') ? [] : [storeRow] } }) });
    renderAt('/stores?q=zzz');
    expect(await screen.findByText('لا يوجد متجر بهذا الاسم أو العنوان.')).toBeInTheDocument();
    expect(screen.getByRole('searchbox')).toHaveValue('zzz');
  });

  it('shows why the directory could not be read', async () => {
    routes({ 'GET /admin/v1/stores': () => ({ status: 403, body: { title: 'Forbidden', status: 403, detail: 'not allowed' } }) });
    renderAt('/stores');
    expect(await screen.findByRole('alert')).toHaveTextContent('not allowed');
  });
});
