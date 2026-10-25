import { useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { carrierLabel, live, shipmentLabel, type Shipment } from '../lib/shipments';

type Connection = components['schemas']['CarrierConnection'];

// OrderShipping is an order's shipments, and, for an order ready to ship
// with none live, handing it to a connected carrier (ADR-0027). The
// carrier creates the shipment in the background; the order then moves
// as the parcel does.
export function OrderShipping({
  storeId,
  orderId,
  status,
  onShipped,
}: {
  storeId: string;
  orderId: string;
  status: components['schemas']['OrderStatus'];
  onShipped: () => void;
}) {
  const [shipments, setShipments] = useState<Shipment[]>([]);
  const [connections, setConnections] = useState<Connection[]>([]);
  const [connection, setConnection] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let alive = true;
    void Promise.all([
      api.GET('/v1/stores/{store_id}/shipping/shipments', {
        params: { path: { store_id: storeId }, query: { order_id: orderId } },
      }),
      api.GET('/v1/stores/{store_id}/shipping/carriers', {
        params: { path: { store_id: storeId } },
      }),
    ])
      .then(([sh, cs]) => {
        if (!alive) return;
        setShipments(sh.data?.items ?? []);
        const active = cs.data?.connections.filter((c) => c.status === 'active') ?? [];
        setConnections(active);
        setConnection((cur) => cur || (active[0]?.id ?? ''));
        // Without shipping.read the carriers are hidden, not an error.
        setError(sh.error ? problemMessage(sh.error) : '');
      })
      .catch((err: unknown) => {
        if (alive) setError(problemMessage(err));
      });
    return () => {
      alive = false;
    };
  }, [storeId, orderId, reloads]);

  async function ship() {
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/shipping/shipments', {
      params: { path: { store_id: storeId } },
      body: { connection_id: connection, order_ids: [orderId] },
    });
    setBusy(false);
    const failed = data?.results[0]?.error;
    if (err || failed) setError(err ? problemMessage(err) : (failed?.detail ?? 'تعذّر إنشاء الشحنة'));
    setReloads((n) => n + 1);
    onShipped();
  }

  const canShip = status === 'ready_to_ship' && !shipments.some(live);
  const early = status === 'pending' || status === 'confirmed';
  if (shipments.length === 0 && !canShip && !early) return null;
  return (
    <section className="card">
      <h2>الشحن</h2>
      {error && <p role="alert">{error}</p>}
      {shipments.length === 0 && early && <p className="muted">تُنشأ الشحنة بعد أن يصبح الطلب جاهزًا للشحن.</p>}
      {shipments.length > 0 && (
        <ul>
          {shipments.map((s) => (
            <li key={s.id}>
              <Link to={`/stores/${storeId}/shipments/${s.id}`}>
                {carrierLabel(s.carrier)} <span dir="ltr">{s.tracking_number ?? ''}</span>
              </Link>{' '}
              — <span className={`status sh-${s.status}`}>{shipmentLabel[s.status]}</span>
            </li>
          ))}
        </ul>
      )}
      {canShip &&
        (connections.length === 0 ? (
          <p className="empty">
            <Link to={`/stores/${storeId}/carriers`}>اربط شركة شحن</Link> لإنشاء شحنة.
          </p>
        ) : (
          <div className="actions">
            {connections.length > 1 && (
              <select
                aria-label="شركة الشحن"
                value={connection}
                onChange={(e) => {
                  setConnection(e.target.value);
                }}
              >
                {connections.map((c) => (
                  <option key={c.id} value={c.id}>
                    {carrierLabel(c.carrier)}
                  </option>
                ))}
              </select>
            )}
            <button type="button" disabled={busy || !connection} onClick={() => void ship()}>
              {busy
                ? 'جارٍ الإرسال…'
                : `إنشاء شحنة${connections.length === 1 && connections[0] ? ` مع ${carrierLabel(connections[0].carrier)}` : ''}`}
            </button>
          </div>
        ))}
    </section>
  );
}
