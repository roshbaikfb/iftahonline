import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime, money, nextSteps, statusLabel, stepLabel, type OrderStatus } from '../lib/orders';
import { problemMessage } from '../lib/problem';

type Detail = components['schemas']['OrderDetail'];

async function fetchOrder(storeId: string, orderId: string): Promise<{ detail?: Detail; error?: string }> {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/orders/{order_id}', {
      params: { path: { store_id: storeId, order_id: orderId } },
    });
    return error ? { error: problemMessage(error) } : { detail: data };
  } catch (err) {
    return { error: problemMessage(err) };
  }
}

// OrderDetail shows one order: contact, lines, timeline, and the moves
// its status allows. A move sends the version read, so a change made by
// someone else in between is refused instead of overwritten.
export function OrderDetail() {
  const { storeId = '', orderId = '' } = useParams();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void fetchOrder(storeId, orderId).then((r) => {
      if (!live) return;
      if (r.detail) setDetail(r.detail);
      setError(r.error ?? '');
    });
    return () => {
      live = false;
    };
  }, [storeId, orderId]);

  async function move(to: OrderStatus) {
    if (!detail) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/orders/{order_id}/transitions', {
      params: { path: { store_id: storeId, order_id: orderId } },
      body: { status: to, note: '', version: detail.order.version },
    });
    const r = await fetchOrder(storeId, orderId);
    if (r.detail) setDetail(r.detail);
    setError(err ? problemMessage(err) : (r.error ?? ''));
    setBusy(false);
  }

  return (
    <Layout storeId={storeId}>
      <p>
        <Link to={`/stores/${storeId}/orders`}>→ الطلبات</Link>
      </p>
      {error && <p role="alert">{error}</p>}
      {detail && <OrderView detail={detail} busy={busy} onMove={(s) => void move(s)} />}
    </Layout>
  );
}

function OrderView({ detail, busy, onMove }: { detail: Detail; busy: boolean; onMove: (s: OrderStatus) => void }) {
  const o = detail.order;
  return (
    <article className="order">
      <h1>
        الطلب <span dir="ltr">#{o.code}</span> <span className={`status s-${o.status}`}>{statusLabel[o.status]}</span>
      </h1>
      <div className="moves">
        {nextSteps[o.status].map((s) => (
          <button
            key={s}
            type="button"
            disabled={busy}
            className={s === 'cancelled' ? 'danger' : ''}
            onClick={() => {
              onMove(s);
            }}
          >
            {stepLabel[s]}
          </button>
        ))}
      </div>
      <section>
        <h2>العميل</h2>
        <dl>
          <dt>الاسم</dt>
          <dd>{o.name}</dd>
          <dt>الموبايل</dt>
          <dd dir="ltr">
            <a href={`tel:${o.phone}`}>{o.phone}</a>
          </dd>
          <dt>العنوان</dt>
          <dd>
            {o.address}، {o.city} (<span dir="ltr">{o.governorate}</span>)
          </dd>
          {o.notes && (
            <>
              <dt>ملاحظات</dt>
              <dd>{o.notes}</dd>
            </>
          )}
        </dl>
      </section>
      <section>
        <h2>المنتجات</h2>
        <table>
          <tbody>
            {detail.lines.map((l) => (
              <tr key={l.variant_id}>
                <td>
                  {l.title}
                  {l.variant_title && <small> ({l.variant_title})</small>}
                </td>
                <td>× {l.qty}</td>
                <td>{money(l.total_minor, o.currency)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            {o.shipping_minor > 0 && (
              <tr>
                <td colSpan={2}>الشحن</td>
                <td>{money(o.shipping_minor, o.currency)}</td>
              </tr>
            )}
            {o.cod_fee_minor > 0 && (
              <tr>
                <td colSpan={2}>رسوم الدفع عند الاستلام</td>
                <td>{money(o.cod_fee_minor, o.currency)}</td>
              </tr>
            )}
            <tr>
              <th colSpan={2}>الإجمالي</th>
              <th>{money(o.total_minor, o.currency)}</th>
            </tr>
          </tfoot>
        </table>
      </section>
      <section>
        <h2>السجل</h2>
        <ol className="timeline">
          {detail.events.map((e) => (
            <li key={e.id}>
              <time dateTime={e.at}>{dateTime(e.at)}</time> — {statusLabel[e.to]}
              {e.note && <q>{e.note}</q>}
            </li>
          ))}
        </ol>
      </section>
    </article>
  );
}
