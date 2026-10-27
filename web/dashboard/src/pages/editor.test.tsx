import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { build, combinations, draftOf, emptyDraft, guessColour, isColourName, newOption, normalizeHex, regenerate, type OptionDraft } from '../lib/editor';
import type { Product } from '../lib/products';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { ProductEditor } from './ProductEditor';
import { SizeCharts } from './SizeCharts';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

type Variant = Product['variants'][number];

function variant(id: string, values: string[], onHand: number, extra: Partial<Variant> = {}): Variant {
  return { id, option_values: values, price_minor: 25000, currency: 'EGP', position: 0, stock: { tracked: true, on_hand: onHand, reserved: 0, available: onHand }, ...extra };
}

const tee: Product = {
  id: 'p1', slug: 'tee', title: 'تيشيرت قطن', description: 'قطن مصري', status: 'active', category_ids: ['c1'], images: [], size_chart_id: 'sc1',
  options: [
    { name: 'اللون', values: ['كحلي', 'أبيض'], colors: ['#1f2a44', ''] },
    { name: 'المقاس', values: ['S', 'M'] },
  ],
  variants: [variant('v1', ['كحلي', 'S'], 8), variant('v2', ['كحلي', 'M'], 12), variant('v3', ['أبيض', 'S'], 5), variant('v4', ['أبيض', 'M'], 0, { hidden: true })],
  created_at: '', updated_at: '',
};

