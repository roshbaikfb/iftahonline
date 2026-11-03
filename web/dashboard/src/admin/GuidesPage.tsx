import { useEffect, useRef, useState, type SubmitEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { ConsoleLayout } from './ConsoleLayout';
import { guideFromForm, guideStatusLabel, slugOK, type Guide, type GuideSummary } from './guides';
import { dateTimeAr } from './lib';
import { uploadImage } from './site';

// GuidesPage lists the site's guides (S4c).
export function GuidesPage() {
  const [guides, setGuides] = useState<GuideSummary[] | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/site/guides').then((r) => {
      if (!live) return;
      if (r.data) setGuides(r.data.items);
      else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, []);

  return (
    <ConsoleLayout>
      <div className="card-head bare">
        <div>
          <h1>الأدلة</h1>
          <p className="muted">مقالات الموقع على ‎/guides‎. تُكتب بصيغة Markdown، بالعربية والإنجليزية اختياريًا، وتظهر على الموقع حين تُنشر.</p>
        </div>
        <Link className="button primary" to="/guides/new">
          دليل جديد
        </Link>
      </div>
      {error && <p role="alert">{error}</p>}
      {guides && (
        <section className="table-card" aria-label="الأدلة">
          {guides.length === 0 ? (
            <p className="empty flat">لا توجد أدلة بعد.</p>
          ) : (
            <div className="scroll">
              <table>
                <thead>
                  <tr>
                    <th scope="col">الدليل</th>
                    <th scope="col">الحالة</th>
                    <th scope="col">بالإنجليزية</th>
                    <th scope="col">آخر تعديل</th>
                  </tr>
                </thead>
                <tbody>
                  {guides.map((g) => (
                    <tr key={g.id}>
                      <td>
                        <Link to={`/guides/${g.id}`}>
                          <strong>{g.title_ar}</strong>
                        </Link>
                        <small className="muted ltr"> /guides/{g.slug}</small>
                      </td>
                      <td>
                        <span className={`status ${g.status === 'published' ? 'store-active' : 'store-closed'}`}>{guideStatusLabel[g.status]}</span>
                      </td>
                      <td>{g.has_en ? 'نعم' : '—'}</td>
                      <td className="muted">{dateTimeAr(g.updated_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}
    </ConsoleLayout>
  );
}

// usePreview renders Markdown as the site would, a moment after typing
// stops (the server's renderer: what the site shows).
function usePreview(markdown: string): string {
  const [html, setHtml] = useState('');
  const empty = !markdown.trim();
  useEffect(() => {
    if (empty) return;
    let live = true;
    const t = setTimeout(() => {
      void api.POST('/admin/v1/site/guides/preview', { body: { markdown } }).then((r) => {
        if (live && r.data) setHtml(r.data.html);
      });
    }, 500);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [markdown, empty]);
  return empty ? '' : html;
}

// GuideEditor writes or edits one guide, with the site's preview beside
// the Markdown.
export function GuideEditor() {
  const { id } = useParams();
  const isNew = id === undefined || id === 'new';
  const navigate = useNavigate();
  const [guide, setGuide] = useState<Guide | null>(null);
  const [bodyAr, setBodyAr] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);
  const preview = usePreview(bodyAr);

  useEffect(() => {
    if (isNew) return;
    let live = true;
    void api.GET('/admin/v1/site/guides/{guide_id}', { params: { path: { guide_id: id } } }).then((r) => {
      if (!live) return;
      if (r.data) {
        setGuide(r.data);
        setBodyAr(r.data.body_ar);
      } else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, [id, isNew]);

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    const input = guideFromForm(new FormData(el));
    if (!slugOK(input.slug)) {
      setError('العنوان في الرابط: حروف إنجليزية صغيرة وأرقام وشرطات فقط، مثل reduce-fake-orders.');
      return;
    }
    setBusy(true);
    setError('');
    setNotice('');
    try {
      const cover = await uploadImage(el.elements.namedItem('cover') as HTMLInputElement | null);
      const body = { ...input, ...(cover ? { upload_key: cover } : {}) };
      const r =
        isNew || !guide
          ? await api.POST('/admin/v1/site/guides', { body })
          : await api.PUT('/admin/v1/site/guides/{guide_id}', { params: { path: { guide_id: guide.id } }, body });
      if (r.error) throw new Error(problemMessage(r.error));
      setGuide(r.data);
      setNotice(r.data.status === 'published' ? 'حُفظ الدليل، وهو منشور على الموقع (يظهر خلال دقائق).' : 'حُفظ الدليل مسودة.');
      if (isNew) void navigate(`/guides/${r.data.id}`, { replace: true });
    } catch (x) {
      setError(x instanceof Error ? x.message : String(x));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!guide) return;
    const reason = window.prompt(`سبب حذف «${guide.title_ar}»`);
    if (!reason) return;
    const { error: err } = await api.DELETE('/admin/v1/site/guides/{guide_id}', { params: { path: { guide_id: guide.id }, query: { reason } } });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    void navigate('/guides');
  }

  if (!isNew && !guide && !error) return <ConsoleLayout>{null}</ConsoleLayout>;

  return (
    <ConsoleLayout>
      <div>
        <p className="muted small">
          <Link to="/guides">الأدلة</Link> / {isNew ? 'دليل جديد' : guide?.title_ar}
        </p>
        <h1>{isNew ? 'دليل جديد' : 'تعديل الدليل'}</h1>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      <form ref={formRef} key={guide?.id ?? 'new'} className="card faq-form" onSubmit={(e) => void save(e)} aria-label="الدليل">
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">العنوان بالعربية</span>
            <input name="title_ar" required minLength={5} maxLength={120} defaultValue={guide?.title_ar ?? ''} />
          </label>
          <label className="grow">
            <span className="field-label">الرابط (بالإنجليزية)</span>
            <input name="slug" required maxLength={80} pattern="[a-z0-9]+(-[a-z0-9]+)*" placeholder="reduce-fake-orders" defaultValue={guide?.slug ?? ''} dir="ltr" />
          </label>
          <label>
            <span className="field-label">الحالة</span>
            <select name="status" defaultValue={guide?.status ?? 'draft'}>
              <option value="draft">مسودة (لا يظهر)</option>
              <option value="published">منشور</option>
            </select>
          </label>
        </div>
        <label>
          <span className="field-label">الملخص (يظهر في القائمة ولجوجل)</span>
          <textarea name="summary_ar" required minLength={20} maxLength={300} rows={2} defaultValue={guide?.summary_ar ?? ''} />
        </label>
        <div className="guide-editor">
          <label>
            <span className="field-label">نص الدليل (Markdown: ## عنوان، - نقطة، **عريض**، [رابط](https://…))</span>
            <textarea
              name="body_ar"
              required
              minLength={50}
              maxLength={40000}
              rows={22}
              defaultValue={guide?.body_ar ?? ''}
              onChange={(e) => {
                setBodyAr(e.currentTarget.value);
              }}
            />
          </label>
          <section className="guide-preview" aria-label="معاينة">
            <span className="field-label">معاينة كما يظهر في الموقع</span>
            {/* The server's renderer: no raw HTML, no script links (S4c). */}
            {preview ? <div className="prose" dangerouslySetInnerHTML={{ __html: preview }} /> : <p className="muted">اكتب لترى المعاينة.</p>}
          </section>
        </div>
        <details>
          <summary>النسخة الإنجليزية (اختيارية: بدونها لا يظهر الدليل في الموقع الإنجليزي)</summary>
          <div className="faq-form">
            <label>
              <span className="field-label">العنوان بالإنجليزية</span>
              <input name="title_en" maxLength={120} defaultValue={guide?.title_en ?? ''} dir="ltr" />
            </label>
            <label>
              <span className="field-label">الملخص بالإنجليزية</span>
              <textarea name="summary_en" maxLength={300} rows={2} defaultValue={guide?.summary_en ?? ''} dir="ltr" />
            </label>
            <label>
              <span className="field-label">النص بالإنجليزية</span>
              <textarea name="body_en" maxLength={40000} rows={10} defaultValue={guide?.body_en ?? ''} dir="ltr" />
            </label>
          </div>
        </details>
        <div className="inline-form">
          {guide?.cover_url && <img src={guide.cover_url} alt="" className="person-thumb" />}
          <label className="grow">
            <span className="field-label">صورة الغلاف (اختيارية، 1200×630 الأفضل)</span>
            <input name="cover" type="file" accept="image/png,image/jpeg,image/webp" />
          </label>
          {guide?.cover_url && (
            <label className="check">
              <input type="checkbox" name="remove_cover" /> احذف الغلاف الحالي
            </label>
          )}
        </div>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">السبب (إجباري)</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            حفظ
          </button>
          {guide && (
            <button type="button" className="button" onClick={() => void remove()}>
              حذف
            </button>
          )}
        </div>
      </form>
    </ConsoleLayout>
  );
}
