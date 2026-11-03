import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useSearchParams } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { count, dateTimeAr, fetchStores, initial, storeStatusLabel, type AdminHealth, type StoreStatus } from './lib';

type Summary = components['schemas']['AdminStoreSummary'];

// StoreCell is a store's mark, name and address, linking to its page.
export function StoreCell({ summary }: { summary: Summary }) {
  return (
    <Link className="store-cell" to={`/stores/${summary.store.id}`}>
      <span className="store-mark" aria-hidden="true">
        {initial(summary.store.name)}
      </span>
      <span>
        <strong>{summary.store.name}</strong>
        <small className="ltr">{summary.primary_host}</small>
      </span>
    </Link>
  );
}

export function StoreStatusPill({ status }: { status: StoreStatus }) {
  return <span className={`status store-${status}`}>{storeStatusLabel[status]}</span>;
}

const tabs: { status: StoreStatus | ''; label: string; count: (s: NonNullable<AdminHealth['stores']>) => number }[] = [
  { status: '', label: 'الكل', count: (s) => s.total },
  { status: 'active', label: 'نشطة', count: (s) => s.active },
  { status: 'suspended', label: 'موقوفة', count: (s) => s.suspended },
  { status: 'closed', label: 'مغلقة', count: (s) => s.closed },
];

const isStatus = (v: string | null): v is StoreStatus => v === 'active' || v === 'suspended' || v === 'closed';

// StoresPage is the console's store directory (the approved mockup):
// newest first, by status in tabs with their counts, searchable by name
// or address. The tab and the search live in the URL.
export function StoresPage() {
  const [params, setParams] = useSearchParams();
  const raw = params.get('status');
  const status: StoreStatus | '' = isStatus(raw) ? raw : '';
  const query = params.get('q') ?? '';
  const [stores, setStores] = useState<Summary[]>([]);
  const [cursor, setCursor] = useState('');
  const [counts, setCounts] = useState<AdminHealth['stores']>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/health').then(({ data }) => {
      if (live && data?.stores) setCounts(data.stores);
    });
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    let live = true;
    void fetchStores({ query, status }).then((r) => {
      if (!live) return;
      setLoading(false);
      if (r.error) {
        setError(problemMessage(r.error));
        return;
      }
      setError('');
      setStores(r.data.items);
      setCursor(r.data.next_cursor ?? '');
    });
    return () => {
      live = false;
    };
  }, [query, status]);

  function update(change: { status?: StoreStatus | ''; q?: string }) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(change)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    setLoading(true);
    setParams(p);
  }

  function search(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    update({ q: field(new FormData(e.currentTarget), 'q').trim() });
  }

  async function more() {
    const r = await fetchStores({ query, status }, cursor);
    if (r.error) {
      setError(problemMessage(r.error));
      return;
    }
    setStores((xs) => [...xs, ...r.data.items]);
    setCursor(r.data.next_cursor ?? '');
  }

  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>المتاجر</h1>
          <p className="muted">كل متاجر المنصة، الأحدث أولًا. افتح متجرًا لإيقافه أو تسوية فواتيره أو الدخول كتاجر.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <section className="table-card stores-dir" aria-label="المتاجر">
        <div className="stores-bar">
          <div className="tabs" role="tablist" aria-label="الحالة">
            {tabs.map((t) => (
              <button
                key={t.label}
                type="button"
                role="tab"
                aria-selected={status === t.status}
                onClick={() => {
                  update({ status: t.status });
                }}
              >
                {t.label}
                {counts && ' '}
                {counts && <span className="count">{count(t.count(counts))}</span>}
              </button>
            ))}
          </div>
          <form role="search" className="stores-search" onSubmit={search} key={query}>
            <label className="grow">
              <span className="visually-hidden">ابحث باسم المتجر أو عنوانه</span>
              <input name="q" type="search" defaultValue={query} placeholder="ابحث باسم المتجر أو عنوانه" maxLength={100} />
            </label>
            <button type="submit" className="secondary">
              بحث
            </button>
          </form>
        </div>
        {loading ? (
          <p className="empty flat">جارٍ التحميل…</p>
        ) : stores.length === 0 ? (
          <p className="empty flat">{query ? 'لا يوجد متجر بهذا الاسم أو العنوان.' : 'لا توجد متاجر هنا.'}</p>
        ) : (
          <div className="scroll">
            <table className="stores-table">
              <thead>
                <tr>
                  <th scope="col">المتجر</th>
                  <th scope="col">الحالة</th>
                  <th scope="col">العملة</th>
                  <th scope="col">أُنشئ</th>
                </tr>
              </thead>
              <tbody>
                {stores.map((s) => (
                  <tr key={s.store.id}>
                    <td>
                      <StoreCell summary={s} />
                    </td>
                    <td>
                      <StoreStatusPill status={s.store.status} />
                    </td>
                    <td className="muted">{s.store.currency}</td>
                    <td className="muted">{dateTimeAr(s.store.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {cursor && !loading && (
          <div className="foot center">
            <button type="button" className="secondary" onClick={() => void more()}>
              عرض المزيد
            </button>
          </div>
        )}
      </section>
    </ConsoleLayout>
  );
}