const chart = { id: 'sc1', name: 'تيشيرتات', header: ['المقاس', 'الصدر'], body: [['S', '96'], ['M', '102']], products: 6, created_at: '', updated_at: '' };

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
    [`GET ${base}/categories`]: () => ({ status: 200, body: { items: [{ id: 'c1', name: 'تيشيرتات', slug: 't', position: 0, created_at: '', updated_at: '' }] } }),
    [`GET ${base}/size-charts`]: () => ({ status: 200, body: { items: [chart] } }),
    [`GET ${base}/products/p1`]: () => ({ status: 200, body: tee }),
    ...over,
  });
}

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/products/new" element={<ProductEditor />} />
          <Route path="/stores/:storeId/products/:productId" element={<ProductEditor />} />
          <Route path="/stores/:storeId/size-charts" element={<SizeCharts />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

describe('ProductEditor', () => {
  it('creates a product: options make the variants, colours guess from names, a new category is made on the way', async () => {
    const calls = routes({
      [`POST ${base}/categories`]: () => ({ status: 201, body: { id: 'c2', name: 'وصل حديثًا', slug: 'n', position: 0, created_at: '', updated_at: '' } }),
      [`POST ${base}/products`]: (c) => ({ status: 201, body: { ...tee, id: 'p9', ...(c.body as object), variants: [] } }),
      [`GET ${base}/products/p9`]: () => ({ status: 200, body: { ...tee, id: 'p9' } }),
    });
    renderAt(`/stores/${store}/products/new`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 1, name: 'منتج جديد' })).toBeInTheDocument();
    expect(screen.getByText('احفظ المنتج أولًا، ثم أضف صوره.')).toBeInTheDocument();

    // saving with nothing filled says what is missing
    await user.click(screen.getByRole('button', { name: 'حفظ المنتج' }));
    expect(screen.getByText('اكتب اسم المنتج')).toBeInTheDocument();
    expect(screen.getByText('اكتب السعر')).toBeInTheDocument();

    await user.type(screen.getByLabelText('اسم المنتج'), 'بولو');
    await user.type(screen.getByLabelText('سعر المنتج'), '320');
    await user.click(screen.getByRole('button', { name: '+ أضف خيارًا' }));
    await user.type(screen.getByLabelText('اسم الخيار'), 'اللون');
    expect(screen.getByLabelText('خيار ألوان (يظهر كدوائر في المتجر)')).toBeChecked(); // the name reads as a colour
    await user.type(screen.getByLabelText('قيمة جديدة لـ اللون'), 'كحلي{Enter}أخضر زيتوني{Enter}');
    expect(screen.getByRole('button', { name: 'لون كحلي' })).toHaveStyle({ background: 'rgb(30, 58, 95)' });
    // an unknown name: the merchant picks the colour
    await user.click(screen.getByRole('button', { name: 'لون أخضر زيتوني' }));
    const picker = screen.getByRole('dialog', { name: 'لون «أخضر زيتوني»' });
    expect(within(picker).getByText('لا نعرف هذا اللون من اسمه؛ اختر لونه.')).toBeInTheDocument();
    await user.clear(within(picker).getByLabelText('الكود'));
    await user.type(within(picker).getByLabelText('الكود'), '#556B2F');
    expect(screen.getByLabelText('سعر كحلي')).toHaveValue('320'); // the new rows took the first row's price

    await user.type(screen.getByLabelText('أضف تصنيفًا'), 'وصل حديثًا{Enter}');
    await user.selectOptions(screen.getByRole('combobox', { name: 'جدول المقاسات' }), 'sc1');
    expect(screen.getByText('مستخدم في 6 منتجات. تعديله يغيّرها كلها.')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'حفظ المنتج' }));

    await waitFor(() => {
      expect(calls.some((c) => c.method === 'POST' && c.path === `${base}/products`)).toBe(true);
    });
    expect(calls.find((c) => c.method === 'POST' && c.path === `${base}/categories`)?.body).toEqual({ name: 'وصل حديثًا', position: 0 });
    const body = calls.find((c) => c.method === 'POST' && c.path === `${base}/products`)?.body as Record<string, unknown>;
    expect(body).toMatchObject({
      title: 'بولو', status: 'draft', category_ids: ['c2'], size_chart_id: 'sc1',
      options: [{ name: 'اللون', values: ['كحلي', 'أخضر زيتوني'], colors: ['', '#556b2f'] }],
      variants: [
        { option_values: ['كحلي'], price_minor: 32000, hidden: false, tracked: true, on_hand: 0 },
        { option_values: ['أخضر زيتوني'], price_minor: 32000, hidden: false, tracked: true, on_hand: 0 },
      ],
    });
  });

  it('edits a product: keeps variant IDs, hides one without losing its stock, and sets changed stock apart', async () => {
    const calls = routes({
      [`PUT ${base}/products/p1`]: () => ({ status: 200, body: tee }),
      [`PUT ${base}/variants/v1/inventory`]: () => ({ status: 200, body: { tracked: true, on_hand: 20, reserved: 0, available: 20 } }),
    });
    renderAt(`/stores/${store}/products/p1`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 1, name: 'تيشيرت قطن' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'عرض في المتجر' })).toHaveAttribute('href', 'http://mona.iftahonline.localhost:18081/p/tee');

    // grouped by colour, the first group open
    expect(screen.getByRole('button', { name: /كحلي · نوعان · 20 قطعة/ })).toHaveAttribute('aria-expanded', 'true');
    await user.click(screen.getByRole('button', { name: /أبيض · نوعان/ }));
    expect(screen.getByLabelText('أبيض / M متاح للبيع')).not.toBeChecked(); // hidden on the server

    await user.click(screen.getByLabelText('كحلي / M متاح للبيع'));
    await user.clear(screen.getByLabelText('مخزون كحلي / S'));
    await user.type(screen.getByLabelText('مخزون كحلي / S'), '20');
    await user.click(screen.getByRole('button', { name: 'حفظ التغييرات' }));

    expect(await screen.findByText('حُفظ المنتج.')).toBeInTheDocument();
    const body = calls.find((c) => c.method === 'PUT' && c.path === `${base}/products/p1`)?.body as { variants: { id: string; hidden: boolean }[] };
    expect(body.variants.map((v) => [v.id, v.hidden])).toEqual([['v1', false], ['v2', true], ['v3', false], ['v4', true]]);
    expect(calls.filter((c) => c.path.endsWith('/inventory')).map((c) => [c.path, c.body])).toEqual([[`${base}/variants/v1/inventory`, { on_hand: 20, tracked: true }]]);
  });

  it('asks before deleting variants that hold stock', async () => {
    const calls = routes({ [`PUT ${base}/products/p1`]: () => ({ status: 200, body: tee }) });
    renderAt(`/stores/${store}/products/p1`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'احذف S' }));
    const confirm = vi.spyOn(globalThis, 'confirm').mockReturnValue(false);
    await user.click(screen.getByRole('button', { name: 'حفظ التغييرات' }));
    expect(confirm).toHaveBeenCalledWith(expect.stringContaining('سيُحذف نوعان (كحلي / S، أبيض / S) ومعه مخزون 13 قطعة'));
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
  });

  it('says what the server refused', async () => {
    routes({
      [`PUT ${base}/products/p1`]: () => ({
        status: 422,
        body: { title: 'Unprocessable Entity', status: 422, errors: [{ field: 'variants[0].sku', code: 'duplicate', message: 'SKUs must be unique' }] },
      }),
    });
    renderAt(`/stores/${store}/products/p1`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'حفظ التغييرات' }));
    expect(await screen.findByText(/SKUs must be unique/)).toBeInTheDocument();
  });
});

