import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { addLine, orderMessage, picks, soldOut, subtotal, type Pick } from '../lib/neworder';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { NewOrder } from './NewOrder';
import { Orders } from './Orders';

const store = '01M5STORE0000000000000000A';
const s = `/v1/stores/${store}`;

function session(role: string): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: 'u', email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' },
        memberships: [{ store_id: store, role: role as 'owner' }],
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const stock = (available: number, tracked = true) => ({ tracked, on_hand: available, reserved: 0, available });
const dress = {
  id: 'p1',
  slug: 'dress',
  title: 'فستان صيفي',
  description: '',
  status: 'active',
  options: [{ name: 'المقاس', values: ['M', 'L'] }],
  variants: [
    { id: 'v1', option_values: ['M'], price_minor: 45000, currency: 'EGP', position: 0, stock: stock(7) },
    { id: 'v2', option_values: ['L'], price_minor: 45000, currency: 'EGP', position: 1, stock: stock(0) },
    { id: 'v3', option_values: ['XL'], price_minor: 45000, currency: 'EGP', position: 2, stock: stock(3), hidden: true },
  ],
  category_ids: [],
  images: [],
  created_at: '',
  updated_at: '',
};
const draft = { ...dress, id: 'p2', title: 'مسودة', status: 'draft', variants: [{ ...dress.variants[0], id: 'v9' }] };

