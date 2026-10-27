import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { flat, parentChoices, tree } from '../lib/categories';
import type { Category, Product } from '../lib/products';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Categories } from './Categories';
import { Imports } from './Imports';
import { Inventory } from './Inventory';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

function cat(id: string, name: string, parent?: string, position = 0): Category {
  const c: Category = { id, name, slug: id, position, created_at: '', updated_at: '' };
  if (parent) c.parent_id = parent;
  return c;
}

const cats = [cat('men', 'رجالي', undefined, 1), cat('women', 'حريمي', undefined, 0), cat('tees', 'تيشيرتات', 'men'), cat('polo', 'بولو', 'tees')];

function session(): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'u', email: 'a@b.c', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/categories`]: () => ({ status: 200, body: { items: cats } }),
    [`GET ${base}/storefront/theme`]: () => ({ status: 200, body: { settings: { low_stock: 5 }, version: 1 } }),
    ...over,
  });
}

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/categories" element={<Categories />} />
          <Route path="/stores/:storeId/inventory" element={<Inventory />} />
          <Route path="/stores/:storeId/imports" element={<Imports />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

describe('Categories', () => {
  it('shows the tree by position, adds, moves and deletes', async () => {
    const calls = routes({
      [`POST ${base}/categories`]: (c) => ({ status: 201, body: { ...cat('new', ''), ...(c.body as object) } }),
      [`PUT ${base}/categories/tees`]: (c) => ({ status: 200, body: { ...cat('tees', ''), ...(c.body as object) } }),
      [`DELETE ${base}/categories/polo`]: () => ({ status: 204 }),
    });
    renderAt(`/stores/${store}/categories`);
    const user = userEvent.setup();
    const tree = await screen.findByRole('region', { name: 'شجرة التصنيفات' });
    await within(tree).findByText('بولو');
    expect(within(tree).getAllByRole('link').map((a) => a.textContent)).toEqual(['حريمي', 'رجالي', 'تيشيرتات', 'بولو']);
    expect(within(tree).getByRole('link', { name: 'تيشيرتات' })).toHaveAttribute('href', `/stores/${store}/products?category=tees`);

    // a parent with children is not deleted: the merchant is told why
    await user.click(within(tree).getByRole('button', { name: 'احذف رجالي' }));
    expect(await screen.findByText('انقل التصنيفات الفرعية تحت «رجالي» أو احذفها أولًا.')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);

    await user.click(screen.getByRole('button', { name: 'أضف تصنيفًا' }));
    const form = screen.getByRole('form', { name: 'تصنيف جديد' });
    await user.type(within(form).getByLabelText('الاسم'), 'أطفال');
    await user.click(within(form).getByRole('button', { name: 'حفظ' }));
    expect(await screen.findByText('أُضيف «أطفال».')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ name: 'أطفال', position: 0 });

    // move «تيشيرتات» under «حريمي»: its subtree (two levels) fits, but not under itself or deeper
    const third = within(tree).getAllByRole('button', { name: 'تعديل' }).at(2);
    if (!third) throw new Error('no third category');
    await user.click(third);
    const edit = screen.getByRole('form', { name: 'تعديل تيشيرتات' });
    const parents = within(edit).getAllByRole('option').map((o) => o.textContent);
    expect(parents).toEqual(['— تصنيف رئيسي —', 'حريمي', 'رجالي']);
    await user.selectOptions(within(edit).getByLabelText('تحت'), 'women');
    await user.click(within(edit).getByRole('button', { name: 'حفظ' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ name: 'تيشيرتات', position: 0, parent_id: 'women' });
    });

    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    await user.click(within(tree).getByRole('button', { name: 'احذف بولو' }));
    expect(await screen.findByText('حُذف «بولو».')).toBeInTheDocument();
  });

  it('builds the tree and the places a category may move to', () => {
    const nodes = tree(cats);
    expect(flat(nodes).map((n) => [n.c.id, n.depth])).toEqual([['women', 1], ['men', 1], ['tees', 2], ['polo', 3]]);
    expect(parentChoices(nodes, 'men').map((n) => n.c.id)).toEqual([]); // three levels already: only the top
    expect(parentChoices(nodes, '').map((n) => n.c.id)).toEqual(['women', 'men', 'tees']);
    expect(parentChoices(nodes, 'polo').map((n) => n.c.id)).toEqual(['women', 'men', 'tees']);
  });
});

type Variant = Product['variants'][number];

function v(id: string, values: string[], onHand: number, reserved = 0, extra: Partial<Variant> = {}): Variant {
  return { id, option_values: values, price_minor: 100, currency: 'EGP', position: 0, stock: { tracked: true, on_hand: onHand, reserved, available: onHand - reserved }, ...extra };
}

const tee: Product = {
  id: 'p1', slug: 'tee', title: 'تيشيرت', description: '', status: 'active', options: [{ name: 'المقاس', values: ['S', 'M', 'L'] }], category_ids: [], images: [],
  variants: [v('v1', ['S'], 3, 1), v('v2', ['M'], 40), v('v3', ['L'], 0, 0, { hidden: true, sku: 'TEE-L' })],
  created_at: '', updated_at: '',
};

describe('Inventory', () => {
  it('starts with what runs low, saves a new count, and switches to what sold out', async () => {
    const calls = routes({
      [`GET ${base}/products`]: () => ({ status: 200, body: { items: [tee] } }),
      [`PUT ${base}/variants/v1/inventory`]: () => ({ status: 200, body: { tracked: true, on_hand: 20, reserved: 1, available: 19 } }),
    });
    renderAt(`/stores/${store}/inventory`);
    const user = userEvent.setup();
    const field = await screen.findByLabelText('عدد جديد في المخزن لـ تيشيرت — S');
    expect(screen.queryByLabelText('عدد جديد في المخزن لـ تيشيرت — M')).toBeNull(); // plenty: not running low
    expect(calls.find((c) => c.path === `${base}/products`)?.search.get('stock')).toBe('low');
    expect(field.closest('tr')).toHaveTextContent('312'); // on hand 3, held 1, free 2

    await user.type(field, '٢٠');
    await user.click(within(field.closest('tr') as HTMLElement).getByRole('button', { name: 'حفظ' }));
    expect(await screen.findByText('حُفظ')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ on_hand: 20, tracked: true });
    expect(field.closest('tr')).toHaveTextContent('20119');

    await user.click(screen.getByRole('tab', { name: 'نفد' }));
    expect(await screen.findByLabelText('عدد جديد في المخزن لـ تيشيرت — L')).toBeInTheDocument();
    expect(screen.getByText('مخفي')).toBeInTheDocument();
    expect(calls.filter((c) => c.path === `${base}/products`).at(-1)?.search.get('stock')).toBe('out');
  });
});

describe('Imports', () => {
  it('uploads a CSV, starts it, follows its progress and shows the skipped rows', async () => {
    let state = 0;
    const imp = {
      id: 'i1', kind: 'csv', status: 'running', total: 10, processed: 4, created: 3, updated: 1, failed: 0, row_errors: [], created_at: '2026-10-27T10:00:00Z',
    };
    const fetchSpy = routes({
      [`GET ${base}/imports`]: () => ({
        status: 200,
        body: { items: state === 0 ? [] : state === 1 ? [imp] : [{ ...imp, status: 'succeeded', processed: 10, created: 8, failed: 1, row_errors: [{ line: 7, message: 'line 7: price: not an amount' }] }] },
      }),
      [`POST ${base}/imports`]: () => ({ status: 201, body: { import: { ...imp, status: 'pending' }, upload: { url: 'https://s3.test/bucket', fields: { key: 'k' }, expires_at: '', max_bytes: 1 } } }),
      [`POST ${base}/imports/i1/start`]: () => {
        state = 1;
        return { status: 202, body: imp };
      },
      'POST /bucket': () => ({ status: 204 }),
    });
    renderAt(`/stores/${store}/imports`);
    const user = userEvent.setup();
    expect(await screen.findByText('لم تستورد شيئًا بعد.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'الملف النموذجي' })).toHaveAttribute('href', '/products-template.csv');

    const file = new File(['handle,title\n'], 'products.csv', { type: 'text/csv' });
    await user.upload(screen.getByLabelText('ارفع ملف CSV'), file);
    expect(await screen.findByRole('progressbar', { name: 'التقدم' })).toHaveAttribute('aria-valuenow', '40');
    expect(fetchSpy.find((c) => c.method === 'POST' && c.path === `${base}/imports`)?.body).toEqual({ kind: 'csv' });

    state = 2;
    expect(await screen.findByText('اكتمل', {}, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.getByText(/أُضيف 8 · حُدّث 1 · تخطّينا 1/)).toBeInTheDocument();
    expect(screen.getByText('line 7: price: not an amount')).toBeInTheDocument();
  });

  it('refuses a file that is not CSV, and connects WooCommerce', async () => {
    const calls = routes({
      [`GET ${base}/imports`]: () => ({ status: 200, body: { items: [] } }),
      [`POST ${base}/imports`]: () => ({ status: 422, body: { title: 'Unprocessable', status: 422, errors: [{ field: 'woocommerce.url', code: 'invalid', message: 'must be https' }] } }),
    });
    renderAt(`/stores/${store}/imports`);
    const user = userEvent.setup({ applyAccept: false });
    await user.upload(await screen.findByLabelText('ارفع ملف CSV'), new File(['x'], 'products.xlsx'));
    expect(screen.getByText('الملف يكون CSV. من Excel: «حفظ باسم» ثم «CSV UTF-8».')).toBeInTheDocument();

    await user.click(screen.getByRole('tab', { name: 'متجر WooCommerce' }));
    await user.type(screen.getByLabelText('رابط المتجر'), 'https://shop.example.com');
    await user.type(screen.getByLabelText('Consumer key'), 'ck_1');
    await user.type(screen.getByLabelText('Consumer secret'), 'cs_1');
    await user.click(screen.getByRole('button', { name: 'ابدأ الاستيراد' }));
    expect(await screen.findByText(/must be https/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      kind: 'woocommerce', woocommerce: { url: 'https://shop.example.com', consumer_key: 'ck_1', consumer_secret: 'cs_1' },
    });
  });
});
