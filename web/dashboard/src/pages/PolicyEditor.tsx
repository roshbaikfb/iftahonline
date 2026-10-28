import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { PageActions } from '../components/PageActions';
import { isPolicy, pageStatus, policies } from '../lib/pages';
import { problemMessage } from '../lib/problem';
import { fieldErrors as toFieldErrors, type FieldError } from '../lib/sections';
import { storefrontURL } from '../lib/storefront';

type Page = components['schemas']['Page'];

const maxBody = 20000;

// PolicyEditor writes one of the store's policies (U6c): plain text in
// paragraphs, saved as a draft or published. A published policy is
// linked from the bottom of every shop page.
export function PolicyEditor() {
  const { storeId = '', policy = '' } = useParams();
  const info = policies.find((p) => p.slug === policy);
  const [saved, setSaved] = useState<Page | null>(null);
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [shop, setShop] = useState('');

  useEffect(() => {
    if (!isPolicy(policy)) return;
    let live = true;
    const path = { params: { path: { store_id: storeId, policy } } };
    void api.GET('/v1/stores/{store_id}/storefront/policies/{policy}', path).then(({ data, error: err }) => {
      if (!live) return;
      if (!data) {
        setError(problemMessage(err));
        return;
      }
      setSaved(data);
      setTitle(data.title);
      setBody(data.body);
    });
    void api.GET('/v1/stores/{store_id}', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setShop(storefrontURL(data.slug));
    });
    return () => {
      live = false;
    };
  }, [storeId, policy]);

  async function save(status: 'draft' | 'published') {
    if (!saved || !isPolicy(policy)) return;
    setBusy(true);
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/storefront/policies/{policy}', {
      params: { path: { store_id: storeId, policy } },
      body: { title, body, status, version: saved.version },
    });
    setBusy(false);
    if (err) {
      const fields = (err as { errors?: FieldError[] }).errors ?? [];
      setErrors(toFieldErrors(fields));
      setError(fields.length ? 'لم تُحفظ: راجع الحقول المعلّمة.' : problemMessage(err));
      return;
    }
    setSaved(data);
    setDirty(false);
    setErrors({});
    setError('');
    setNotice(status === 'published' ? 'نُشرت. تظهر في أسفل صفحات متجرك.' : 'حُفظت كمسودة. لا يراها العملاء حتى تنشرها.');
  }

  const back = (
    <Link to={`/stores/${storeId}/pages`} className="back">
      → الصفحات
    </Link>
  );
  if (!info) {
    return (
      <Layout storeId={storeId}>
        {back}
        <p role="alert">هذه السياسة غير موجودة.</p>
      </Layout>
    );
  }
  if (!saved) {
    return (
      <Layout storeId={storeId}>
        {back}
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const st = pageStatus(saved);

  return (
    <Layout storeId={storeId}>
      <div className="page-head editor-head">
        <div>
          {back}
          <div className="title-line">
            <h1>{saved.title || info.title}</h1>
            <span className={`status ${st.className}`}>{st.label}</span>
            {dirty && saved.version > 0 && <span className="status s-pending">تغييرات لم تُحفظ</span>}
          </div>
          <p className="muted">{info.hint}</p>
        </div>
        <PageActions
          status={saved.version === 0 ? null : saved.status}
          dirty={dirty}
          busy={busy}
          viewURL={shop ? `${shop}/policies/${info.slug}` : undefined}
          onSave={(s) => void save(s)}
        />
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <section className="card editor-card policy-card" aria-label={info.title}>
        <label className="field-label">
          <span>
            العنوان <span className="muted">(اختياري)</span>
          </span>
          <input
            value={title}
            maxLength={200}
            placeholder={info.title}
            aria-invalid={Boolean(errors['title']) || undefined}
            onChange={(e) => {
              setTitle(e.target.value);
              setDirty(true);
              setNotice('');
            }}
          />
          {errors['title'] && <span className="field-error">{errors['title']}</span>}
        </label>
        <label className="field-label">
          النص
          <textarea
            rows={18}
            value={body}
            maxLength={maxBody}
            aria-invalid={Boolean(errors['body']) || undefined}
            onChange={(e) => {
              setBody(e.target.value);
              setDirty(true);
              setNotice('');
            }}
          />
          {errors['body'] && <span className="field-error">{errors['body']}</span>}
        </label>
        <p className="muted small">
          نص عادي: افصل بين الفقرات بسطر فارغ. {body.length} من {maxBody} حرف.
        </p>
      </section>
    </Layout>
  );
}
