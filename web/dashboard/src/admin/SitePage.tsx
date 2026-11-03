import { useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { Editable } from './Can';
import { ConsoleLayout } from './ConsoleLayout';
import { faqFromForm, nextPosition, socials, sortFAQs, type SiteFAQ, type SiteImages, type SitePerson, type SiteReview, type SiteSettings, type SiteStory, type Socials } from './site';
import { SiteIdentity } from './SiteIdentity';
import { SitePeople } from './SitePeople';
import { SiteReviews } from './SiteReviews';

interface About { story: SiteStory; images: SiteImages; people: SitePerson[]; reviews: SiteReview[] }

// SitePage edits what the platform's own site shows from the console
// (ADR-0073): the contact details and the FAQ (S4a); the logo, the
// story, the video, the people and merchants' reviews (S4b-1). Changes
// reach the site within minutes.
export function SitePage() {
  const [settings, setSettings] = useState<SiteSettings | null>(null);
  const [faqs, setFaqs] = useState<SiteFAQ[]>([]);
  const [about, setAbout] = useState<About | null>(null);
  const [editing, setEditing] = useState<SiteFAQ | null>(null);
  const [formKey, setFormKey] = useState(0);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/site').then((r) => {
      if (!live) return;
      if (r.data) {
        setSettings(r.data.settings);
        setFaqs(sortFAQs(r.data.faqs));
        setAbout({ story: r.data.story, images: r.data.images, people: r.data.people, reviews: r.data.testimonials });
        // The new question's position follows the questions read.
        setFormKey((k) => k + 1);
      } else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, []);

  function begin() {
    setBusy(true);
    setError('');
    setNotice('');
  }

  async function saveSettings(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const links: Socials = {};
    for (const s of socials) {
      const v = field(form, `social_${s.key}`).trim();
      if (v) links[s.key] = v;
    }
    begin();
    const { data, error: err } = await api.PUT('/admin/v1/site/settings', {
      body: {
        whatsapp: field(form, 'whatsapp').trim(),
        whatsapp_intl: field(form, 'whatsapp_intl').trim(),
        email: field(form, 'email').trim(),
        company_number: field(form, 'company_number').trim(),
        socials: links,
        reason: field(form, 'reason'),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setSettings(data);
    setNotice('حُفظت بيانات التواصل. تظهر على الموقع خلال دقائق.');
  }

  async function saveFAQ(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const body = faqFromForm(new FormData(e.currentTarget));
    begin();
    const r = editing
      ? await api.PUT('/admin/v1/site/faqs/{faq_id}', { params: { path: { faq_id: editing.id } }, body })
      : await api.POST('/admin/v1/site/faqs', { body });
    setBusy(false);
    if (r.error) {
      setError(problemMessage(r.error));
      return;
    }
    const saved = r.data;
    setFaqs((list) => sortFAQs([...list.filter((f) => f.id !== saved.id), saved]));
    setNotice(editing ? 'حُفظ السؤال.' : 'أُضيف السؤال.');
    setEditing(null);
    setFormKey((k) => k + 1);
  }

  async function remove(f: SiteFAQ) {
    const reason = window.prompt(`سبب حذف السؤال: «${f.question_ar}»`);
    if (!reason) return;
    setError('');
    setNotice('');
    const { error: err } = await api.DELETE('/admin/v1/site/faqs/{faq_id}', { params: { path: { faq_id: f.id }, query: { reason } } });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setFaqs((list) => list.filter((x) => x.id !== f.id));
    if (editing?.id === f.id) {
      setEditing(null);
      setFormKey((k) => k + 1);
    }
    setNotice('حُذف السؤال.');
  }

  function say(msg: string) {
    setError('');
    setNotice(msg);
  }

  function fail(msg: string) {
    setNotice('');
    setError(msg);
  }

  function edit(f: SiteFAQ) {
    setEditing(f);
    setFormKey((k) => k + 1);
  }

  function cancelEdit() {
    setEditing(null);
    setFormKey((k) => k + 1);
  }

  return (
    <ConsoleLayout>
      <Editable p="content.manage">
        <div>
          <h1>الموقع التعريفي</h1>
          <p className="muted">ما يظهر على موقع المنصة من الكونسول. يظهر أي تعديل على الموقع خلال دقائق.</p>
        </div>
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}

        {settings && (
          <form className="card" onSubmit={(e) => void saveSettings(e)} aria-labelledby="h-contact">
            <h2 id="h-contact">بيانات التواصل</h2>
            <p className="muted small">تظهر في أسفل كل صفحة وفي صفحة «عن افتح أونلاين». اترك الحقل فارغًا لإخفائه.</p>
            <div className="inline-form">
              <label className="grow">
                <span className="field-label">واتساب (مصر)</span>
                <input name="whatsapp" defaultValue={settings.whatsapp} placeholder="+201153450716" pattern="\+[1-9][0-9]{7,14}" dir="ltr" />
              </label>
              <label className="grow">
                <span className="field-label">واتساب (دولي)</span>
                <input name="whatsapp_intl" defaultValue={settings.whatsapp_intl} placeholder="+447700900123" pattern="\+[1-9][0-9]{7,14}" dir="ltr" />
              </label>
              <label className="grow">
                <span className="field-label">البريد الإلكتروني</span>
                <input name="email" type="email" defaultValue={settings.email} placeholder="hello@iftahonline.com" maxLength={254} dir="ltr" />
              </label>
              <label className="grow">
                <span className="field-label">رقم الشركة في Companies House</span>
                <input name="company_number" defaultValue={settings.company_number} placeholder="12345678" maxLength={12} dir="ltr" />
              </label>
            </div>
            <h3>حسابات التواصل الاجتماعي</h3>
            <div className="inline-form">
              {socials.map((s) => (
                <label key={s.key} className="grow">
                  <span className="field-label">{s.name}</span>
                  <input
                    name={`social_${s.key}`}
                    type="url"
                    defaultValue={settings.socials[s.key] ?? ''}
                    placeholder={s.example}
                    pattern="https://.*"
                    maxLength={300}
                    dir="ltr"
                  />
                </label>
              ))}
            </div>
            <div className="inline-form">
              <label className="grow">
                <span className="field-label">السبب (إجباري)</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <button type="submit" className="button primary" disabled={busy}>
                حفظ بيانات التواصل
              </button>
            </div>
          </form>
        )}

        {about && (
          <>
            <SiteIdentity story={about.story} images={about.images} onNotice={say} onError={fail} />
            <SitePeople people={about.people} onNotice={say} onError={fail} />
            <SiteReviews reviews={about.reviews} onNotice={say} onError={fail} />
          </>
        )}

        <section className="table-card" aria-labelledby="h-faqs">
          <div className="card-head">
            <h2 id="h-faqs">الأسئلة الشائعة</h2>
            <span className="muted small">{faqs.length} من 100 · تظهر كلها في صفحة الأسئلة، والمختارة في الرئيسية أيضًا.</span>
          </div>
          {faqs.length === 0 ? (
            <p className="empty flat">لا توجد أسئلة بعد.</p>
          ) : (
            <div className="scroll">
              <table>
                <thead>
                  <tr>
                    <th scope="col">الترتيب</th>
                    <th scope="col">السؤال</th>
                    <th scope="col">بالإنجليزية</th>
                    <th scope="col">في الرئيسية</th>
                    <th scope="col">
                      <span className="visually-hidden">إجراء</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {faqs.map((f) => (
                    <tr key={f.id} aria-current={editing?.id === f.id ? 'true' : undefined}>
                      <td className="muted">{f.position}</td>
                      <td>
                        <strong>{f.question_ar}</strong>
                      </td>
                      <td>{f.question_en ? 'نعم' : '—'}</td>
                      <td>{f.on_home ? 'نعم' : '—'}</td>
                      <td>
                        <button
                          type="button"
                          className="link-button"
                          onClick={() => {
                            edit(f);
                          }}
                        >
                          تعديل
                        </button>{' '}
                        <button type="button" className="link-button" onClick={() => void remove(f)}>
                          حذف
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <form key={formKey} className="faq-form" onSubmit={(e) => void saveFAQ(e)} aria-label={editing ? 'تعديل السؤال' : 'سؤال جديد'}>
            <h3>{editing ? 'تعديل السؤال' : 'سؤال جديد'}</h3>
            <label>
              <span className="field-label">السؤال بالعربية</span>
              <input name="question_ar" required minLength={3} maxLength={200} defaultValue={editing?.question_ar ?? ''} />
            </label>
            <label>
              <span className="field-label">الإجابة بالعربية</span>
              <textarea name="answer_ar" required minLength={3} maxLength={2000} rows={3} defaultValue={editing?.answer_ar ?? ''} />
            </label>
            <label>
              <span className="field-label">السؤال بالإنجليزية (اختياري: بدونه لا يظهر في الموقع الإنجليزي)</span>
              <input name="question_en" maxLength={200} defaultValue={editing?.question_en ?? ''} dir="ltr" />
            </label>
            <label>
              <span className="field-label">الإجابة بالإنجليزية</span>
              <textarea name="answer_en" maxLength={2000} rows={3} defaultValue={editing?.answer_en ?? ''} dir="ltr" />
            </label>
            <div className="inline-form">
              <label>
                <span className="field-label">الترتيب (الأصغر أولًا)</span>
                <input name="position" type="number" required min={0} max={100000} defaultValue={editing?.position ?? nextPosition(faqs)} />
              </label>
              <label className="check">
                <input type="checkbox" name="on_home" defaultChecked={editing?.on_home ?? false} /> يظهر في الصفحة الرئيسية
              </label>
              <label className="grow">
                <span className="field-label">السبب (إجباري)</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <button type="submit" className="button primary" disabled={busy}>
                {editing ? 'حفظ السؤال' : 'إضافة السؤال'}
              </button>
              {editing && (
                <button type="button" className="button" onClick={cancelEdit}>
                  إلغاء التعديل
                </button>
              )}
            </div>
          </form>
        </section>
      </Editable>
    </ConsoleLayout>
  );
}
