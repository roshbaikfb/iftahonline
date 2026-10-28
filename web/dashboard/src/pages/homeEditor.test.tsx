import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { HomeEditor } from './HomeEditor';

// The server's own section schemas, so the form is tested against what
// the API serves.
const schemaFiles = import.meta.glob<Record<string, unknown>>('../../../../internal/modules/storefront/internal/sections/schemas/*.json', { eager: true, import: 'default' });
const sectionTypes = Object.entries(schemaFiles)
  .map(([path, schema]) => ({ type: path.replace(/^.*\/|\.json$/g, ''), schema }))
  .filter((t) => t.type !== 'defs');

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const cat = '01M5CAT0000000000000000001';
const hoodie = '01M5PROD000000000000000001';

const live = [
  { id: 'hero', type: 'hero', settings: { badge: 'جديد', heading: 'تشكيلة الشتاء وصلت', subheading: 'قطن مصري دافئ', button_label: 'تسوّق الآن', button_link: '/c/tshirts', align: 'start' } },
  { id: 'cats', type: 'category_tiles', settings: {} },
  { id: 'newest', type: 'product_grid', settings: { title: 'وصل حديثًا', source: 'newest', limit: 8 } },
];

function session(): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'u', email: 'a@b.c', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const page = (sections: unknown[], version = 2) => ({ kind: 'home', slug: 'home', title: '', description: '', body: '', status: 'published', version, sections });

