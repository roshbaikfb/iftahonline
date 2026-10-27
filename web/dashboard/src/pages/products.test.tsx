import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { priceLine, stockView, variantsLine, type Product } from '../lib/products';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Products } from './Products';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

type Variant = Product['variants'][number];

function variant(values: string[], price: number, available: number, extra: Partial<Variant> = {}): Variant {
  return {
    id: `v-${values.join('-')}`, option_values: values, price_minor: price, currency: 'EGP', position: 0,
    stock: { tracked: true, on_hand: available, reserved: 0, available }, ...extra,
  };
}

function product(id: string, title: string, extra: Partial<Product> = {}): Product {
  return {
    id, slug: id, title, description: '', status: 'active', options: [], variants: [variant([], 18000, 30)], category_ids: [], images: [],
    created_at: '', updated_at: '', ...extra,
  };
}

const tee = product('p1', 'تيشيرت قطن أساسي', {
  options: [
    { name: 'اللون', values: ['كحلي', 'أبيض', 'نبيتي'], colors: ['#1f2a44', '', ''] },
    { name: 'المقاس', values: ['S', 'M', 'L', 'XL'] },
  ],
  variants: ['كحلي', 'أبيض', 'نبيتي'].flatMap((c) => ['S', 'M', 'L', 'XL'].map((s) => variant([c, s], 25000, 7))),
  category_ids: ['c1'],
  images: [{ id: 'i1', status: 'ready', alt: '', position: 0, created_at: '', urls: { thumb: { avif: 'https://img/t.avif', webp: 'https://img/t.webp' }, small: { avif: '', webp: '' }, medium: { avif: '', webp: '' }, large: { avif: '', webp: '' } } }],
});
const jeans = product('p2', 'بنطلون جينز', {
  options: [{ name: 'المقاس', values: ['30', '32'] }],
  variants: [variant(['30'], 48000, 3), variant(['32'], 52000, 20)],
});
const shirt = product('p3', 'قميص كتان', { status: 'draft', variants: [variant([], 39000, 0, { compare_at_minor: 45000 })] });
const bag = product('p4', 'شنطة قماش', { sample: true, variants: [variant([], 18000, 0, { stock: { tracked: false, on_hand: 0, reserved: 0, available: 0 } })] });

