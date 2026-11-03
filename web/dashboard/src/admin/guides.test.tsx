import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI } from '../test/api';
import { Gate } from './Console';
import { guideFromForm, slugOK, type Guide } from './guides';
import { AdminSessionContext, type AdminSession } from './session';

const admin = testAdmin({ id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' });
const id = '01M9GUIDE00000000000000001';

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

function guide(over: Partial<Guide> = {}): Guide {
  return {
    id,
    slug: 'reduce-fake-orders',
    status: 'draft',
    title_ar: 'كيف تقلل الطلبات الوهمية',
    summary_ar: 'خطوات عملية لتقليل رفض الاستلام.',
    body_ar: '# أولًا\n\nاتصل بالعميل.',
    title_en: '',
    summary_en: '',
    body_en: '',
    cover_url: '',
    updated_at: '2026-11-03T09:00:00Z',
    ...over,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('guides in the console', () => {
  it('lists the guides with their status', async () => {
    mockAPI({
      'GET /admin/v1/site/guides': () => ({
        status: 200,
        body: { items: [{ ...guide(), has_en: true, status: 'published', published_at: '2026-11-03T09:00:00Z' }] },
      }),
    });
    renderAt('/guides');
    const row = (await screen.findByText('كيف تقلل الطلبات الوهمية')).closest('tr') as HTMLElement;
    expect(within(row).getByText('منشور')).toBeInTheDocument();
    expect(within(row).getByText('نعم')).toBeInTheDocument();
    expect(within(row).getByRole('link')).toHaveAttribute('href', `/guides/${id}`);
  });

  it('writes a guide with the site’s preview, and refuses an Arabic address', async () => {
    const calls = mockAPI({
      'POST /admin/v1/site/guides/preview': () => ({ status: 200, body: { html: '<h2 id="x">أولًا</h2><p>اتصل بالعميل.</p>' } }),
      'POST /admin/v1/site/guides': () => ({ status: 201, body: guide() }),
      [`GET /admin/v1/site/guides/${id}`]: () => ({ status: 200, body: guide() }),
    });
    renderAt('/guides/new');
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText('العنوان بالعربية'), 'كيف تقلل الطلبات الوهمية');
    await user.type(screen.getByLabelText('الرابط (بالإنجليزية)'), 'الطلبات');
    await user.type(screen.getByLabelText('الملخص (يظهر في القائمة ولجوجل)'), 'خطوات عملية لتقليل رفض الاستلام.');
    await user.type(screen.getByLabelText(/نص الدليل/), '# أولًا{enter}{enter}اتصل بالعميل.');
    const preview = screen.getByRole('region', { name: 'معاينة' });
    expect(await within(preview).findByRole('heading', { level: 2, name: 'أولًا' }, { timeout: 2000 })).toBeInTheDocument();

    await user.type(screen.getByLabelText('السبب (إجباري)'), 'دليل أول');
    await user.click(screen.getByRole('button', { name: 'حفظ' }));
    // The address field refuses Arabic itself (the browser's check).
    expect(screen.getByLabelText('الرابط (بالإنجليزية)')).toBeInvalid();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/admin/v1/site/guides')).toBe(false);

    const slug = screen.getByLabelText('الرابط (بالإنجليزية)');
    await user.clear(slug);
    await user.type(slug, 'reduce-fake-orders');
    await user.click(screen.getByRole('button', { name: 'حفظ' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظ الدليل مسودة.');
    expect(calls.find((c) => c.method === 'POST' && c.path === '/admin/v1/site/guides')?.body).toMatchObject({
      slug: 'reduce-fake-orders',
      status: 'draft',
      title_ar: 'كيف تقلل الطلبات الوهمية',
      reason: 'دليل أول',
    });
  });
});

describe('guide helpers', () => {
  it('reads the editor and checks addresses', () => {
    const form = new FormData();
    for (const [k, v] of Object.entries({ slug: ' Reduce-Fake-Orders ', status: 'published', title_ar: 'عنوان', summary_ar: 'ملخص', body_ar: 'نص', reason: 'سبب' }))
      form.set(k, v);
    expect(guideFromForm(form)).toEqual({
      slug: 'reduce-fake-orders',
      status: 'published',
      title_ar: 'عنوان',
      summary_ar: 'ملخص',
      body_ar: 'نص',
      remove_cover: false,
      reason: 'سبب',
    });
    form.set('title_en', 'Title');
    expect(guideFromForm(form)).toMatchObject({ title_en: 'Title', summary_en: '', body_en: '' });
    expect(slugOK('reduce-fake-orders')).toBe(true);
    expect(slugOK('fake--orders')).toBe(false);
    expect(slugOK('الطلبات')).toBe(false);
  });
});
