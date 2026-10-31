import { useCallback, useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { ConsoleLayout } from './ConsoleLayout';

type Detail = components['schemas']['AdminAgencyDetail'];

// fetchAgency reads the agency and the names of its stores.
async function fetchAgency(id: string) {
  const r = await api.GET('/admin/v1/agencies/{agency_id}', { params: { path: { agency_id: id } } });
  const names = new Map<string, string>();
  if (r.data) {
    const stores = await Promise.all(r.data.store_ids.map((s) => api.GET('/admin/v1/stores/{store_id}', { params: { path: { store_id: s } } })));
    for (const s of stores) if (s.data) names.set(s.data.store.id, s.data.store.name);
  }
  return { r, names };
}

// AgencyPage is one agency and its stores; a store leaves it onto the free
// plan (ADR-0059). Stores join from their own page.
export function AgencyPage() {
  const { agencyId = '' } = useParams();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [names, setNames] = useState<Map<string, string>>(new Map());
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = useCallback((x: Awaited<ReturnType<typeof fetchAgency>>) => {
    if (x.r.data) setDetail(x.r.data);
    if (x.r.error) setError(problemMessage(x.r.error));
    setNames(x.names);
  }, []);

  useEffect(() => {
    let live = true;
    void fetchAgency(agencyId).then((x) => {
      if (live) apply(x);
    });
    return () => {
      live = false;
    };
  }, [agencyId, apply]);

  async function remove(store: string) {
    const reason = window.prompt('سبب إخراج المتجر من الوكالة (ينتقل للباقة المجانية)');
    if (!reason) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/admin/v1/agencies/{agency_id}/stores/{store_id}/remove', {
      params: { path: { agency_id: agencyId, store_id: store } },
      body: { reason },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setNotice('خرج المتجر من الوكالة.');
    apply(await fetchAgency(agencyId));
  }

  const a = detail?.agency;
  return (
    <ConsoleLayout>
      <Link to="/agencies">→ الوكالات</Link>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      {a && (
        <>
          <div className="page-head">
            <div>
              <h1>{a.name}</h1>
              <p className="muted">
                {a.currency} · باقة <span className="ltr">{a.plan_code}</span> · {a.stores} متجر · فاتورة واحدة شهريًا
              </p>
            </div>
          </div>
          <section className="table-card" aria-labelledby="h-members">
            <div className="card-head wrap">
              <h2 id="h-members">متاجر الوكالة</h2>
              <span className="muted small">لضم متجر افتح صفحته من «الحالة والمتاجر».</span>
            </div>
            {detail.store_ids.length === 0 ? (
              <p className="empty flat">لا متاجر بعد.</p>
            ) : (
              <div className="scroll">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">المتجر</th>
                      <th scope="col">
                        <span className="visually-hidden">إجراء</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {detail.store_ids.map((s) => (
                      <tr key={s}>
                        <td>
                          <Link to={`/stores/${s}`}>{names.get(s) ?? s}</Link>
                        </td>
                        <td>
                          <button type="button" className="link-button danger" disabled={busy} onClick={() => void remove(s)}>
                            إخراج من الوكالة
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </ConsoleLayout>
  );
}