function api(extra: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${s}/shipping`]: () => ({
      status: 200,
      body: {
        currency: 'EGP',
        areas: [
          { code: 'EG-C', name_ar: 'القاهرة', name_en: 'Cairo' },
          { code: 'EG-ALX', name_ar: 'الإسكندرية', name_en: 'Alexandria' },
        ],
        settings: { cod_fee: { kind: 'none' }, version: 1 },
        zones: [],
      },
    }),
    [`GET ${s}/products/search`]: () => ({ status: 200, body: { items: [{ id: 'p1' }], estimated_total: 1 } }),
    [`GET ${s}/products/p1`]: () => ({ status: 200, body: dress }),
    [`POST ${s}/shipping/quote`]: (c) => ({
      status: 200,
      body:
        (c.body as { governorate: string }).governorate === 'EG-C'
          ? { deliverable: false, shipping_minor: 0, free_shipping: false, cod_fee_minor: 0, currency: 'EGP' }
          : { deliverable: true, shipping_minor: 6000, free_shipping: false, cod_fee_minor: 1000, currency: 'EGP' },
    }),
    [`GET ${s}/customers`]: () => ({ status: 200, body: { items: [{ id: 'c1', phone: '+201098765432', name: 'هدى سمير', orders_count: 3 }] } }),
    [`GET ${s}/orders`]: () => ({
      status: 200,
      body: { items: [{ id: 'o0', name: 'هدى', governorate: 'EG-ALX', city: 'سموحة', address: 'شارع فوزي معاذ' }] },
    }),
    ...extra,
  });
}

function renderAt(role = 'owner') {
  render(
    <SessionContext.Provider value={session(role)}>
      <MemoryRouter initialEntries={[`/stores/${store}/orders/new`]}>
        <Routes>
          <Route path="/stores/:storeId/orders/new" element={<NewOrder />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<p>صفحة الطلب</p>} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const sent = (calls: Call[], method: string, path: string) => calls.filter((c) => c.method === method && c.path === path);

async function addDress(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('أضف منتجًا'), 'فستان');
  const list = await screen.findByRole('listbox', { name: 'نتائج البحث' }, { timeout: 2000 });
  await user.click(within(list).getByRole('button', { name: /فستان صيفي · M/ }));
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('a phone order (D5)', () => {
  it('fills a known customer in, sums like checkout, and creates the order once', async () => {
    const calls = api({ [`POST ${s}/orders`]: () => ({ status: 201, body: { order: { id: 'o1' }, lines: [], events: [] } }) });
    renderAt();
    const user = userEvent.setup();
    expect(await screen.findByRole('option', { name: 'الإسكندرية' })).toBeInTheDocument();

    await user.type(screen.getByLabelText('الموبايل'), '01098765432');
    await user.tab();
    expect(await screen.findByText(/عميل سابق: 3 طلبات/)).toBeInTheDocument();
    expect(screen.getByLabelText('الاسم')).toHaveValue('هدى سمير');
    expect(screen.getByLabelText('المحافظة')).toHaveValue('EG-ALX');
    expect(screen.getByLabelText('المدينة أو المنطقة')).toHaveValue('سموحة');
    expect(screen.getByLabelText('العنوان')).toHaveValue('شارع فوزي معاذ');

    await addDress(user);
    const list = screen.getByRole('listbox', { name: 'نتائج البحث' });
    expect(within(list).getByRole('button', { name: /فستان صيفي · L/ })).toBeDisabled();
    expect(within(list).queryByText(/XL/)).toBeNull();
    await user.clear(screen.getByLabelText('كمية فستان صيفي · M'));
    await user.type(screen.getByLabelText('كمية فستان صيفي · M'), '2');

    const sum = screen.getByRole('complementary', { name: 'الملخص' });
    await waitFor(() => {
      expect(within(sum).getByText('الشحن (الإسكندرية)').nextSibling).toHaveTextContent('60 ج.م');
    });
    expect(within(sum).getByText('رسوم الدفع عند الاستلام').nextSibling).toHaveTextContent('10 ج.م');
    expect(within(sum).getByText('الإجمالي').nextSibling).toHaveTextContent('970 ج.م');

    await user.click(screen.getByRole('button', { name: 'أنشئ الطلب' }));
    expect(await screen.findByText('صفحة الطلب')).toBeInTheDocument();
    const posts = sent(calls, 'POST', `${s}/orders`);
    expect(posts).toHaveLength(1);
    expect(posts[0]?.body).toEqual({
      name: 'هدى سمير',
      phone: '01098765432',
      governorate: 'EG-ALX',
      city: 'سموحة',
      address: 'شارع فوزي معاذ',
      notes: '',
      lines: [{ variant_id: 'v1', qty: 2 }],
    });
  });

  it('keeps the key on a network failure and makes a new one after a refusal', async () => {
    const keys: (string | null)[] = [];
    let n = 0;
    api({
      [`POST ${s}/orders`]: () => {
        n++;
        return n === 1
          ? { status: 409, body: { title: 'x', status: 409, code: 'insufficient_stock' } }
          : { status: 201, body: { order: { id: 'o1' }, lines: [], events: [] } };
      },
    });
    // Wrap the routed mock: record each create's key, fail the first on the network.
    const routed = vi.mocked(globalThis.fetch).getMockImplementation();
    if (!routed) throw new Error('no routed fetch');
    let failOnce = true;
    vi.mocked(globalThis.fetch).mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = input instanceof Request ? input : new Request(input, init);
      if (req.method === 'POST' && new URL(req.url).pathname === `${s}/orders`) {
        keys.push(req.headers.get('Idempotency-Key'));
        if (failOnce) {
          failOnce = false;
          throw new TypeError('Failed to fetch');
        }
      }
      return routed(req);
    });
    renderAt();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('الموبايل'), '01012345678');
    await user.type(screen.getByLabelText('الاسم'), 'سارة');
    await user.selectOptions(await screen.findByLabelText('المحافظة'), 'EG-ALX');
    await user.type(screen.getByLabelText('المدينة أو المنطقة'), 'سموحة');
    await user.type(screen.getByLabelText('العنوان'), 'شارع 1');
    await addDress(user);

    const create = screen.getByRole('button', { name: 'أنشئ الطلب' });
    await user.click(create); // the network fails
    await waitFor(() => {
      expect(keys).toHaveLength(1);
    });
    await user.click(create); // refused: insufficient stock
    expect(await screen.findByText('الكمية المطلوبة أكبر من المتاح في المخزون.')).toBeInTheDocument();
    await user.click(create); // created
    expect(await screen.findByText('صفحة الطلب')).toBeInTheDocument();
    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[1]);
  });

  it('says a governorate without delivery and does not create', async () => {
    api();
    renderAt();
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByLabelText('المحافظة'), 'EG-C');
    expect(await screen.findByText('لا توصيل لهذه المحافظة في إعدادات الشحن.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'أنشئ الطلب' })).toBeDisabled();
  });

  it('asks for a product before sending', async () => {
    const calls = api();
    renderAt();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('الموبايل'), '01012345678');
    await user.type(screen.getByLabelText('الاسم'), 'سارة');
    await user.selectOptions(await screen.findByLabelText('المحافظة'), 'EG-ALX');
    await user.type(screen.getByLabelText('المدينة أو المنطقة'), 'سموحة');
    await user.type(screen.getByLabelText('العنوان'), 'شارع 1');
    await user.click(screen.getByRole('button', { name: 'أنشئ الطلب' }));
    expect(screen.getByRole('alert')).toHaveTextContent('أضف منتجًا واحدًا على الأقل.');
    expect(sent(calls, 'POST', `${s}/orders`)).toHaveLength(0);
  });

  it('is only for roles that manage orders', async () => {
    api();
    renderAt('shipping');
    expect(await screen.findByText('إنشاء الطلبات لصاحب المتجر والمدير وخدمة العملاء.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'أنشئ الطلب' })).toBeNull();
  });

  it.each([
    ['owner', true],
    ['support', true],
    ['shipping', false],
  ])('shows «طلب جديد» on the orders page to %s: %s', async (role, shown) => {
    mockAPI({});
    render(
      <SessionContext.Provider value={session(role)}>
        <MemoryRouter initialEntries={[`/stores/${store}/orders`]}>
          <Routes>
            <Route path="/stores/:storeId/orders" element={<Orders />} />
          </Routes>
        </MemoryRouter>
      </SessionContext.Provider>,
    );
    await screen.findByRole('heading', { name: 'الطلبات' });
    const link = screen.queryByRole('link', { name: 'طلب جديد' });
    if (shown) expect(link).toHaveAttribute('href', `/stores/${store}/orders/new`);
    else expect(link).toBeNull();
  });
});

describe('phone order helpers (D5)', () => {
  it('lists only what can be sold, and names variants', () => {
    const got = picks([dress, draft] as Parameters<typeof picks>[0]);
    expect(got.map((p) => p.name)).toEqual(['فستان صيفي · M', 'فستان صيفي · L']);
    expect(got.map(soldOut)).toEqual([false, true]);
    const untracked = picks([{ ...dress, variants: [{ ...dress.variants[0], option_values: [], stock: stock(0, false) }] }] as Parameters<typeof picks>[0]);
    expect(untracked[0]).toMatchObject({ name: 'فستان صيفي', available: null });
    const [first] = untracked;
    expect(first && soldOut(first)).toBe(false);
  });

  it('adds a line once and counts it up', () => {
    const p: Pick = { variantId: 'v1', name: 'x', priceMinor: 1000, currency: 'EGP', available: 5 };
    const lines = addLine(addLine([], p), p);
    expect(lines).toEqual([{ ...p, qty: 2 }]);
    expect(subtotal(lines)).toBe(2000);
  });

  it.each([
    [{ code: 'customer_blocked' }, 'هذا العميل محظور في متجرك، فلا يُنشأ له طلب.'],
    [{ code: 'validation_failed', errors: [{ field: 'coupon_code', code: 'not_found' }] }, 'هذا الكوبون غير موجود.'],
    [{ code: 'validation_failed', errors: [{ field: 'coupon_code', code: 'expired' }] }, 'هذا الكوبون لا ينطبق على هذا الطلب.'],
    [{ code: 'validation_failed', errors: [{ field: 'governorate', code: 'not_delivered' }] }, 'لا توصيل لهذه المحافظة في إعدادات الشحن.'],
    [
      {
        code: 'validation_failed',
        errors: [
          { field: 'lines[0].variant_id', code: 'unavailable' },
          { field: 'lines[1].variant_id', code: 'unavailable' },
        ],
      },
      'منتج في الطلب لم يعد متاحًا للبيع.',
    ],
    [{ code: 'validation_failed', errors: [{ field: 'phone', code: 'invalid' }] }, 'اكتب رقم موبايل صحيحًا.'],
  ])('says %j in Arabic', (problem, want) => {
    expect(orderMessage(problem)).toBe(want);
  });
});
