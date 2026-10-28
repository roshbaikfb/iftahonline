import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { sectionTypeList } from '../test/sections';
import { LandingEditor } from './LandingEditor';
import { Pages } from './Pages';
import { PolicyEditor } from './PolicyEditor';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const hoodie = '01M5PROD000000000000000001';
const pageID = '01M5PAGE000000000000000001';
// Waits after a screen change, which loads the next screen's data: a
// busy CI machine needs more than the default second.
const slow = { timeout: 3000 };

function session(): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'u', email: 'a@b.c', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const landing = (over: Record<string, unknown> = {}) => ({
  id: pageID, kind: 'landing', slug: 'hoodie-offer', title: 'عرض الهودي', description: '', body: '', status: 'draft', version: 1,
  sections: [{ id: 'main', type: 'product_landing', settings: { product_id: hoodie } }],
  ...over,
});
const policy = (slug: string, over: Record<string, unknown> = {}) => ({ kind: 'policy', slug, title: '', description: '', body: '', status: 'draft', version: 0, sections: [], ...over });

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/storefront/theme`]: () => ({
      status: 200,
      body: { settings: { colors: { primary: '#0f766e', accent: '#f59e0b', background: '#ffffff', text: '#0f172a' }, font: 'cairo', radius: 'medium', look: 'classic' }, version: 1 },
    }),
    [`GET ${base}/storefront/section-types`]: () => ({ status: 200, body: { items: sectionTypeList, definitions: {} } }),
    [`GET ${base}/storefront/logo`]: () => ({ status: 200, body: {} }),
    [`GET ${base}/storefront/pages`]: () => ({ status: 200, body: { items: [landing(), landing({ id: '01M5PAGE000000000000000002', slug: 'eid', title: 'عروض العيد', status: 'published', version: 3 })] } }),
    [`GET ${base}/storefront/pages/${pageID}`]: () => ({ status: 200, body: landing() }),
    [`GET ${base}/storefront/policies`]: () => ({ status: 200, body: { items: [policy('shipping', { status: 'published', version: 2, title: 'الشحن والتوصيل' }), policy('refund'), policy('privacy'), policy('terms')] } }),
    [`GET ${base}/categories`]: () => ({ status: 200, body: { items: [] } }),
    [`GET ${base}/products`]: () => ({
      status: 200,
      body: { items: [{ id: hoodie, slug: 'hoodie', title: 'هودي شتوي', description: '', status: 'active', options: [], variants: [], category_ids: [], images: [], created_at: '', updated_at: '' }] },
    }),
    ...over,
  });
}

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/pages" element={<Pages />} />
          <Route path="/stores/:storeId/pages/new" element={<LandingEditor />} />
          <Route path="/stores/:storeId/pages/:pageId" element={<LandingEditor />} />
          <Route path="/stores/:storeId/pages/policies/:policy" element={<PolicyEditor />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Pages', () => {
  it('lists the landing pages and the policies with whether shoppers see them', async () => {
    routes();
    renderAt(`/stores/${store}/pages`);
    const landingCard = (await screen.findByRole('heading', { name: 'صفحات الهبوط' })).closest('section');
    if (!landingCard) throw new Error('no landing card');
    expect(await within(landingCard).findByRole('link', { name: 'عرض الهودي' })).toHaveAttribute('href', `/stores/${store}/pages/${pageID}`);
    expect(within(landingCard).getByText('/pages/eid')).toBeInTheDocument();
    expect(within(landingCard).getByText('مسودة')).toBeInTheDocument();
    expect(within(landingCard).getByText('منشورة')).toBeInTheDocument();
    expect(within(landingCard).getByRole('link', { name: '+ صفحة جديدة' })).toHaveAttribute('href', `/stores/${store}/pages/new`);

    const policiesCard = screen.getByRole('heading', { name: 'السياسات' }).closest('section');
    if (!policiesCard) throw new Error('no policies card');
    const rows = within(policiesCard).getAllByRole('listitem');
    expect(rows.map((r) => r.querySelector('strong')?.textContent)).toEqual(['الشحن والتوصيل', 'سياسة الاسترجاع', 'سياسة الخصوصية', 'الشروط والأحكام']);
    expect(rows[0]).toHaveTextContent('منشورة');
    expect(rows[1]).toHaveTextContent('لم تُكتب بعد');
    expect(within(policiesCard).getByRole('link', { name: 'سياسة الاسترجاع' })).toHaveAttribute('href', `/stores/${store}/pages/policies/refund`);
  });
});

describe('LandingEditor', () => {
  it('starts a page with one product and publishes it', async () => {
    const calls = routes({
      [`POST ${base}/storefront/pages`]: (c) => ({ status: 201, body: { ...landing(), ...(c.body as object), slug: 'winter', id: pageID, version: 1 } }), // the server derives the address from the title
    });
    renderAt(`/stores/${store}/pages/new`);
    const user = userEvent.setup();
    expect(screen.getByRole('heading', { level: 1, name: 'صفحة جديدة' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'منتج واحد' })).toBeInTheDocument();
    expect(screen.getByText('القسم 1 من 1')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'حذف الصفحة' })).toBeNull();

    await user.type(screen.getByLabelText('العنوان'), 'عرض الشتاء');
    await user.selectOptions(await screen.findByLabelText('المنتج'), 'هودي شتوي');
    await user.type(screen.getByLabelText('المميزات'), 'قطن 100%');
    await user.click(screen.getByRole('button', { name: 'نشر' }));

    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST')?.body).toMatchObject({
        title: 'عرض الشتاء', slug: '', status: 'published', version: 0,
        sections: [{ type: 'product_landing', settings: { product_id: hoodie, highlights: ['قطن 100%'] } }],
      });
    }, slow);
    expect(await screen.findByText('نُشرت الصفحة.', {}, slow)).toBeInTheDocument();
    expect(await screen.findByRole('heading', { level: 1, name: 'عرض الشتاء' }, slow)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'عرض في المتجر' })).toHaveAttribute('href', expect.stringMatching(/\/pages\/winter$/) as string);
    // the new address keeps the screen: no reload of what was just saved
    expect(calls.filter((c) => c.method === 'GET' && c.path.endsWith(`/storefront/pages/${pageID}`))).toHaveLength(0);
    expect(screen.getByText('نُشرت الصفحة.')).toBeInTheDocument();
  });

  it('offers «منتج واحد» first, says what the server refused, and deletes the page', async () => {
    const calls = routes({
      [`PUT ${base}/storefront/pages/${pageID}`]: () => ({ status: 422, body: { title: 'Unprocessable', status: 422, errors: [{ field: 'slug', code: 'taken', message: 'another page already uses this slug' }] } }),
      [`DELETE ${base}/storefront/pages/${pageID}`]: () => ({ status: 204 }),
    });
    renderAt(`/stores/${store}/pages/${pageID}`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 1, name: 'عرض الهودي' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'حفظ كمسودة' })).toBeDisabled(); // nothing changed

    await user.click(screen.getByRole('button', { name: '+ أضف قسمًا' }));
    const types = within(screen.getByRole('dialog')).getAllByRole('button').map((b) => b.querySelector('strong')?.textContent).filter(Boolean);
    expect(types[0]).toBe('منتج واحد');
    expect(types).toHaveLength(10);
    expect(within(screen.getByRole('dialog')).queryByText(/لصفحات الهبوط فقط/)).toBeNull();
    await user.keyboard('{Escape}');

    await user.type(screen.getByRole('textbox', { name: 'رابط الصفحة' }), '-2');
    await user.click(screen.getByRole('button', { name: 'حفظ كمسودة' }));
    expect(await screen.findByText('صفحة أخرى تستخدم هذا الرابط')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ slug: 'hoodie-offer-2', status: 'draft', version: 1 });

    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    await user.click(screen.getByRole('button', { name: 'حذف الصفحة' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE' && c.path.endsWith(pageID))).toBe(true);
    }, slow);
    // back on the list, once its requests are answered
    expect(await screen.findByRole('heading', { level: 1, name: 'الصفحات' }, slow)).toBeInTheDocument();
    expect(await screen.findByRole('link', { name: 'سياسة الاسترجاع' }, slow)).toBeInTheDocument();
  });
});

describe('PolicyEditor', () => {
  it('writes a policy and publishes it over the version read', async () => {
    const calls = routes({
      [`GET ${base}/storefront/policies/refund`]: () => ({ status: 200, body: policy('refund') }),
      [`PUT ${base}/storefront/policies/refund`]: (c) => ({ status: 200, body: { ...policy('refund'), ...(c.body as object), version: 1 } }),
    });
    renderAt(`/stores/${store}/pages/policies/refund`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 1, name: 'سياسة الاسترجاع' })).toBeInTheDocument();
    expect(screen.getByText('لم تُكتب بعد')).toBeInTheDocument();
    expect(screen.getByLabelText(/العنوان/)).toHaveAttribute('placeholder', 'سياسة الاسترجاع');

    await user.type(screen.getByLabelText('النص'), 'يمكنك إرجاع المنتج خلال 14 يومًا.');
    expect(screen.getByText(`نص عادي: افصل بين الفقرات بسطر فارغ. ${String('يمكنك إرجاع المنتج خلال 14 يومًا.'.length)} من 20000 حرف.`)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'نشر' }));
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ title: '', body: 'يمكنك إرجاع المنتج خلال 14 يومًا.', status: 'published', version: 0 });
    }, slow);
    expect(await screen.findByText('نُشرت. تظهر في أسفل صفحات متجرك.')).toBeInTheDocument();
    expect(screen.getByText('منشورة')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'إلغاء النشر' })).toBeEnabled();
    expect(screen.getByRole('link', { name: 'عرض في المتجر' })).toHaveAttribute('href', expect.stringMatching(/\/policies\/refund$/) as string);
  });
});
