import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { describe, when, who, type AuditEntry } from '../lib/activity';
import { problemMessage } from '../lib/problem';
import { useRole } from '../lib/settings';

const pageSize = 50;

// ActivityLog is «سجل النشاط» (D6, the approved mockup): who changed what
// in the store, newest first, in Arabic, for its owner. Older entries come
// a page at a time.
export function ActivityLog() {
  const { storeId = '' } = useParams();
  const owner = useRole(storeId) === 'owner';
  const [items, setItems] = useState<AuditEntry[] | null>(null);
  const [next, setNext] = useState<string | undefined>();
  const [names, setNames] = useState<ReadonlyMap<string, string>>(new Map());
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!owner) return undefined;
    let live = true;
    const path = { store_id: storeId };
    void Promise.all([
      api.GET('/v1/stores/{store_id}/audit-log', { params: { path, query: { limit: pageSize } } }),
      api.GET('/v1/stores/{store_id}/members', { params: { path } }),
    ]).then(([log, members]) => {
      if (!live) return;
      if (members.data) setNames(new Map(members.data.items.map((m) => [m.user_id, m.name])));
      if (log.data) {
        setItems(log.data.items);
        setNext(log.data.next_cursor);
      } else setError(problemMessage(log.error));
    });
    return () => {
      live = false;
    };
  }, [storeId, owner]);

  async function older() {
    if (!next) return;
    setBusy(true);
    const { data, error: err } = await api.GET('/v1/stores/{store_id}/audit-log', {
      params: { path: { store_id: storeId }, query: { limit: pageSize, cursor: next } },
    });
    setBusy(false);
    if (!data) {
      setError(problemMessage(err));
      return;
    }
    setItems((now) => [...(now ?? []), ...data.items]);
    setNext(data.next_cursor);
  }

  const now = new Date();
  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>سجل النشاط</h1>
          <p className="muted">من غيّر ماذا في متجرك: المنتجات والطلبات والإعدادات والفريق. للمالك وحده.</p>
        </div>
      </div>
      {!owner && <p className="notice">سجل النشاط لصاحب المتجر وحده.</p>}
      {owner && (
        <section className="card activity" aria-label="الأحداث">
          {items === null && !error && <p role="status">جارٍ التحميل…</p>}
          {items !== null && items.length === 0 && <p className="empty">لا نشاط بعد.</p>}
          {items !== null && items.length > 0 && (
            <div className="table-wrap">
              <table className="orders">
                <thead>
                  <tr>
                    <th scope="col">الوقت</th>
                    <th scope="col">العضو</th>
                    <th scope="col">ماذا حدث</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((e) => {
                    const said = describe(e, storeId);
                    return (
                      <tr key={e.id}>
                        <td className="muted nowrap">
                          <time dateTime={e.occurred_at}>{when(e.occurred_at, now)}</time>
                        </td>
                        <td>{who(e, names)}</td>
                        <td>
                          {said.text}
                          {said.ltr && (
                            <>
                              {' '}
                              <bdi dir="ltr">{said.ltr}</bdi>
                            </>
                          )}
                          {said.link && (
                            <>
                              {' '}
                              <Link to={said.link.to}>
                                <bdi>{said.link.label}</bdi>
                              </Link>
                            </>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
          {error && <p role="alert">{error}</p>}
          {next && (
            <div className="activity-more">
              <button type="button" className="secondary" disabled={busy} onClick={() => void older()}>
                {busy ? 'جارٍ التحميل…' : 'عرض الأقدم'}
              </button>
            </div>
          )}
        </section>
      )}
    </Layout>
  );
}
