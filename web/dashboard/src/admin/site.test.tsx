import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession } from './session';
import { faqFromForm, nextPosition, personFromForm, sortFAQs, type SiteFAQ, type SitePerson } from './site';

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

// about is the rest of GET /admin/v1/site, empty.
const about = { story: { story_ar: '', story_en: '', video_id: '' }, images: { logo_url: '', share_url: '' }, people: [], testimonials: [] };

// row is the table's i-th row (0 is the head).
function row(i: number): HTMLElement {
  const r = screen.getAllByRole('row')[i];
  if (!r) throw new Error(`no row ${String(i)}`);
  return r;
}

// cell is a row's i-th cell's text.
const cell = (r: HTMLElement, i: number) => within(r).getAllByRole('cell')[i]?.textContent;

const body = (calls: Call[], method: string, path: string) => calls.find((c) => c.method === method && c.path === path)?.body;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the site in the console', () => {
  it('saves the contact details with a reason, leaving empty profiles out', async () => {
    const calls = mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [], ...about } }),
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
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [second, first], ...about } }),
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
    await screen.findByText('هل يمكنني البدء مجانًا؟');
    const rows = screen.getAllByRole('row').slice(1);
    expect(rows.map((r) => cell(r, 1))).toEqual(['ما هي منصة افتح أونلاين؟', 'هل يمكنني البدء مجانًا؟']);
    expect(cell(row(2), 2)).toBe('—');

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
    await user.click(within(row(1)).getByRole('button', { name: 'تعديل' }));
    const edit = screen.getByRole('form', { name: 'تعديل السؤال' });
    expect(within(edit).getByLabelText('السؤال بالعربية')).toHaveValue('ما هي منصة افتح أونلاين؟');
    await user.click(within(edit).getByLabelText('يظهر في الصفحة الرئيسية'));
    await user.type(within(edit).getByLabelText('السبب (إجباري)'), 'ليس في الرئيسية');
    await user.click(within(edit).getByRole('button', { name: 'حفظ السؤال' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظ السؤال.');
    expect(body(calls, 'PUT', `/admin/v1/site/faqs/${first.id}`)).toMatchObject({ on_home: false, question_en: 'What is iftahonline?' });
    expect(screen.getByRole('form', { name: 'سؤال جديد' })).toBeInTheDocument();

    await user.click(within(row(2)).getByRole('button', { name: 'حذف' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُذف السؤال.');
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe(`/admin/v1/site/faqs/${second.id}`);
    expect(screen.queryByText('هل يمكنني البدء مجانًا؟')).not.toBeInTheDocument();
  });

  it('shows the API’s refusal', async () => {
    mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [], ...about } }),
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

describe('the about page in the console', () => {
  function person(over: Partial<SitePerson> = {}): SitePerson {
    return {
      id: '01M9PERSON0000000000000001',
      kind: 'founder',
      position: 0,
      name_ar: 'أحمد',
      name_en: '',
      role_ar: 'المدير التقني',
      role_en: '',
      bio_ar: '',
      bio_en: '',
      photo_url: '',
      linkedin_url: '',
      x_url: '',
      ...over,
    };
  }

  it('saves the story and the video, and adds a founder', async () => {
    const calls = mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [], ...about } }),
      'PUT /admin/v1/site/story': () => ({ status: 200, body: { story_ar: 'بدأنا من متجر.', story_en: '', video_id: 'dQw4w9WgXcQ' } }),
      'POST /admin/v1/site/people': () => ({ status: 201, body: person({ id: '01M9PERSON0000000000000002', name_ar: 'سارة', kind: 'team', role_ar: 'الدعم' }) }),
    });
    renderSite();
    const user = userEvent.setup();

    const story = await screen.findByRole('form', { name: 'قصة التأسيس والفيديو' });
    await user.type(within(story).getByLabelText('القصة بالعربية'), 'بدأنا من متجر.');
    await user.type(within(story).getByLabelText('رابط فيديو يوتيوب (اتركه فارغًا لإخفاء الفيديو)'), 'https://youtu.be/dQw4w9WgXcQ');
    await user.type(within(story).getByLabelText('السبب (إجباري)'), 'القصة');
    await user.click(within(story).getByRole('button', { name: 'حفظ القصة والفيديو' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت القصة والفيديو.');
    expect(body(calls, 'PUT', '/admin/v1/site/story')).toEqual({ story_ar: 'بدأنا من متجر.', story_en: '', video_url: 'https://youtu.be/dQw4w9WgXcQ', reason: 'القصة' });

    const add = screen.getByRole('form', { name: 'شخص جديد' });
    await user.selectOptions(within(add).getByLabelText('القسم'), 'team');
    await user.type(within(add).getByLabelText('الاسم بالعربية'), 'سارة');
    await user.type(within(add).getByLabelText('المنصب بالعربية'), 'الدعم');
    await user.type(within(add).getByLabelText('السبب (إجباري)'), 'عضو جديد');
    await user.click(within(add).getByRole('button', { name: 'إضافة' }));
    expect(await screen.findByText('أُضيف الشخص.')).toBeInTheDocument();
    expect(body(calls, 'POST', '/admin/v1/site/people')).toMatchObject({ kind: 'team', name_ar: 'سارة', role_ar: 'الدعم', position: 0, remove_photo: false });
    expect(within(screen.getByRole('region', { name: 'المؤسسون وفريق العمل' })).getByText('سارة')).toBeInTheDocument();
  });

  it('edits a founder and removes one with a reason', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('غادر الفريق');
    const ahmed = person();
    const calls = mockAPI({
      'GET /admin/v1/site': () => ({ status: 200, body: { settings, faqs: [], ...about, people: [ahmed] } }),
      [`PUT /admin/v1/site/people/${ahmed.id}`]: () => ({ status: 200, body: { ...ahmed, role_en: 'CTO' } }),
      [`DELETE /admin/v1/site/people/${ahmed.id}`]: () => ({ status: 204 }),
    });
    renderSite();
    const user = userEvent.setup();

    const people = await screen.findByRole('region', { name: 'المؤسسون وفريق العمل' });
    await user.click(within(people).getByRole('button', { name: 'تعديل' }));
    const edit = screen.getByRole('form', { name: 'تعديل الشخص' });
    expect(within(edit).getByLabelText('الاسم بالعربية')).toHaveValue('أحمد');
    await user.type(within(edit).getByLabelText('المنصب بالإنجليزية'), 'CTO');
    await user.type(within(edit).getByLabelText('السبب (إجباري)'), 'English');
    await user.click(within(edit).getByRole('button', { name: 'حفظ' }));
    expect(await screen.findByText('حُفظ الشخص.')).toBeInTheDocument();
    expect(body(calls, 'PUT', `/admin/v1/site/people/${ahmed.id}`)).toMatchObject({ kind: 'founder', role_en: 'CTO' });

    await user.click(within(people).getByRole('button', { name: 'حذف' }));
    expect(await screen.findByText('حُذف الشخص.')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'DELETE')?.path).toBe(`/admin/v1/site/people/${ahmed.id}`);
  });

  it('reads the person form', () => {
    const form = new FormData();
    for (const [k, v] of Object.entries({ kind: 'founder', position: '10', name_ar: ' أحمد ', role_ar: 'المدير', reason: 'سبب' })) form.set(k, v);
    expect(personFromForm(form)).toMatchObject({ kind: 'founder', position: 10, name_ar: 'أحمد', role_ar: 'المدير', name_en: '', remove_photo: false });
    form.set('kind', 'anything');
    form.set('remove_photo', 'on');
    expect(personFromForm(form)).toMatchObject({ kind: 'founder', remove_photo: true });
  });
});
