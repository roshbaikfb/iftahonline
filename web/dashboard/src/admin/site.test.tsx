import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession } from './session';
import { faqFromForm, nextPosition, sortFAQs, type SiteFAQ } from './site';

const admin = { id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' };

function renderSite() {
  const state = { kind: 'signed-in', admin } as const;
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={['/site']}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function q(over: Partial<SiteFAQ> = {}): SiteFAQ {
  return {
    id: '01M9FAQ0000000000000000001',
    position: 0,
    on_home: true,
    question_ar: 'ما هي منصة افتح أونلاين؟',
    answer_ar: 'منصة متاجر.',
    question_en: 'What is iftahonline?',
    answer_en: 'A store platform.',
    ...over,
  };
}

const settings = { whatsapp: '+201153450716', whatsapp_intl: '', email: '', company_number: '', socials: {} };

const body = (calls: Call[], method: string, path: string) => calls.find((c) => c.method === method && c.path === path)?.body;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the site in the console', () => {
  it('saves the contact details with a reason, leaving empty profiles out', async () => {
    const calls = mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [] } }),
      'PUT /admin/v1/site/settings': () => ({
        status: 200,
        body: { ...settings, email: 'hello@iftahonline.com', socials: { instagram: 'https://instagram.com/iftahonline' } },
      }),
    });
    renderSite();
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText('البريد الإلكتروني'), 'hello@iftahonline.com');
    await user.type(screen.getByLabelText('Instagram'), 'https://instagram.com/iftahonline');
    const form = screen.getByRole('form', { name: 'بيانات التواصل' });
    await user.type(within(form).getByLabelText('السبب (إجباري)'), 'بريد الدعم');
    await user.click(screen.getByRole('button', { name: 'حفظ بيانات التواصل' }));

    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت بيانات التواصل');
    expect(body(calls, 'PUT', '/admin/v1/site/settings')).toEqual({
      whatsapp: '+201153450716',
      whatsapp_intl: '',
      email: 'hello@iftahonline.com',
      company_number: '',
      socials: { instagram: 'https://instagram.com/iftahonline' },
      reason: 'بريد الدعم',
    });
  });

  it('adds a question last, edits one, and deletes one with a reason', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('سؤال مكرر');
    const first = q();
    const second = q({ id: '01M9FAQ0000000000000000002', position: 10, on_home: false, question_ar: 'هل يمكنني البدء مجانًا؟', question_en: '', answer_en: '' });
    const calls = mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [second, first] } }),
      'POST /admin/v1/site/faqs': () => ({
        status: 201,
        body: q({ id: '01M9FAQ0000000000000000003', position: 20, on_home: false, question_ar: 'هل تدعمون فودافون كاش؟', question_en: '', answer_en: '' }),
      }),
      [`PUT /admin/v1/site/faqs/${first.id}`]: () => ({ status: 200, body: { ...first, on_home: false } }),
      [`DELETE /admin/v1/site/faqs/${second.id}`]: () => ({ status: 204 }),
    });
    renderSite();
    const user = userEvent.setup();

    // In the site's order, with what each shows.
    const rows = (await screen.findAllByRole('row')).slice(1);
    expect(rows.map((r) => within(r).getAllByRole('cell')[1].textContent)).toEqual(['ما هي منصة افتح أونلاين؟', 'هل يمكنني البدء مجانًا؟']);
    expect(within(rows[1]).getAllByRole('cell')[2]).toHaveTextContent('—');

    // A new question goes last.
    const add = screen.getByRole('form', { name: 'سؤال جديد' });
    expect(within(add).getByLabelText('الترتيب (الأصغر أولًا)')).toHaveValue(20);
    await user.type(within(add).getByLabelText('السؤال بالعربية'), 'هل تدعمون فودافون كاش؟');
    await user.type(within(add).getByLabelText('الإجابة بالعربية'), 'قريبًا.');
    await user.type(within(add).getByLabelText('السبب (إجباري)'), 'سؤال يتكرر');
    await user.click(within(add).getByRole('button', { name: 'إضافة السؤال' }));
    expect(await screen.findByRole('status')).toHaveTextContent('أُضيف السؤال.');
    expect(body(calls, 'POST', '/admin/v1/site/faqs')).toEqual({
      position: 20,
      on_home: false,
      question_ar: 'هل تدعمون فودافون كاش؟',
      answer_ar: 'قريبًا.',
      reason: 'سؤال يتكرر',
    });
    expect(screen.getAllByRole('row')).toHaveLength(4);

    // Editing fills the form with the question.
    await user.click(within(screen.getAllByRole('row')[1]).getByRole('button', { name: 'تعديل' }));
    const edit = screen.getByRole('form', { name: 'تعديل السؤال' });
    expect(within(edit).getByLabelText('السؤال بالعربية')).toHaveValue('ما هي منصة افتح أونلاين؟');
    await user.click(within(edit).getByLabelText('يظهر في الصفحة الرئيسية'));
    await user.type(within(edit).getByLabelText('السبب (إجباري)'), 'ليس في الرئيسية');
    await user.click(within(edit).getByRole('button', { name: 'حفظ السؤال' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظ السؤال.');
    expect(body(calls, 'PUT', `/admin/v1/site/faqs/${first.id}`)).toMatchObject({ on_home: false, question_en: 'What is iftahonline?' });
    expect(screen.getByRole('form', { name: 'سؤال جديد' })).toBeInTheDocument();

    await user.click(within(screen.getAllByRole('row')[2]).getByRole('button', { name: 'حذف' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُذف السؤال.');
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe(`/admin/v1/site/faqs/${second.id}`);
    expect(screen.queryByText('هل يمكنني البدء مجانًا؟')).not.toBeInTheDocument();
  });

  it('shows the API’s refusal', async () => {
    mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [] } }),
      'PUT /admin/v1/site/settings': () => ({
        status: 422,
        body: { type: 'about:blank', title: 'Validation failed', status: 422, errors: [{ field: 'whatsapp', code: 'invalid', message: 'x' }] },
      }),
    });
    renderSite();
    const user = userEvent.setup();
    const form = await screen.findByRole('form', { name: 'بيانات التواصل' });
    await user.type(within(form).getByLabelText('السبب (إجباري)'), 'تجربة');
    await user.click(screen.getByRole('button', { name: 'حفظ بيانات التواصل' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
  });
});

describe('site helpers', () => {
  it('puts a new question last and keeps the site’s order', () => {
    expect(nextPosition([])).toBe(0);
    expect(nextPosition([q({ position: 30 }), q({ position: 5 })])).toBe(40);
    expect(sortFAQs([q({ id: 'b', position: 10 }), q({ id: 'a', position: 0 })]).map((f) => f.id)).toEqual(['a', 'b']);
  });

  it('sends English only when written', () => {
    const form = new FormData();
    for (const [k, v] of Object.entries({ position: '5', question_ar: ' سؤال؟ ', answer_ar: 'جواب', question_en: '', answer_en: '', reason: 'سبب' })) form.set(k, v);
    expect(faqFromForm(form)).toEqual({ position: 5, on_home: false, question_ar: 'سؤال؟', answer_ar: 'جواب', reason: 'سبب' });
    form.set('question_en', 'Q?');
    form.set('on_home', 'on');
    expect(faqFromForm(form)).toMatchObject({ on_home: true, question_en: 'Q?', answer_en: '' });
  });
});
