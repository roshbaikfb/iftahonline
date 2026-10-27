import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { SamplesBanner } from '../components/SamplesBanner';
import {
  dateTime,
  dayStart,
  money,
  paymentPill,
  selectedLabel,
  statusLabel,
  statuses,
  tabStatuses,
  type Order,
  type OrderCounts,
  type OrderStatus,
} from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

// The filters live in the URL, so a filtered list can be shared or
// reloaded.
const filterKeys = ['status', 'phone', 'from', 'to'] as const;

interface ListQuery {
  status?: OrderStatus;
  phone?: string;
  from?: string;
  to?: string;
  cursor?: string;
  limit?: number;
}

function listQuery(params: URLSearchParams, cursor?: string): ListQuery {
  const q: ListQuery = { limit: 50 };
  const status = params.get('status');
  if (status && (statuses as string[]).includes(status)) q.status = status as OrderStatus;
  const phone = params.get('phone');
  if (phone) q.phone = phone;
  const from = params.get('from');
  if (from) q.from = dayStart(from);
  const to = params.get('to');
  if (to) q.to = dayStart(to, 1);
  if (cursor) q.cursor = cursor;
  return q;
}

interface Page {
  items: Order[];
  next?: string | undefined;
  error?: string;
}

async function fetchPage(storeId: string, q: ListQuery): Promise<Page> {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/orders', { params: { path: { store_id: storeId }, query: q } });
    if (error) return { items: [], error: problemMessage(error) };
    return { items: data.items, next: data.next_cursor };
  } catch (err) {
    return { items: [], error: problemMessage(err) };
  }
}

