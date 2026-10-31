import { useCallback, useEffect, useState, type SubmitEvent } from 'react';
import { Link } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { DuplicatePayments } from './DuplicatePayments';
import { dateTimeAr, healthGroups, sectionLabel, storeStatusLabel, type AdminHealth, type StoreStatus } from './lib';

type Summary = components['schemas']['AdminStoreSummary'];
interface Filter { query: string; status: StoreStatus | '' }

// fetchStores reads a page of the directory.
function fetchStores(f: Filter, after: string) {
  const query: { query?: string; status?: StoreStatus; cursor?: string } = {};
  if (f.query) query.query = f.query;
  if (f.status) query.status = f.status;
  if (after) query.cursor = after;
  return api.GET('/admin/v1/stores', { params: { query } });
}

// Overview is the console's home (the approved mockup): the platform's
// health, then the store directory with search and a status filter.
export function Overview() {
  const [health, setHealth] = useState<AdminHealth | null>(null);
  const [stores, setStores] = useState<Summary[]>([]);
  const [cursor, setCursor] = useState('');
  const [filter, setFilter] = useState<Filter>({ query: '', status: '' });
  const [error, setError] = useState('');

  const loadHealth = useCallback(async () => {
    const { data, error: err } = await api.GET('/admin/v1/health');
    if (data) setHealth(data);
    if (err) setError(problemMessage(err));
  }, []);

  const showStores = useCallback((r: Awaited<ReturnType<typeof fetchStores>>, after: string) => {
    if (r.error) {
      setError(problemMessage(r.error));
      return;
    }
    const page = r.data;
    setStores((prev) => (after ? [...prev, ...page.items] : page.items));
    setCursor(page.next_cursor ?? '');
  }, []);

  const loadStores = async (f: Filter, after: string) => {
    showStores(await fetchStores(f, after), after);
  };

  useEffect(() => {
    let live = true;
    void Promise.all([api.GET('/admin/v1/health'), fetchStores({ query: '', status: '' }, '')]).then(([h, st]) => {
      if (!live) return;
      if (h.data) setHealth(h.data);
      if (h.error) setError(problemMessage(h.error));
      showStores(st, '');
    });
    return () => {
      live = false;
    };
  }, [showStores]);

  function search(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const next = { query: field(form, 'query').trim(), status: field(form, 'status') as StoreStatus | '' };
    setFilter(next);
    void loadStores(next, '');
  }

  return (
    <ConsoleLayout>
      <div className="page-head">
        <h1>حالة المنصة</h1>
        {health && (
          <span className="muted small">
            آخر فحص: {dateTimeAr(health.checked_at)} —{' '}
            <button type="button" className="link-button" onClick={() => void loadHealth()}>
              تحديث
            </button>
          </span>
        )}
      </div>
      {error && <p role="alert">{error}</p>}
      {health && (
        <>
          <div className="health-checks">
            {[
              { label: 'قاعدة البيانات', ok: health.database },
              { label: 'Redis', ok: health.redis },
            ].map((c) => (
              <div key={c.label} className="card health-check">
                <span className={c.ok ? 'dot ok' : 'dot down'} aria-hidden="true" />
                <div>
                  <strong>{c.label}</strong>
                  <div className="muted small">{c.ok ? 'يعمل' : 'لا يرد'}</div>
                </div>
              </div>
            ))}
          </div>
          {health.errors.length > 0 && (
            <p className="warn-note reached">تعذّرت قراءة: {health.errors.map((e) => sectionLabel[e]).join('، ')}. باقي الصفحة يعمل.</p>
          )}
          <div className="health-groups">
            {healthGroups(health).map((g) => (
              <section key={g.title} className="card" aria-label={g.title}>
                <h2>{g.title}</h2>
                <dl className="facts">
                  {g.items.map((it) => (
                    <div key={it.label} className="fact">
                      <dt>{it.label}</dt>
                      <dd className={it.warn ? 'warn' : ''}>{it.value}</dd>
                    </div>
                  ))}
                </dl>
              </section>
            ))}
          </div>
        </>
      )}
      <DuplicatePayments />

      <section className="table-card" aria-labelledby="h-stores">
        <div className="card-head wrap">
          <h2 id="h-stores">المتاجر</h2>
          <form role="search" className="inline-form" onSubmit={search}>
            <label>
              <span className="field-label">بحث بالاسم أو العنوان</span>
              <input name="query" type="search" defaultValue={filter.query} maxLength={100} />
            </label>
            <label>
              <span className="field-label">الحالة</span>
              <select name="status" defaultValue={filter.status}>
                <option value="">الكل</option>
                <option value="active">نشط</option>
                <option value="suspended">موقوف</option>
                <option value="closed">مغلق</option>
              </select>
            </label>
            <button type="submit" className="button">
              بحث
            </button>
          </form>
        </div>
        <div className="scroll">
          <table>
            <thead>
              <tr>
                <th scope="col">المتجر</th>
                <th scope="col">العنوان</th>
                <th scope="col">الحالة</th>
                <th scope="col">العملة</th>
                <th scope="col">أنشئ</th>
              </tr>
            </thead>
            <tbody>
              {stores.map((s) => (
                <tr key={s.store.id}>
                  <td>
                    <Link to={`/stores/${s.store.id}`}>{s.store.name}</Link>
                  </td>
                  <td className="ltr muted">{s.primary_host}</td>
                  <td>
                    <span className={`status store-${s.store.status}`}>{storeStatusLabel[s.store.status]}</span>
                  </td>
                  <td>{s.store.currency}</td>
                  <td className="muted">{dateTimeAr(s.store.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {stores.length === 0 && <p className="empty flat">لا توجد متاجر.</p>}
        {cursor && (
          <p className="foot">
            <button type="button" className="button" onClick={() => void loadStores(filter, cursor)}>
              المزيد
            </button>
          </p>
        )}
      </section>
    </ConsoleLayout>
  );
}