function routes(draft?: unknown[], over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/storefront/home`]: () => ({ status: 200, body: page(live) }),
    [`GET ${base}/storefront/draft`]: () => ({ status: 200, body: draft ? { home: { title: '', description: '', sections: draft } } : {} }),
    [`GET ${base}/storefront/theme`]: () => ({
      status: 200,
      body: { settings: { colors: { primary: '#0f766e', accent: '#f59e0b', background: '#ffffff', text: '#0f172a' }, font: 'cairo', radius: 'medium', look: 'classic' }, version: 1 },
    }),
    [`GET ${base}/storefront/section-types`]: () => ({ status: 200, body: { items: sectionTypes, definitions: {} } }),
    [`GET ${base}/storefront/logo`]: () => ({ status: 200, body: {} }),
    [`GET ${base}/storefront/pages`]: () => ({ status: 200, body: { items: [{ ...page([]), id: '01M5PAGE000000000000000001', kind: 'landing', slug: 'eid', title: 'عروض العيد' }] } }),
    [`GET ${base}/categories`]: () => ({ status: 200, body: { items: [{ id: cat, name: 'تيشيرتات', slug: 'tshirts', position: 0, created_at: '', updated_at: '' }] } }),
    [`GET ${base}/products`]: () => ({
      status: 200,
      body: { items: [{ id: hoodie, slug: 'hoodie', title: 'هودي شتوي', description: '', status: 'active', options: [], variants: [], category_ids: [], images: [], created_at: '', updated_at: '' }] },
    }),
    [`PUT ${base}/storefront/draft/home`]: (c) => ({ status: 200, body: { home: c.body, updated_at: '' } }),
    ...over,
  });
}

function renderEditor() {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[`/stores/${store}/home-page`]}>
        <Routes>
          <Route path="/stores/:storeId/home-page" element={<HomeEditor />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const drafts = (calls: Call[]) => calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/draft/home'));
const lastDraft = (calls: Call[]) => drafts(calls).at(-1)?.body as { sections: { id: string; type: string; settings: Record<string, unknown> }[] } | undefined;
const sectionList = () => screen.getByRole('list');
const names = () => within(sectionList()).getAllByRole('listitem').map((li) => li.querySelector('.name strong')?.textContent);

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('HomeEditor', () => {
  it('lists the sections, edits the chosen one as a draft, and publishes over the version read', async () => {
    const calls = routes(undefined, {
      [`POST ${base}/storefront/draft/home/publish`]: () => ({ status: 200, body: page(live, 3) }),
    });
    renderEditor();
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 1, name: 'الصفحة الرئيسية' })).toBeInTheDocument();
    expect(names()).toEqual(['صورة رئيسية', 'مربعات التصنيفات', 'شبكة منتجات']);
    expect(within(sectionList()).getByText('وصل حديثًا · 8 منتجات')).toBeInTheDocument();
    expect(screen.queryByText('تغييرات لم تُنشر')).toBeNull();
    for (const b of screen.getAllByRole('button', { name: 'نشر' })) expect(b).toBeDisabled();

    // the hero's fields, as the mockup
    expect(screen.getByRole('heading', { level: 2, name: 'صورة رئيسية' })).toBeInTheDocument();
    expect(screen.getByText('القسم 1 من 3')).toBeInTheDocument();
    expect(screen.getByLabelText(/شارة صغيرة/)).toHaveValue('جديد');
    expect(screen.getByLabelText('الزر يفتح')).toHaveValue('/c/tshirts');
    expect(await screen.findByRole('option', { name: 'هودي شتوي' })).toBeInTheDocument();
    expect(screen.getByText('نستخدم الصورة الرئيسية للمنتج، فتبقى محدّثة.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'يمين' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText('القسم المختار محاط بإطار.')).toBeInTheDocument();

    const heading = screen.getByLabelText('العنوان');
    await user.clear(heading);
    await user.type(heading, 'عروض الصيف');
    await user.click(screen.getByRole('button', { name: 'وسط' }));
    await waitFor(() => {
      expect(lastDraft(calls)?.sections[0]?.settings).toMatchObject({ heading: 'عروض الصيف', align: 'center' });
    }, { timeout: 2000 });
    expect(lastDraft(calls)?.sections.map((s) => s.id)).toEqual(['hero', 'cats', 'newest']);
    expect(await screen.findByText('حُفظت المسودة')).toBeInTheDocument();
    expect(screen.getByText('تغييرات لم تُنشر')).toBeInTheDocument();
    expect(within(sectionList()).getByText('عروض الصيف')).toBeInTheDocument();

    // the button goes to the page's product grid, a landing page or the hero has none
    const link = screen.getByLabelText('الزر يفتح');
    await user.selectOptions(link, 'كل المنتجات');
    await user.selectOptions(link, 'صفحة: عروض العيد');
    await waitFor(() => {
      expect(lastDraft(calls)?.sections[0]?.settings['button_link']).toBe('/pages/eid');
    }, { timeout: 2000 });

    const [publish] = screen.getAllByRole('button', { name: 'نشر' });
    if (!publish) throw new Error('no publish button');
    await user.click(publish);
    await waitFor(() => {
      expect(calls.find((c) => c.method === 'POST' && c.path.endsWith('/draft/home/publish'))?.body).toEqual({ version: 2 });
    });
    await waitFor(() => {
      expect(screen.queryByText('تغييرات لم تُنشر')).toBeNull();
    });
  });

  it('adds a section from the dialog at the bottom, moves and deletes sections', async () => {
    const calls = routes();
    renderEditor();
    const user = userEvent.setup();
    await screen.findByText('القسم 1 من 3');

    await user.click(screen.getByRole('button', { name: '+ أضف قسمًا' }));
    const dialog = screen.getByRole('dialog', { name: 'أضف قسمًا' });
    expect(within(dialog).getByText('يُضاف في أسفل الصفحة، ثم تنقله حيث تريد.')).toBeInTheDocument();
    expect(within(dialog).getAllByRole('button', { name: /./ }).map((b) => b.querySelector('strong')?.textContent).filter(Boolean)).toEqual([
      'صورة رئيسية', 'شبكة منتجات', 'مربعات التصنيفات', 'عروض', 'عدّاد تنازلي', 'آراء العملاء', 'مميزات المتجر', 'أسئلة شائعة', 'زر واتساب',
    ]);
    expect(within(dialog).getByText('«منتج واحد» لصفحات الهبوط فقط، تجده في «الصفحات».')).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();

    await user.click(screen.getByRole('button', { name: '+ أضف قسمًا' }));
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /أسئلة شائعة/ }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(names()).toEqual(['صورة رئيسية', 'مربعات التصنيفات', 'شبكة منتجات', 'أسئلة شائعة']);
    expect(screen.getByText('القسم 4 من 4')).toBeInTheDocument();
    await user.type(screen.getByLabelText('السؤال'), 'متى يصل طلبي؟');
    await user.type(screen.getByLabelText('الجواب'), 'خلال 3 أيام');
    await waitFor(() => {
      expect(lastDraft(calls)?.sections[3]).toMatchObject({ type: 'faq', settings: { items: [{ question: 'متى يصل طلبي؟', answer: 'خلال 3 أيام' }] } });
    }, { timeout: 2000 });
    expect(lastDraft(calls)?.sections[3]?.id).toMatch(/^[a-z0-9-]{1,32}$/);

    // the first moves down, the others up
    expect(screen.getByRole('button', { name: 'انقل صورة رئيسية لأسفل' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'انقل شبكة منتجات لأعلى' }));
    expect(names()).toEqual(['صورة رئيسية', 'شبكة منتجات', 'مربعات التصنيفات', 'أسئلة شائعة']);

    vi.spyOn(globalThis, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
    await user.click(screen.getByRole('button', { name: 'احذف مربعات التصنيفات' }));
    expect(names()).toHaveLength(4); // not confirmed
    await user.click(screen.getByRole('button', { name: 'احذف مربعات التصنيفات' }));
    expect(names()).toEqual(['صورة رئيسية', 'شبكة منتجات', 'أسئلة شائعة']);
    await waitFor(() => {
      expect(lastDraft(calls)?.sections.map((s) => s.type)).toEqual(['hero', 'product_grid', 'faq']);
    }, { timeout: 2000 });
  });

  it('shows what the server refused on the section and field, in Arabic', async () => {
    routes(undefined, {
      [`PUT ${base}/storefront/draft/home`]: () => ({
        status: 422,
        body: {
          title: 'Unprocessable', status: 422,
          errors: [{ field: 'sections[0].settings.button_link', code: 'pattern', message: 'does not match pattern' }],
        },
      }),
    });
    renderEditor();
    const user = userEvent.setup();
    await screen.findByText('القسم 1 من 3');
    await user.selectOptions(screen.getByLabelText('الزر يفتح'), 'رابط آخر…');
    await user.type(screen.getByLabelText('الرابط'), 'ftp://x');
    expect(await screen.findByText('رابط غير مقبول: يبدأ بـ / أو https://', {}, { timeout: 2000 })).toBeInTheDocument();
    expect(screen.getByText('لم تُحفظ المسودة: راجع الأقسام المعلّمة')).toBeInTheDocument();
    expect(within(sectionList()).getAllByRole('listitem')[0]).toHaveClass('has-error');
  });

  it('opens the draft, and discards it back to the published page', async () => {
    const calls = routes([live[2], live[0]], { [`DELETE ${base}/storefront/draft/home`]: () => ({ status: 204 }) });
    renderEditor();
    const user = userEvent.setup();
    await screen.findByText('القسم 1 من 2');
    expect(names()).toEqual(['شبكة منتجات', 'صورة رئيسية']);
    expect(screen.getByText('تغييرات لم تُنشر')).toBeInTheDocument();
    // a grid's chosen products only show for «أختارها بنفسي»
    expect(screen.queryByLabelText('اختر المنتجات')).toBeNull();
    await user.selectOptions(screen.getByLabelText('المنتجات المعروضة'), 'أختارها بنفسي');
    await user.selectOptions(await screen.findByLabelText('اختر المنتجات'), 'هودي شتوي');
    expect(screen.getByRole('button', { name: 'أزل هودي شتوي' })).toBeInTheDocument();
    await waitFor(() => {
      expect(lastDraft(calls)?.sections[0]?.settings).toMatchObject({ source: 'manual', product_ids: [hoodie] });
    }, { timeout: 2000 });

    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    await user.click(screen.getByRole('button', { name: 'تجاهل التغييرات' }));
    await waitFor(() => {
      expect(names()).toEqual(['صورة رئيسية', 'مربعات التصنيفات', 'شبكة منتجات']);
    });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
    expect(screen.queryByText('تغييرات لم تُنشر')).toBeNull();
  });

  it('opens one section at a time on phones', async () => {
    vi.stubGlobal('matchMedia', (q: string) => ({ matches: true, media: q, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    routes();
    renderEditor();
    const user = userEvent.setup();
    expect(await screen.findByText('اضغط قسمًا لتعديله.')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: 'معاينة' })).toBeNull();
    const hero = screen.getByText('صورة رئيسية').closest('details');
    expect(hero).toHaveAttribute('open');
    expect(screen.getByLabelText('العنوان')).toHaveValue('تشكيلة الشتاء وصلت');
    expect(screen.getByText('وصل حديثًا')).toBeInTheDocument(); // a closed one's summary, shorter on phones
    await user.click(screen.getByText('مربعات التصنيفات'));
    expect(hero).not.toHaveAttribute('open');
    expect(screen.getByText('مربعات التصنيفات').closest('details')).toHaveAttribute('open');
    expect(screen.getByRole('button', { name: 'انقل مربعات التصنيفات لأعلى' })).toBeInTheDocument();
  });
});
