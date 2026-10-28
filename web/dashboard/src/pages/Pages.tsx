import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { maxLandingPages, pageStatus, policies } from '../lib/pages';
import { problemMessage } from '../lib/problem';

type Page = components['schemas']['Page'];

// Pages lists the store's landing pages and its policies (U6c): each
// with its address and whether shoppers can see it.
export function Pages() {
  const { storeId = '' } = useParams();
  const [landing, setLanding] = useState<Page[] | null>(null);
  const [written, setWritten] = useState<Page[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void api.GET('/v1/stores/{store_id}/storefront/pages', path).then(({ data, error: err }) => {
      if (!live) return;
      if (data) setLanding(data.items);
      else setError(problemMessage(err));
    });
    void api.GET('/v1/stores/{store_id}/storefront/policies', path).then(({ data }) => {
      if (live && data) setWritten(data.items);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  const at = (p: string) => `/stores/${storeId}/${p}`;

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>الصفحات</h1>
          <p className="muted">صفحات هبوط لحملاتك وإعلاناتك، وسياسات متجرك التي تظهر في أسفل كل صفحة.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <section className="card" aria-labelledby="h-landing">
        <div className="card-head bare">
          <h2 id="h-landing">صفحات الهبوط</h2>
          {landing && landing.length < maxLandingPages && (
            <Link className="button primary small" to={at('pages/new')}>
              + صفحة جديدة
            </Link>
          )}
        </div>
        {!landing ? (
          !error && <p role="status">جارٍ التحميل…</p>
        ) : landing.length === 0 ? (
          <p className="muted">لا توجد صفحات بعد. أنشئ صفحة لمنتج واحد تروّج له في إعلان، أو لعرض موسمي.</p>
        ) : (
          <ul className="page-rows">
            {landing.map((p) => {
              const st = pageStatus(p);
              return (
                <li key={p.id}>
                  <div className="grow">
                    <Link to={at(`pages/${p.id ?? ''}`)}>
                      <strong>{p.title}</strong>
                    </Link>
                    <span className="muted small" dir="ltr">
                      /pages/{p.slug}
                    </span>
                  </div>
                  <span className={`status ${st.className}`}>{st.label}</span>
                </li>
              );
            })}
          </ul>
        )}
        {landing && landing.length >= maxLandingPages && <p className="muted">وصلت إلى الحد: {maxLandingPages} صفحة. احذف صفحة قديمة لتضيف غيرها.</p>}
      </section>

      <section className="card" aria-labelledby="h-policies">
        <h2 id="h-policies">السياسات</h2>
        <ul className="page-rows">
          {policies.map((pol) => {
            const p = written.find((x) => x.slug === pol.slug);
            const st = pageStatus(p ?? { status: 'draft', version: 0 });
            return (
              <li key={pol.slug}>
                <div className="grow">
                  <Link to={at(`pages/policies/${pol.slug}`)}>
                    <strong>{(p?.title ?? '') || pol.title}</strong>
                  </Link>
                  <span className="muted small">{pol.hint}</span>
                </div>
                <span className={`status ${st.className}`}>{st.label}</span>
              </li>
            );
          })}
        </ul>
        <p className="muted small">تظهر في أسفل صفحات المتجر بعد نشرها فقط.</p>
      </section>
    </Layout>
  );
}