function session(): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'u', email: 'a@b.c', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/products/counts`]: () => ({ status: 200, body: { all: 24, active: 19, draft: 3, archived: 2 } }),
    [`GET ${base}/categories`]: () => ({ status: 200, body: { items: [{ id: 'c1', name: 'تيشيرتات', slug: 't', position: 0, created_at: '', updated_at: '' }] } }),
    [`GET ${base}/storefront/theme`]: () => ({ status: 200, body: { settings: { low_stock: 5 }, version: 1 } }),
    [`GET ${base}/products`]: (c) =>
      c.search.get('limit') === '50'
        ? { status: 200, body: { items: [bag] } } // the samples banner
        : { status: 200, body: { items: [tee, jeans, shirt, bag], next_cursor: c.search.get('cursor') ? undefined : 'p4' } },
    ...over,
  });
}

function renderAt(path = `/stores/${store}/products`) {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/products" element={<Products />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const listCalls = (calls: Call[]) => calls.filter((c) => c.path === `${base}/products` && c.search.get('limit') === '20');

describe('Products', () => {
  it('shows the tabs, the samples banner and each product with its options, stock and price', async () => {
    routes();
    renderAt();
    expect(await screen.findByText('24 منتجًا')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'منشورة 19' })).toBeInTheDocument();
    expect(await screen.findByText('منتج تجريبي واحد من القالب')).toBeInTheDocument();

    const row = (name: string) => screen.getByRole('link', { name }).closest('tr') as HTMLElement;
    await screen.findByRole('link', { name: 'تيشيرت قطن أساسي' });
    expect(row('تيشيرت قطن أساسي')).toHaveTextContent('3 ألوان · 4 مقاسات · 12 نوعًا');
    expect(row('تيشيرت قطن أساسي')).toHaveTextContent('84 في 12 نوعًا');
    expect(row('تيشيرت قطن أساسي')).toHaveTextContent('تيشيرتات');
    expect(row('تيشيرت قطن أساسي').querySelector('img.thumb')).toHaveAttribute('src', 'https://img/t.webp');
    expect(row('بنطلون جينز')).toHaveTextContent('قارب على النفاد نوع واحد');
    expect(row('بنطلون جينز')).toHaveTextContent('480 – 520 ج.م');
    expect(row('قميص كتان')).toHaveTextContent('مسودة');
    expect(row('قميص كتان')).toHaveTextContent('0 نفد');
    expect(row('قميص كتان')).toHaveTextContent('390 ج.م 450');
    expect(row('شنطة قماش')).toHaveTextContent('تجريبي');
    expect(row('شنطة قماش')).toHaveTextContent('غير محدود');
    expect(screen.getByRole('link', { name: 'تيشيرت قطن أساسي' })).toHaveAttribute('href', `/stores/${store}/products/p1`);
    expect(screen.getByText('يعرض 1–4 من 24')).toBeInTheDocument();
  });

  it('filters by tab, category and stock, with the store’s low-stock threshold, and pages through', async () => {
    const calls = routes({ [`GET ${base}/storefront/theme`]: () => ({ status: 200, body: { settings: { low_stock: 8 }, version: 1 } }) });
    renderAt();
    const user = userEvent.setup();
    await screen.findByRole('link', { name: 'بنطلون جينز' });

    await user.click(screen.getByRole('tab', { name: 'مسودات 3' }));
    await waitFor(() => {
      expect(listCalls(calls).at(-1)?.search.get('status')).toBe('draft');
    });
    await user.selectOptions(screen.getByLabelText('التصنيف'), 'c1');
    await user.selectOptions(screen.getByLabelText('المخزون'), 'low');
    await user.click(screen.getByRole('button', { name: 'تصفية' }));
    await waitFor(() => {
      const q = listCalls(calls).at(-1)?.search;
      expect([q?.get('status'), q?.get('category_id'), q?.get('stock'), q?.get('low_threshold')]).toEqual(['draft', 'c1', 'low', '8']);
    });

    await user.click(await screen.findByRole('button', { name: 'الصفحة التالية' }));
    await waitFor(() => {
      expect(listCalls(calls).at(-1)?.search.get('cursor')).toBe('p4');
    });
    expect(await screen.findByText('يعرض 21–24')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'الصفحة السابقة' }));
    await waitFor(() => {
      expect(listCalls(calls).at(-1)?.search.get('cursor')).toBeNull();
    });
  });

  it('searches through the index, where stock cannot be filtered', async () => {
    const calls = routes({
      [`GET ${base}/products/search`]: () => ({
        status: 200,
        body: { items: [{ id: 'p9', title: 'تيشيرت أسود', slug: 'x', status: 'active', category_ids: [], price_minor: 15000, currency: 'EGP' }], estimated_total: 1 },
      }),
    });
    renderAt(`/stores/${store}/products?q=${encodeURIComponent('تيشيرت')}`);
    expect(await screen.findByRole('link', { name: 'تيشيرت أسود' })).toBeInTheDocument();
    expect(screen.getByText('من 150 ج.م')).toBeInTheDocument();
    expect(screen.getByLabelText('المخزون')).toBeDisabled();
    expect(screen.getByText('التصفية بالمخزون لا تعمل مع البحث.')).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith('/products/search'))?.search.get('q')).toBe('تيشيرت');
    expect(listCalls(calls)).toHaveLength(0);
  });

  it('archives and deletes the selected products in bulk', async () => {
    const calls = routes({ [`POST ${base}/products/bulk`]: (c) => ({ status: 200, body: { updated: (c.body as { product_ids: string[] }).product_ids.length } }) });
    renderAt();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('checkbox', { name: 'حدّد تيشيرت قطن أساسي' }));
    await user.click(screen.getByRole('checkbox', { name: 'حدّد بنطلون جينز' }));
    const bar = screen.getByRole('region', { name: 'إجراءات المحدد' });
    expect(within(bar).getByText('حدّدت منتجين')).toBeInTheDocument();
    await user.click(within(bar).getByRole('button', { name: 'أرشفة' }));
    expect(await screen.findByText('أُرشف: منتجان')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ product_ids: ['p1', 'p2'], action: 'archived' });
    await waitFor(() => {
      expect(screen.queryByRole('region', { name: 'إجراءات المحدد' })).toBeNull();
    });

    const confirm = vi.spyOn(globalThis, 'confirm').mockReturnValue(false);
    await user.click(screen.getByRole('checkbox', { name: 'حدّد الكل' }));
    await user.click(within(screen.getByRole('region', { name: 'إجراءات المحدد' })).getByRole('button', { name: 'حذف' }));
    expect(confirm).toHaveBeenCalled();
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1); // declined: nothing sent
  });

  it('invites the first product in an empty store', async () => {
    routes({
      [`GET ${base}/products`]: () => ({ status: 200, body: { items: [] } }),
      [`GET ${base}/products/counts`]: () => ({ status: 200, body: { all: 0, active: 0, draft: 0, archived: 0 } }),
    });
    renderAt();
    expect(await screen.findByText('لا منتجات في متجرك بعد.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'أضف أول منتج' })).toHaveAttribute('href', `/stores/${store}/products/new`);
  });
});

describe('product helpers', () => {
  it('sums options, stock and prices up the Arabic way', () => {
    expect(variantsLine(product('x', 'x'))).toBe('بلا خيارات');
    expect(variantsLine({ options: [{ name: 'Material', values: ['a', 'b'] }], variants: [] })).toBe('Material: 2');
    expect(variantsLine({ options: [{ name: 'اللون', values: ['a', 'b'] }], variants: [] })).toBe('لونان');
    expect(stockView(tee, 5)).toEqual({ units: 84, variants: 12, state: 'plenty', lowVariants: 0 });
    expect(stockView(tee, 7).state).toBe('low');
    expect(stockView(shirt, 5).state).toBe('out');
    expect(stockView(bag, 5)).toEqual({ variants: 1, state: 'untracked', lowVariants: 0 });
    expect(priceLine(jeans)).toEqual({ price: '480 – 520 ج.م' });
    expect(priceLine(shirt)).toEqual({ price: '390 ج.م', before: '450' });
  });
});