describe('SizeCharts', () => {
  it('edits a chart, previews it as shoppers see it, and creates and deletes one', async () => {
    const calls = routes({
      [`PUT ${base}/size-charts/sc1`]: (c) => ({ status: 200, body: { ...chart, ...(c.body as object) } }),
      [`POST ${base}/size-charts`]: (c) => ({ status: 201, body: { ...chart, id: 'sc2', products: 0, ...(c.body as object) } }),
      [`DELETE ${base}/size-charts/sc1`]: () => ({ status: 204 }),
    });
    renderAt(`/stores/${store}/size-charts?id=sc1`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 2, name: 'تيشيرتات' })).toBeInTheDocument();
    expect(screen.getByText('مستخدم في 6 منتجات.')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '+ عمود' }));
    await user.type(screen.getByLabelText('اسم العمود 3'), 'الطول');
    await user.type(screen.getByLabelText('الطول S'), '68');
    await user.click(screen.getByRole('button', { name: '+ صف' }));
    await user.type(screen.getByLabelText('المقاس، صف 3'), 'L');
    const preview = screen.getByRole('region', { name: 'كيف يظهر في صفحة المنتج' });
    expect(within(preview).getByRole('rowheader', { name: 'L' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'حفظ' }));
    expect(await screen.findByText('حُفظ الجدول.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({
      name: 'تيشيرتات', header: ['المقاس', 'الصدر', 'الطول'], body: [['S', '96', '68'], ['M', '102', ''], ['L', '', '']],
    });

    const confirm = vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    await user.click(screen.getByRole('button', { name: 'حذف الجدول' }));
    expect(confirm).toHaveBeenCalledWith('حذف جدول «تيشيرتات»؟ سيختفي من 6 منتجات.');
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
    });

    await user.click(screen.getByRole('button', { name: '+ جدول جديد' }));
    await user.type(screen.getByLabelText('اسم الجدول (لك فقط)'), 'بناطيل');
    await user.click(screen.getByRole('button', { name: 'حفظ' }));
    await waitFor(() => {
      expect((calls.find((c) => c.method === 'POST')?.body as { name: string }).name).toBe('بناطيل');
    });
  });
});

function row<T>(list: T[], i: number): T {
  const r = list[i];
  if (r === undefined) throw new Error(`no row ${String(i)}`);
  return r;
}

describe('editor helpers', () => {
  const opts = (...o: [string, string[]][]): OptionDraft[] => o.map(([name, values]) => ({ ...newOption(), name, values }));

  it('makes one variant per combination and keeps the ones still there', () => {
    expect(combinations(opts(['a', ['1', '2']], ['b', ['x', 'y']]))).toEqual([['1', 'x'], ['1', 'y'], ['2', 'x'], ['2', 'y']]);
    const d = draftOf(tee);
    const [colour, size] = d.options as [OptionDraft, OptionDraft];
    const next = regenerate(d.options, [{ ...colour, values: [...colour.values, 'نبيتي'] }, size], d.variants);
    expect(next).toHaveLength(6);
    expect(next.slice(0, 4).map((v) => v.id)).toEqual(['v1', 'v2', 'v3', 'v4']);
    expect(next[4]).toMatchObject({ values: ['نبيتي', 'S'], price: '250', stock: '0', hidden: false });
    expect(next[4]?.id).toBeUndefined();
    // no options: the one variant stays the same row
    const single = emptyDraft().variants;
    expect(regenerate([], [], single)).toEqual(single);
  });

  it('keeps every variant and its stock when an option is added, renamed or another removed', () => {
    const d = draftOf(tee);
    const [colour, size] = d.options as [OptionDraft, OptionDraft];
    // by size only, then a colour added: each size becomes the first colour
    const bySize = regenerate(d.options, [size], d.variants);
    expect(bySize.map((v) => [v.id, v.values])).toEqual([['v1', ['S']], ['v2', ['M']]]);
    const fabric = { ...newOption(), name: 'الخامة', values: ['قطن', 'كتان'] };
    const added = regenerate([size], [size, fabric], bySize);
    expect(added.map((v) => [v.id ?? 'new', v.values, v.stock])).toEqual([
      ['v1', ['S', 'قطن'], '8'], ['new', ['S', 'كتان'], '0'], ['v2', ['M', 'قطن'], '12'], ['new', ['M', 'كتان'], '0'],
    ]);
    // renaming an option keeps every row
    const renamed = regenerate(d.options, [{ ...colour, name: 'Colour' }, size], d.variants);
    expect(renamed.map((v) => v.id)).toEqual(['v1', 'v2', 'v3', 'v4']);
  });

  it('checks amounts and stock before sending', () => {
    const d = draftOf(tee);
    d.variants[0] = { ...row(d.variants, 0), compare: '100' };
    d.variants[1] = { ...row(d.variants, 1), stock: 'كثير' };
    expect(build(d).errors).toEqual({ 'variants.0.compare': 'السعر قبل الخصم أكبر من السعر', 'variants.1.stock': 'اكتب عددًا صحيحًا' });
    const ok = draftOf(tee);
    ok.variants[2] = { ...row(ok.variants, 2), price: '٢٧٠', stock: '٧' };
    const b = build(ok);
    expect(b.errors).toEqual({});
    expect(b.body.variants[2]).toMatchObject({ id: 'v3', price_minor: 27000 });
    expect(b.stock).toEqual([{ id: 'v3', onHand: 7, tracked: true }]);
  });

  it('reads colours the way the shop does', () => {
    expect(isColourName('اللون')).toBe(true);
    expect(isColourName('Colour')).toBe(true);
    expect(isColourName('المقاس')).toBe(false);
    expect(guessColour('أبيض')).toBe('#f8fafc');
    expect(guessColour('تركواز غامق')).toBe('');
    expect(normalizeHex('1F2A44')).toBe('#1f2a44');
    expect(normalizeHex('#fff')).toBe('');
  });
});