// Orders lists the store's orders (the approved mockup): status tabs with
// their counts, a phone and date filter, the table in a card, a page at a
// time. Pending orders can be selected and confirmed in bulk from the bar
// that appears; the current filter exports as CSV.
export function Orders() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const [items, setItems] = useState<Order[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [reloads, setReloads] = useState(0);
  const [counts, setCounts] = useState<OrderCounts | null>(null);
  const [places, setPlaces] = useState<Map<string, string>>(new Map());
  const paramsKey = params.toString();

  useEffect(() => {
    let live = true;
    void fetchPage(storeId, listQuery(new URLSearchParams(paramsKey))).then((p) => {
      if (!live) return;
      setItems(p.items);
      setNext(p.next);
      setError(p.error ?? '');
      setSelected(new Set());
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [storeId, paramsKey, reloads]);

  // The tabs' counts follow every change the list makes.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/orders/counts', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setCounts(data);
    });
    return () => {
      live = false;
    };
  }, [storeId, reloads]);

  // Governorates by name, from the store's shipping areas; the code stays
  // when they cannot be read.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/shipping', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setPlaces(new Map(data.areas.map((a) => [a.code, a.name_ar])));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function loadMore() {
    if (!next) return;
    setLoading(true);
    const p = await fetchPage(storeId, listQuery(params, next));
    setItems((prev) => [...prev, ...p.items]);
    setNext(p.next);
    setError(p.error ?? '');
    setLoading(false);
  }

  function applyFilters(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const nextParams = new URLSearchParams();
    const status = params.get('status');
    if (status) nextParams.set('status', status);
    for (const k of filterKeys) {
      if (k === 'status') continue;
      const v = field(form, k).trim();
      if (v) nextParams.set(k, v);
    }
    setLoading(true);
    setParams(nextParams);
  }

  function pickTab(status: OrderStatus | '') {
    const nextParams = new URLSearchParams(params);
    if (status) nextParams.set('status', status);
    else nextParams.delete('status');
    setLoading(true);
    setParams(nextParams);
  }

  async function confirmSelected() {
    setNotice('');
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/orders/transitions', {
      params: { path: { store_id: storeId } },
      body: { order_ids: [...selected], status: 'confirmed', note: '' },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    const ok = data.results.filter((r) => r.ok).length;
    const failed = data.results.length - ok;
    setNotice(failed === 0 ? `تم تأكيد ${String(ok)} طلب` : `تم تأكيد ${String(ok)} طلب، وتعذّر ${String(failed)}`);
    setLoading(true);
    setReloads((n) => n + 1);
  }

  function toggle(id: string, on: boolean) {
    const s = new Set(selected);
    if (on) s.add(id);
    else s.delete(id);
    setSelected(s);
  }

  const q = listQuery(params);
  const exportQuery = new URLSearchParams();
  if (q.status) exportQuery.set('status', q.status);
  if (q.from) exportQuery.set('from', q.from);
  if (q.to) exportQuery.set('to', q.to);
  const exportURL = `/v1/stores/${storeId}/orders/export${exportQuery.size ? `?${exportQuery.toString()}` : ''}`;
  const pending = items.filter((o) => o.status === 'pending');
  const current = q.status ?? '';
  const total = counts ? tabStatuses.reduce((n, st) => n + counts[st], 0) : undefined;
  const tabs: { status: OrderStatus | ''; label: string; count: number | undefined }[] = [
    { status: '', label: 'الكل', count: total },
    ...tabStatuses.map((st) => ({ status: st, label: statusLabel[st], count: counts?.[st] })),
  ];

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <h1>الطلبات</h1>
        <a className="button" href={exportURL} download>
          تصدير CSV
        </a>
      </div>
      <SamplesBanner storeId={storeId} />

      <div className="tabs" role="tablist" aria-label="حالة الطلب">
        {tabs.map((t) => (
          <button
            key={t.status || 'all'}
            type="button"
            role="tab"
            aria-selected={current === t.status}
            onClick={() => {
              pickTab(t.status);
            }}
          >
            <span>{t.label}</span>
            {t.count !== undefined && (
              <>
                {' '}
                <span className="count">{t.count}</span>
              </>
            )}
          </button>
        ))}
      </div>

      <form className="filters" onSubmit={applyFilters} aria-label="تصفية الطلبات" key={paramsKey}>
        <label className="grow">
          رقم الموبايل
          <input name="phone" type="tel" defaultValue={params.get('phone') ?? ''} dir="ltr" placeholder="01012345678" />
        </label>
        <label>
          من
          <input name="from" type="date" defaultValue={params.get('from') ?? ''} />
        </label>
        <label>
          إلى
          <input name="to" type="date" defaultValue={params.get('to') ?? ''} />
        </label>
        <button type="submit" className="secondary">
          تصفية
        </button>
      </form>

      {selected.size > 0 && (
        <div className="bulk" role="region" aria-label="إجراءات المحدد">
          <strong>{selectedLabel(selected.size)}</strong>
          <div className="actions">
            <button type="button" className="success" onClick={() => void confirmSelected()}>
              تأكيد
            </button>
            <button
              type="button"
              className="link"
              onClick={() => {
                setSelected(new Set());
              }}
            >
              إلغاء التحديد
            </button>
          </div>
        </div>
      )}
      {notice && <p role="status" className="ok-note">{notice}</p>}
      {error && <p role="alert">{error}</p>}

      <section className="table-card">
        <div className="scroll">
          <table className="orders">
            <thead>
              <tr>
                <th className="pick">
                  <input
                    type="checkbox"
                    aria-label="تحديد كل الطلبات قيد الانتظار"
                    disabled={pending.length === 0}
                    checked={pending.length > 0 && pending.every((o) => selected.has(o.id))}
                    onChange={(e) => {
                      setSelected(e.target.checked ? new Set(pending.map((o) => o.id)) : new Set());
                    }}
                  />
                </th>
                <th>الطلب</th>
                <th>العميل</th>
                <th>المحافظة</th>
                <th>الإجمالي</th>
                <th>الدفع</th>
                <th>الحالة</th>
                <th>التاريخ</th>
              </tr>
            </thead>
            <tbody>
              {items.map((o) => {
                const pay = paymentPill(o);
                return (
                  <tr key={o.id} className={selected.has(o.id) ? 'picked' : undefined}>
                    <td className="pick">
                      {o.status === 'pending' && (
                        <input
                          type="checkbox"
                          aria-label={`تحديد الطلب ${o.code}`}
                          checked={selected.has(o.id)}
                          onChange={(e) => {
                            toggle(o.id, e.target.checked);
                          }}
                        />
                      )}
                    </td>
                    <td>
                      <Link to={`/stores/${storeId}/orders/${o.id}`} dir="ltr" className="code">
                        #{o.code}
                      </Link>
                    </td>
                    <td>
                      {o.name}
                      <small className="sub" dir="ltr">
                        {o.phone}
                      </small>
                    </td>
                    <td>{places.get(o.governorate) ?? o.governorate}</td>
                    <td className="num">{money(o.total_minor, o.currency)}</td>
                    <td>
                      <span className={`status ${pay.cls}`}>{pay.label}</span>
                    </td>
                    <td>
                      <span className={`status s-${o.status}`}>{statusLabel[o.status]}</span>
                    </td>
                    <td className="muted">{dateTime(o.created_at)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {loading && <p role="status" className="foot">جارٍ التحميل…</p>}
        {!loading && items.length === 0 && !error && <p className="empty flat">لا توجد طلبات هنا.</p>}
        {!loading && items.length > 0 && (
          <div className="foot">
            <span>عرض {items.length} طلب</span>
            {next && (
              <button type="button" className="secondary" onClick={() => void loadMore()}>
                تحميل المزيد
              </button>
            )}
          </div>
        )}
      </section>
    </Layout>
  );
}
