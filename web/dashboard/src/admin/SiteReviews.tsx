import { useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { nextPosition, reviewFromForm, uploadImage, withSaved, type SiteReview } from './site';

interface Props {
  reviews: SiteReview[];
  onNotice: (msg: string) => void;
  onError: (msg: string) => void;
}

// SiteReviews edits merchants' real reviews (S4b-1): the site shows the
// section only once one exists.
export function SiteReviews({ reviews: initial, onNotice, onError }: Props) {
  const [reviews, setReviews] = useState(initial);
  const [editing, setEditing] = useState<SiteReview | null>(null);
  const [formKey, setFormKey] = useState(0);
  const [busy, setBusy] = useState(false);

  function reset(r: SiteReview | null) {
    setEditing(r);
    setFormKey((k) => k + 1);
  }

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    setBusy(true);
    try {
      const upload = await uploadImage(el.elements.namedItem('photo') as HTMLInputElement | null);
      const body = { ...reviewFromForm(new FormData(el)), ...(upload ? { upload_key: upload } : {}) };
      const r = editing
        ? await api.PUT('/admin/v1/site/testimonials/{testimonial_id}', { params: { path: { testimonial_id: editing.id } }, body })
        : await api.POST('/admin/v1/site/testimonials', { body });
      if (r.error) throw new Error(problemMessage(r.error));
      const saved = r.data;
      setReviews((list) => withSaved(list, saved));
      onNotice(editing ? 'حُفظ الرأي.' : 'أُضيف الرأي.');
      reset(null);
    } catch (x) {
      onError(x instanceof Error ? x.message : String(x));
    } finally {
      setBusy(false);
    }
  }

  async function remove(r: SiteReview) {
    const reason = window.prompt(`سبب حذف رأي «${r.name}»`);
    if (!reason) return;
    const { error } = await api.DELETE('/admin/v1/site/testimonials/{testimonial_id}', { params: { path: { testimonial_id: r.id }, query: { reason } } });
    if (error) {
      onError(problemMessage(error));
      return;
    }
    setReviews((list) => list.filter((x) => x.id !== r.id));
    if (editing?.id === r.id) reset(null);
    onNotice('حُذف الرأي.');
  }

  return (
    <section className="table-card" aria-labelledby="h-reviews">
      <div className="card-head">
        <h2 id="h-reviews">آراء التجار</h2>
        <span className="muted small">آراء حقيقية فقط، بإذن أصحابها. القسم يظهر في الرئيسية وصفحة «عن» من أول رأي.</span>
      </div>
      {reviews.length === 0 ? (
        <p className="empty flat">لا توجد آراء بعد.</p>
      ) : (
        <ul className="site-people">
          {reviews.map((r) => (
            <li key={r.id} aria-current={editing?.id === r.id ? 'true' : undefined}>
              {r.photo_url ? <img src={r.photo_url} alt="" className="person-thumb" /> : <span className="person-thumb" />}
              <span>
                <strong>{r.name}</strong>
                <small className="muted">{r.quote_ar}</small>
              </span>
              <span>
                <button
                  type="button"
                  className="link-button"
                  onClick={() => {
                    reset(r);
                  }}
                >
                  تعديل
                </button>{' '}
                <button type="button" className="link-button" onClick={() => void remove(r)}>
                  حذف
                </button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <form key={formKey} className="faq-form" onSubmit={(e) => void save(e)} aria-label={editing ? 'تعديل الرأي' : 'رأي جديد'}>
        <h3>{editing ? `تعديل رأي: ${editing.name}` : 'رأي جديد'}</h3>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">اسم التاجر</span>
            <input name="name" required minLength={2} maxLength={80} defaultValue={editing?.name ?? ''} />
          </label>
          <label className="grow">
            <span className="field-label">اسم المتجر</span>
            <input name="store_name" maxLength={80} defaultValue={editing?.store_name ?? ''} />
          </label>
          <label className="grow">
            <span className="field-label">رابط المتجر (https)</span>
            <input name="store_url" type="url" maxLength={300} pattern="https://.*" defaultValue={editing?.store_url ?? ''} dir="ltr" />
          </label>
        </div>
        <label>
          <span className="field-label">الرأي بالعربية</span>
          <textarea name="quote_ar" required minLength={10} maxLength={600} rows={3} defaultValue={editing?.quote_ar ?? ''} />
        </label>
        <label>
          <span className="field-label">الرأي بالإنجليزية (اختياري: بدونه لا يظهر في الموقع الإنجليزي)</span>
          <textarea name="quote_en" maxLength={600} rows={2} defaultValue={editing?.quote_en ?? ''} dir="ltr" />
        </label>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">صورة التاجر (اختيارية)</span>
            <input name="photo" type="file" accept="image/png,image/jpeg,image/webp" />
          </label>
          {editing?.photo_url && (
            <label className="check">
              <input type="checkbox" name="remove_photo" /> احذف الصورة الحالية
            </label>
          )}
          <label>
            <span className="field-label">الترتيب</span>
            <input name="position" type="number" required min={0} max={100000} defaultValue={editing?.position ?? nextPosition(reviews)} />
          </label>
        </div>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">السبب (إجباري)</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            {editing ? 'حفظ' : 'إضافة'}
          </button>
          {editing && (
            <button
              type="button"
              className="button"
              onClick={() => {
                reset(null);
              }}
            >
              إلغاء التعديل
            </button>
          )}
        </div>
      </form>
    </section>
  );
}
