import { useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { nextPosition, personFromForm, uploadImage, withSaved, type SitePerson } from './site';

interface Props {
  people: SitePerson[];
  onNotice: (msg: string) => void;
  onError: (msg: string) => void;
}

const kindName = { founder: 'شريك مؤسس', team: 'فريق العمل' } as const;

// SitePeople edits the about page's founders and team (S4b-1).
export function SitePeople({ people: initial, onNotice, onError }: Props) {
  const [people, setPeople] = useState(initial);
  const [editing, setEditing] = useState<SitePerson | null>(null);
  const [formKey, setFormKey] = useState(0);
  const [busy, setBusy] = useState(false);

  function reset(p: SitePerson | null) {
    setEditing(p);
    setFormKey((k) => k + 1);
  }

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    setBusy(true);
    try {
      const upload = await uploadImage(el.elements.namedItem('photo') as HTMLInputElement | null);
      const body = { ...personFromForm(new FormData(el)), ...(upload ? { upload_key: upload } : {}) };
      const r = editing
        ? await api.PUT('/admin/v1/site/people/{person_id}', { params: { path: { person_id: editing.id } }, body })
        : await api.POST('/admin/v1/site/people', { body });
      if (r.error) throw new Error(problemMessage(r.error));
      const saved = r.data;
      setPeople((list) => withSaved(list, saved));
      onNotice(editing ? 'حُفظ الشخص.' : 'أُضيف الشخص.');
      reset(null);
    } catch (x) {
      onError(x instanceof Error ? x.message : String(x));
    } finally {
      setBusy(false);
    }
  }

  async function remove(p: SitePerson) {
    const reason = window.prompt(`سبب حذف «${p.name_ar}»`);
    if (!reason) return;
    const { error } = await api.DELETE('/admin/v1/site/people/{person_id}', { params: { path: { person_id: p.id }, query: { reason } } });
    if (error) {
      onError(problemMessage(error));
      return;
    }
    setPeople((list) => list.filter((x) => x.id !== p.id));
    if (editing?.id === p.id) reset(null);
    onNotice('حُذف الشخص.');
  }

  return (
    <section className="table-card" aria-labelledby="h-people">
      <div className="card-head">
        <h2 id="h-people">المؤسسون وفريق العمل</h2>
        <span className="muted small">يظهرون في صفحة «عن افتح أونلاين»، كل قسم يظهر حين يكون فيه أحد.</span>
      </div>
      {people.length === 0 ? (
        <p className="empty flat">لم يُضف أحد بعد.</p>
      ) : (
        <ul className="site-people">
          {people.map((p) => (
            <li key={p.id} aria-current={editing?.id === p.id ? 'true' : undefined}>
              {p.photo_url ? <img src={p.photo_url} alt="" className="person-thumb" /> : <span className="person-thumb" />}
              <span>
                <strong>{p.name_ar}</strong>
                <small className="muted">
                  {kindName[p.kind]} · {p.role_ar}
                </small>
              </span>
              <span>
                <button
                  type="button"
                  className="link-button"
                  onClick={() => {
                    reset(p);
                  }}
                >
                  تعديل
                </button>{' '}
                <button type="button" className="link-button" onClick={() => void remove(p)}>
                  حذف
                </button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <form key={formKey} className="faq-form" onSubmit={(e) => void save(e)} aria-label={editing ? 'تعديل الشخص' : 'شخص جديد'}>
        <h3>{editing ? `تعديل: ${editing.name_ar}` : 'شخص جديد'}</h3>
        <div className="inline-form">
          <label>
            <span className="field-label">القسم</span>
            <select name="kind" defaultValue={editing?.kind ?? 'founder'}>
              <option value="founder">المؤسسون (شريك مؤسس)</option>
              <option value="team">فريق العمل</option>
            </select>
          </label>
          <label className="grow">
            <span className="field-label">الاسم بالعربية</span>
            <input name="name_ar" required minLength={2} maxLength={80} defaultValue={editing?.name_ar ?? ''} />
          </label>
          <label className="grow">
            <span className="field-label">الاسم بالإنجليزية</span>
            <input name="name_en" maxLength={80} defaultValue={editing?.name_en ?? ''} dir="ltr" />
          </label>
        </div>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">المنصب بالعربية</span>
            <input name="role_ar" required minLength={2} maxLength={80} placeholder="المدير التنفيذي" defaultValue={editing?.role_ar ?? ''} />
          </label>
          <label className="grow">
            <span className="field-label">المنصب بالإنجليزية</span>
            <input name="role_en" maxLength={80} placeholder="CEO" defaultValue={editing?.role_en ?? ''} dir="ltr" />
          </label>
        </div>
        <label>
          <span className="field-label">نبذة بالعربية</span>
          <textarea name="bio_ar" rows={3} maxLength={600} defaultValue={editing?.bio_ar ?? ''} />
        </label>
        <label>
          <span className="field-label">نبذة بالإنجليزية</span>
          <textarea name="bio_en" rows={3} maxLength={600} defaultValue={editing?.bio_en ?? ''} dir="ltr" />
        </label>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">LinkedIn</span>
            <input name="linkedin_url" type="url" maxLength={300} pattern="https://.*" defaultValue={editing?.linkedin_url ?? ''} dir="ltr" />
          </label>
          <label className="grow">
            <span className="field-label">X</span>
            <input name="x_url" type="url" maxLength={300} pattern="https://.*" defaultValue={editing?.x_url ?? ''} dir="ltr" />
          </label>
        </div>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">الصورة (اختيارية، مربعة أفضل)</span>
            <input name="photo" type="file" accept="image/png,image/jpeg,image/webp" />
          </label>
          {editing?.photo_url && (
            <label className="check">
              <input type="checkbox" name="remove_photo" /> احذف الصورة الحالية
            </label>
          )}
          <label>
            <span className="field-label">الترتيب</span>
            <input name="position" type="number" required min={0} max={100000} defaultValue={editing?.position ?? nextPosition(people)} />
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
