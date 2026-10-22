import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime, money, statusLabel, statuses, type Order, type OrderStatus } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

// The filters live in the URL, so a filtered list can be shared or
// reloaded.
const filterKeys = ['status', 'phone', 'from', 'to'] as const;

// dayStart is the start of a local calendar day; the "to" filter is
// exclusive, so it takes the start of the day after.
function dayStart(day: string, plusDays = 0): string {
  const d = new Date(`${day}T00:00:00`);
  d.setDate(d.getDate() + plusDays);
  return d.toISOString();
}

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

// Orders lists the store's orders, newest first, with filters, a page at
// a time; pending ones can be selected and confirmed in bulk, and the
// current filter exported as CSV.
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
    for (const k of filterKeys) {
      const v = field(form, k).trim();
      if (v) nextParams.set(k, v);
    }
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

  return (
    <Layout storeId={storeId}>
      <h1>الطلبات</h1>
      <form className="filters" onSubmit={applyFilters} aria-label="تصفية الطلبات" key={paramsKey}>
        <label>
          الحالة
          <select name="status" defaultValue={params.get('status') ?? ''}>
            <option value="">كل الحالات</option>
            {statuses.map((s) => (
              <option key={s} value={s}>
                {statusLabel[s]}
              </option>
            ))}
          </select>
        </label>
        <label>
          رقم الموبايل
          <input name="phone" type="tel" defaultValue={params.get('phone') ?? ''} dir="ltr" />
        </label>
        <label>
          من
          <input name="from" type="date" defaultValue={params.get('from') ?? ''} />
        </label>
        <label>
          إلى
          <input name="to" type="date" defaultValue={params.get('to') ?? ''} />
        </label>
        <button type="submit">تصفية</button>
        <a className="button" href={exportURL} download>
          تصدير CSV
        </a>
      </form>

      <div className="actions">
        <button type="button" disabled={selected.size === 0} onClick={() => void confirmSelected()}>
          تأكيد المحدد ({selected.size})
        </button>
      </div>
      {notice && <p role="status">{notice}</p>}
      {error && <p role="alert">{error}</p>}

      <table className="orders">
        <thead>
          <tr>
            <th>
              <input
                type="checkbox"
                aria-label="تحديد كل الطلبات قيد الانتظار"
                checked={pending.length > 0 && pending.every((o) => selected.has(o.id))}
                onChange={(e) => {
                  setSelected(e.target.checked ? new Set(pending.map((o) => o.id)) : new Set());
                }}
              />
            </th>
            <th>الطلب</th>
            <th>التاريخ</th>
            <th>العميل</th>
            <th>الموبايل</th>
            <th>المحافظة</th>
            <th>الإجمالي</th>
            <th>الحالة</th>
          </tr>
        </thead>
        <tbody>
          {items.map((o) => (
            <tr key={o.id}>
              <td>
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
                <Link to={`/stores/${storeId}/orders/${o.id}`} dir="ltr">
                  #{o.code}
                </Link>
              </td>
              <td>{dateTime(o.created_at)}</td>
              <td>{o.name}</td>
              <td dir="ltr">{o.phone}</td>
              <td dir="ltr">{o.governorate}</td>
              <td>{money(o.total_minor, o.currency)}</td>
              <td>
                <span className={`status s-${o.status}`}>{statusLabel[o.status]}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p role="status">جارٍ التحميل…</p>}
      {!loading && items.length === 0 && !error && <p className="empty">لا توجد طلبات.</p>}
      {next && !loading && (
        <button type="button" onClick={() => void loadMore()}>
          تحميل المزيد
        </button>
      )}
    </Layout>
  );
}
