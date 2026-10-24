import { useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime, money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { carrierLabel, shipmentLabel, shipmentStatuses, type Shipment, type ShipmentStatus } from '../lib/shipments';

async function fetchPage(storeId: string, status: ShipmentStatus | undefined, cursor?: string) {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/shipping/shipments', {
      params: {
        path: { store_id: storeId },
        query: {
          limit: 50,
          ...(status ? { status } : {}),
          ...(cursor ? { cursor } : {}),
        },
      },
    });
    if (error) return { items: [] as Shipment[], error: problemMessage(error) };
    return { items: data.items, next: data.next_cursor };
  } catch (err) {
    return { items: [] as Shipment[], error: problemMessage(err) };
  }
}

// Shipments lists the store's shipments, newest first, filtered by
// status (in the URL, so a filtered list can be shared).
export function Shipments() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const raw = params.get('status') ?? '';
  const status = (shipmentStatuses as string[]).includes(raw) ? (raw as ShipmentStatus) : undefined;
  const [items, setItems] = useState<Shipment[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let live = true;
    void fetchPage(storeId, status).then((p) => {
      if (!live) return;
      setItems(p.items);
      setNext(p.next);
      setError(p.error ?? '');
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [storeId, status]);

  async function loadMore() {
    if (!next) return;
    setLoading(true);
    const p = await fetchPage(storeId, status, next);
    setItems((prev) => [...prev, ...p.items]);
    setNext(p.next);
    setError(p.error ?? '');
    setLoading(false);
  }

  return (
    <Layout storeId={storeId}>
      <h1>الشحنات</h1>
      <div className="filters">
        <label>
          الحالة
          <select
            value={status ?? ''}
            onChange={(e) => {
              setLoading(true);
              setParams(e.target.value ? { status: e.target.value } : {});
            }}
          >
            <option value="">الكل</option>
            {shipmentStatuses.map((s) => (
              <option key={s} value={s}>
                {shipmentLabel[s]}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error && <p role="alert">{error}</p>}
      {!loading && items.length === 0 && !error && <p className="empty">لا توجد شحنات. أنشئ شحنة من صفحة طلب جاهز للشحن.</p>}
      {items.length > 0 && (
        <table className="orders">
          <thead>
            <tr>
              <th>رقم التتبع</th>
              <th>الشركة</th>
              <th>الحالة</th>
              <th>التحصيل</th>
              <th>آخر تحديث</th>
            </tr>
          </thead>
          <tbody>
            {items.map((s) => (
              <tr key={s.id}>
                <td dir="ltr">
                  <Link to={`/stores/${storeId}/shipments/${s.id}`}>{s.tracking_number ?? '—'}</Link>
                </td>
                <td>{carrierLabel(s.carrier)}</td>
                <td>
                  <span className={`status sh-${s.status}`}>{shipmentLabel[s.status]}</span>
                </td>
                <td>{money(s.cod_minor, s.currency)}</td>
                <td>{dateTime(s.status_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {next && (
        <button type="button" disabled={loading} onClick={() => void loadMore()}>
          المزيد
        </button>
      )}
    </Layout>
  );
}
