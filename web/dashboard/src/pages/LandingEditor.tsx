import { useEffect, useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router';

import '../shop-fonts.css';
import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { PageActions } from '../components/PageActions';
import { SectionsEditor } from '../components/SectionsEditor';
import { useNarrow } from '../lib/narrow';
import { pageStatus } from '../lib/pages';
import { problemMessage } from '../lib/problem';
import { fieldErrors as toFieldErrors, landingTypes, newID, type FieldError, type Section } from '../lib/sections';
import { useShopData } from '../lib/shopData';
import { storefrontURL } from '../lib/storefront';

type Page = components['schemas']['Page'];

interface Form {
  title: string;
  slug: string;
  description: string;
  sections: Section[];
}

// blank is a new landing page: one «منتج واحد» section to fill in.
function blank(): Form {
  return { title: '', slug: '', description: '', sections: [{ id: newID(), type: 'product_landing', settings: {} }] };
}

// LandingEditor writes a landing page (U6c): its title, address and
// search description, and its sections, as on the home page, starting
// with «منتج واحد». It is saved when the merchant says so, as a draft
// or published (landing pages have no separate draft; ADR-0036).
export function LandingEditor() {
  const { storeId = '', pageId = 'new' } = useParams();
  const navigate = useNavigate();
  const narrow = useNarrow();
  const data = useShopData(storeId);
  const location = useLocation();
  const [saved, setSaved] = useState<Page | null>(null);
  const [form, setForm] = useState<Form | null>(() => (pageId === 'new' ? blank() : null));
  const [dirty, setDirty] = useState(false);
  const [selected, setSelected] = useState(() => form?.sections[0]?.id ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  // A page just created arrives here with what was done.
  const [notice, setNotice] = useState(() => (location.state as { notice?: string } | null)?.notice ?? '');
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    if (pageId === 'new') return;
    let live = true;
    void api.GET('/v1/stores/{store_id}/storefront/pages/{page_id}', { params: { path: { store_id: storeId, page_id: pageId } } }).then(({ data: p, error: err }) => {
      if (!live) return;
      if (!p) {
        setError(problemMessage(err));
        return;
      }
      setSaved(p);
      setForm({ title: p.title, slug: p.slug, description: p.description, sections: p.sections });
      setSelected(p.sections[0]?.id ?? '');
    });
    return () => {
      live = false;
    };
  }, [storeId, pageId]);

  const edit = (next: Partial<Form>) => {
    if (!form) return;
    setForm({ ...form, ...next });
    setDirty(true);
    setNotice('');
  };

  async function save(status: 'draft' | 'published') {
    if (!form) return;
    setBusy(true);
    const body = { ...form, status, version: saved?.version ?? 0 };
    const res = saved?.id
      ? await api.PUT('/v1/stores/{store_id}/storefront/pages/{page_id}', { params: { path: { store_id: storeId, page_id: saved.id } }, body })
      : await api.POST('/v1/stores/{store_id}/storefront/pages', { params: { path: { store_id: storeId } }, body });
    setBusy(false);
    if (res.error) {
      const fields = (res.error as { errors?: FieldError[] }).errors ?? [];
      setErrors(toFieldErrors(fields));
      setError(fields.length ? 'لم تُحفظ الصفحة: راجع الحقول المعلّمة.' : problemMessage(res.error));
      return;
    }
    const p = res.data;
    setSaved(p);
    setForm({ title: p.title, slug: p.slug, description: p.description, sections: p.sections });
    setDirty(false);
    setErrors({});
    setError('');
    const done = status === 'published' ? 'نُشرت الصفحة.' : 'حُفظت الصفحة كمسودة. لا يراها العملاء حتى تنشرها.';
    setNotice(done);
    if (!saved?.id && p.id) void navigate(`/stores/${storeId}/pages/${p.id}`, { replace: true, state: { notice: done } });
  }

  async function remove() {
    if (!saved?.id || !globalThis.confirm(`حذف صفحة «${saved.title}» نهائيًا؟`)) return;
    setBusy(true);
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/storefront/pages/{page_id}', { params: { path: { store_id: storeId, page_id: saved.id } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    void navigate(`/stores/${storeId}/pages`);
  }

  const back = (
    <Link to={`/stores/${storeId}/pages`} className="back">
      → الصفحات
    </Link>
  );
  if (!form) {
    return (
      <Layout storeId={storeId}>
        {back}
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const st = saved ? pageStatus(saved) : null;
  const base = data.store ? `${storefrontURL(data.store.slug)}/pages/` : '/pages/';

  return (
    <Layout storeId={storeId}>
      <div className="page-head editor-head">
        <div>
          {back}
          <div className="title-line">
            <h1>{saved?.title ?? 'صفحة جديدة'}</h1>
            {st && <span className={`status ${st.className}`}>{st.label}</span>}
            {dirty && saved && <span className="status s-pending">تغييرات لم تُحفظ</span>}
          </div>
        </div>
        <PageActions
          status={saved ? saved.status : null}
          dirty={dirty}
          busy={busy}
          viewURL={saved ? `${base}${encodeURIComponent(saved.slug)}` : undefined}
          onSave={(s) => void save(s)}
          onDelete={saved?.id ? () => void remove() : undefined}
        />
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <section className="card editor-card page-meta" aria-labelledby="h-meta">
        <h2 id="h-meta">بيانات الصفحة</h2>
        <div className="two-cols">
          <label className="field-label">
            العنوان
            <input
              value={form.title}
              maxLength={200}
              aria-invalid={Boolean(errors['title']) || undefined}
              onChange={(e) => {
                edit({ title: e.target.value });
              }}
            />
            {errors['title'] && <span className="field-error">{errors['title']}</span>}
          </label>
          <label className="field-label">
            رابط الصفحة
            <span className="slug-input" dir="ltr">
              <span className="muted small">/pages/</span>
              <input
                value={form.slug}
                aria-label="رابط الصفحة"
                maxLength={120}
                placeholder="من العنوان"
                aria-invalid={Boolean(errors['slug']) || undefined}
                onChange={(e) => {
                  edit({ slug: e.target.value });
                }}
              />
            </span>
            {errors['slug'] && <span className="field-error">{errors['slug']}</span>}
          </label>
        </div>
        <label className="field-label">
          <span>
            وصف لمحركات البحث <span className="muted">(اختياري)</span>
          </span>
          <input
            value={form.description}
            maxLength={300}
            aria-invalid={Boolean(errors['description']) || undefined}
            onChange={(e) => {
              edit({ description: e.target.value });
            }}
          />
        </label>
        <p className="muted small">يظهر تحت عنوان الصفحة في نتائج البحث وعند مشاركة رابطها.</p>
      </section>

      <SectionsEditor
        sections={form.sections}
        onChange={(sections) => {
          edit({ sections });
        }}
        selected={selected}
        onSelect={setSelected}
        errors={errors}
        data={data}
        theme={data.theme}
        narrow={narrow}
        types={landingTypes}
        self={saved?.id}
      />
    </Layout>
  );
}
