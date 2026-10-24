import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime, money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { cancellable, carrierLabel, shipmentLabel, sourceLabel } from '../lib/shipments';

type Detail = components['schemas']['ShipmentDetail'];

async function fetchShipment(storeId: string, id: string): Promise<{ detail?: Detail; error?: string }> {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/shipping/shipments/{shipment_id}', {
      params: { path: { store_id: storeId, shipment_id: id } },
    });
    return error ? { error: problemMessage(error) } : { detail: data };
  } catch (err) {
    return { error: problemMessage(err) };
  }
}

// ShipmentDetail shows one shipment: its carrier, tracking number, what
// the courier collects, its timeline, the label to print, and cancel.
export function ShipmentDetail() {
  const { storeId = '', shipmentId = '' } = useParams();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void fetchShipment(storeId, shipmentId).then((r) => {
      if (!live) return;
      if (r.detail) setDetail(r.detail);
      setError(r.error ?? '');
    });
    return () => {
      live = false;
    };
  }, [storeId, shipmentId, reloads]);

  async function label() {
    setError('');
    const { data, error: err } = await api.GET('/v1/stores/{store_id}/shipping/shipments/{shipment_id}/label', {
      params: { path: { store_id: storeId, shipment_id: shipmentId } },
      parseAs: 'blob',
    });
    if (err || !(data instanceof Blob)) {
      setError(err ? problemMessage(err) : 'تعذّر تحميل البوليصة');
      return;
    }
    const url = URL.createObjectURL(data);
    globalThis.open(url, '_blank', 'noopener');
    setTimeout(() => {
      URL.revokeObjectURL(url);
    }, 60_000);
  }

  async function cancel() {
    if (!globalThis.confirm('إلغاء الشحنة؟ ترفض شركة الشحن الإلغاء بعد استلام الطرد.')) return;
    setBusy(true);
    const { error: err } = await api.POST('/v1/stores/{store_id}/shipping/shipments/{shipment_id}/cancel', {
      params: { path: { store_id: storeId, shipment_id: shipmentId } },
    });
    setBusy(false);
    if (err) setError(problemMessage(err));
    setReloads((n) => n + 1);
  }

  const s = detail?.shipment;
  return (
    <Layout storeId={storeId}>
      <p>
        <Link to={`/stores/${storeId}/shipments`}>→ الشحنات</Link>
      </p>
      {error && <p role="alert">{error}</p>}
      {s && (
        <article className="order">
          <h1>
            شحنة <span dir="ltr">{s.tracking_number ?? '—'}</span>{' '}
            <span className={`status sh-${s.status}`}>{shipmentLabel[s.status]}</span>
          </h1>
          <dl>
            <dt>الشركة</dt>
            <dd>{carrierLabel(s.carrier)}</dd>
            <dt>الطلب</dt>
            <dd>
              <Link to={`/stores/${storeId}/orders/${s.order_id}`}>عرض الطلب</Link>
            </dd>
            <dt>يحصّل المندوب</dt>
            <dd>{money(s.cod_minor, s.currency)}</dd>
            {s.failure && (
              <>
                <dt>سبب الرفض</dt>
                <dd>{s.failure}</dd>
              </>
            )}
            {s.cancel_requested_at && (
              <>
                <dt>طُلب الإلغاء</dt>
                <dd>{dateTime(s.cancel_requested_at)}</dd>
              </>
            )}
          </dl>
          <div className="moves">
            {s.label_ready && (
              <button type="button" onClick={() => void label()}>
                طباعة البوليصة
              </button>
            )}
            {cancellable(s) && (
              <button type="button" className="danger" disabled={busy} onClick={() => void cancel()}>
                إلغاء الشحنة
              </button>
            )}
          </div>
          <section>
            <h2>السجل</h2>
            <ol className="timeline">
              {detail.events.map((e) => (
                <li key={e.id}>
                  {dateTime(e.at)} — {shipmentLabel[e.status]} <small>({sourceLabel[e.source]})</small>
                  {e.note && <q>{e.note}</q>}
                </li>
              ))}
            </ol>
          </section>
        </article>
      )}
    </Layout>
  );
}
