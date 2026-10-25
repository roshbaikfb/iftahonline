import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { OrderPayments } from '../components/OrderPayments';
import { OrderShipping } from '../components/OrderShipping';
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

// OrderDetail shows one order (the approved mockup): its number, status
// and the moves the status allows; a reminder to call before confirming a
// cash order; the lines and totals and the timeline; and the customer,
// payment and shipping cards. A move sends the version read, so a change
// made by someone else in between is refused instead of overwritten.
export function OrderDetail() {
  const { storeId = '', orderId = '' } = useParams();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [places, setPlaces] = useState<Map<string, string>>(new Map());

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

  // Governorates are named, not coded.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/shipping', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setPlaces(new Map(data.areas.map((a) => [a.code, a.name_ar])));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

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

  function reload() {
    void fetchOrder(storeId, orderId).then((r) => {
      if (r.detail) setDetail(r.detail);
    });
  }

  const o = detail?.order;
  return (
    <Layout storeId={storeId}>
      <nav className="crumbs" aria-label="مسار الصفحة">
        <Link to={`/stores/${storeId}/orders`}>الطلبات</Link> / <span dir="ltr">{o ? `#${o.code}` : ''}</span>
      </nav>
      {error && <p role="alert">{error}</p>}
      {detail && o && (
        <article className="order">
          <div className="page-head">
            <div className="title-row">
              <h1>
                الطلب <span dir="ltr">#{o.code}</span>
              </h1>
              <span className={`status s-${o.status}`}>{statusLabel[o.status]}</span>
              <span className="muted">
                {dateTime(o.created_at)} · {o.source === 'storefront' ? 'من المتجر' : 'طلب أدخله التاجر'}
              </span>
            </div>
            <div className="actions moves">
              {nextSteps[o.status].map((s) => (
                <button
                  key={s}
                  type="button"
                  disabled={busy}
                  className={s === 'cancelled' ? 'danger' : s === 'confirmed' ? 'success' : ''}
                  onClick={() => void move(s)}
                >
                  {s === 'confirmed' ? 'تأكيد الطلب' : stepLabel[s]}
                </button>
              ))}
            </div>
          </div>
          {o.status === 'pending' && o.payment_method === 'cod' && (
            <p className="notice" role="note">
              طلب دفع عند الاستلام: اتصل بالعميل لتأكيده قبل الشحن.
            </p>
          )}
          <div className="od-grid">
            <div className="od-main">
              <section className="card">
                <h2>المنتجات ({detail.lines.reduce((n, l) => n + l.qty, 0)})</h2>
                <ul className="od-lines">
                  {detail.lines.map((l) => (
                    <li key={l.variant_id}>
                      <div className="what">
                        <strong>{l.title}</strong>
                        <small>{[l.variant_title, l.sku && `SKU ${l.sku}`].filter(Boolean).join(' · ')}</small>
                      </div>
                      <span className="muted">
                        {l.qty} × {money(l.unit_price_minor, o.currency)}
                      </span>
                      <strong className="num">{money(l.total_minor, o.currency)}</strong>
                    </li>
                  ))}
                </ul>
                <dl className="totals">
                  <div>
                    <dt>المنتجات</dt>
                    <dd>{money(o.subtotal_minor, o.currency)}</dd>
                  </div>
                  <div>
                    <dt>الشحن إلى {places.get(o.governorate) ?? o.governorate}</dt>
                    <dd>{o.shipping_minor > 0 ? money(o.shipping_minor, o.currency) : 'مجاني'}</dd>
                  </div>
                  {o.cod_fee_minor > 0 && (
                    <div>
                      <dt>رسوم الدفع عند الاستلام</dt>
                      <dd>{money(o.cod_fee_minor, o.currency)}</dd>
                    </div>
                  )}
                  <div className="total">
                    <dt>الإجمالي</dt>
                    <dd>{money(o.total_minor, o.currency)}</dd>
                  </div>
                </dl>
              </section>
              <section className="card">
                <h2>سجل الطلب</h2>
                <ol className="od-timeline">
                  {[...detail.events].reverse().map((e) => (
                    <li key={e.id}>
                      <span className={`dot s-${e.to}`} aria-hidden="true" />
                      <div>
                        <strong>{e.from ? statusLabel[e.to] : o.source === 'storefront' ? 'وصل الطلب من المتجر' : 'أُدخل الطلب'}</strong>
                        <small>
                          <time dateTime={e.at}>{dateTime(e.at)}</time>
                          {byLabel(e, o.source)}
                        </small>
                        {e.note && <q>{e.note}</q>}
                      </div>
                    </li>
                  ))}
                </ol>
              </section>
            </div>
            <div className="od-side">
              <section className="card customer">
                <h2>العميل</h2>
                <strong>{o.name}</strong>
                <span dir="ltr" className="phone">
                  {o.phone}
                </span>
                <div className="contact">
                  <a className="button primary" href={`tel:${o.phone}`}>
                    اتصال
                  </a>
                  <a className="button success" href={`https://wa.me/${o.phone.replace(/^\+/, '')}`} target="_blank" rel="noopener noreferrer">
                    واتساب
                  </a>
                </div>
                <p className="address">
                  {o.address}، {o.city}، {places.get(o.governorate) ?? o.governorate}
                  {o.notes && <small>ملاحظة العميل: {o.notes}</small>}
                </p>
                <Link to={`/stores/${storeId}/orders?phone=${encodeURIComponent(o.phone)}`}>طلبات هذا العميل</Link>
              </section>
              {o.payment_method === 'cod' ? (
                <section className="card">
                  <h2>الدفع</h2>
                  <p className="pay-row">
                    الدفع عند الاستلام
                    <span className={o.payment_status === 'paid' ? 'status pay-paid' : 'status pay-awaiting'}>
                      {o.payment_status === 'paid' ? 'محصَّل' : 'لم يُحصَّل'}
                    </span>
                  </p>
                  {o.payment_status !== 'paid' && !closed.has(o.status) && (
                    <small className="muted">يحصّل المندوب {money(o.total_minor, o.currency)} ويحوّلها لك شركة الشحن.</small>
                  )}
                </section>
              ) : (
                <OrderPayments storeId={storeId} order={o} onChanged={reload} />
              )}
              <OrderShipping storeId={storeId} orderId={orderId} status={o.status} onShipped={reload} />
            </div>
          </div>
        </article>
      )}
    </Layout>
  );
}

// closed are the statuses after which nothing is left to collect.
const closed = new Set<OrderStatus>(['cancelled', 'returned']);

// byLabel says who moved the order: the customer who placed it, or a
// system ("carrier:bosta"). A merchant's own moves carry no label.
function byLabel(e: { from?: OrderStatus; actor?: string }, source: string): string {
  if (!e.from) return source === 'storefront' ? ' · العميل' : '';
  if (!e.actor) return '';
  const [kind, name = ''] = e.actor.split(':');
  if (kind === 'carrier') return ` · شركة الشحن (${name})`;
  if (kind === 'payments') return ' · بوابة الدفع';
  return ` · ${e.actor}`;
}
