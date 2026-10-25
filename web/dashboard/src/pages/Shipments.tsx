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

  const tabs: { status: ShipmentStatus | undefined; label: string }[] = [
    { status: undefined, label: 'الكل' },
    ...shipmentStatuses.map((st) => ({ status: st, label: shipmentLabel[st] })),
  ];

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <h1>الشحنات</h1>
      </div>
      <div className="tabs" role="tablist" aria-label="حالة الشحنة">
        {tabs.map((t) => (
          <button
            key={t.status ?? 'all'}
            type="button"
            role="tab"
            aria-selected={status === t.status}
            onClick={() => {
              if (status === t.status) return;
              setLoading(true);
              setParams(t.status ? { status: t.status } : {});
            }}
          >
            {t.label}
          </button>
        ))}
      </div>
      {error && <p role="alert">{error}</p>}
      <section className="table-card">
        {items.length > 0 && (
          <div className="scroll">
            <table className="orders">
              <thead>
                <tr>
                  <th>رقم التتبع</th>
                  <th>الشركة</th>
                  <th>التحصيل</th>
                  <th>الحالة</th>
                  <th>آخر تحديث</th>
                </tr>
              </thead>
              <tbody>
                {items.map((s) => (
                  <tr key={s.id}>
                    <td>
                      <Link to={`/stores/${storeId}/shipments/${s.id}`} dir="ltr" className="code">
                        {s.tracking_number ?? '—'}
                      </Link>
                    </td>
                    <td>{carrierLabel(s.carrier)}</td>
                    <td className="num">{s.cod_minor > 0 ? money(s.cod_minor, s.currency) : '—'}</td>
                    <td>
                      <span className={`status sh-${s.status}`}>{shipmentLabel[s.status]}</span>
                    </td>
                    <td className="muted">{dateTime(s.status_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {loading && <p role="status" className="foot">جارٍ التحميل…</p>}
        {!loading && items.length === 0 && !error && (
          <p className="empty flat">{status ? 'لا توجد شحنات بهذه الحالة.' : 'لا توجد شحنات بعد. أنشئ شحنة من صفحة طلب جاهز للشحن.'}</p>
        )}
        {!loading && items.length > 0 && (
          <div className="foot">
            <span>عرض {items.length} شحنة</span>
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
