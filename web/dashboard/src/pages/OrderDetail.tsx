import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { CustomerRiskCard } from '../components/CustomerRiskCard';
import { OrderConfirmationCard } from '../components/OrderConfirmationCard';
import { OrderPayments } from '../components/OrderPayments';
import { OrderShipping } from '../components/OrderShipping';
import { canReadMessages, channelLabel, type CustomerMessage, type OrderConfirmation } from '../lib/messages';
import { dateTime, money, nextSteps, statusLabel, stepLabel, type OrderStatus } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { reasonLabel, takesReason, type OutcomeReason } from '../lib/risk';
import { useRole } from '../lib/settings';

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

// fetchConfirmation reads the messages sent for an order and, when one
// asked the customer to confirm, the confirmation itself: an order that
// asked for none (a card order, a store that does not confirm, a held
// order) costs no request that would answer 404. A prepaid delivery's
// link to pay is its confirmation's message (ADR-0070).
async function fetchConfirmation(storeId: string, orderId: string): Promise<{ confirmation?: OrderConfirmation; messages: CustomerMessage[] }> {
  const path = { store_id: storeId };
  const m = await api.GET('/v1/stores/{store_id}/messages', { params: { path, query: { order_id: orderId } } });
  const messages = m.data?.items ?? [];
  if (!messages.some((x) => x.kind === 'order_confirmation' || x.kind === 'order_delivery_prepay')) return { messages };
  const c = await api.GET('/v1/stores/{store_id}/messaging/confirmations/{order_id}', { params: { path: { ...path, order_id: orderId } } });
  return c.data ? { confirmation: c.data, messages } : { messages };
}

// OrderDetail shows one order (the approved mockups): its number, status
// and the moves the status allows; the customer's confirmation by
// message, or a reminder to call before confirming a cash order; the
// lines, discount and totals and the timeline; and the customer, the
// campaign it came from, payment and shipping cards. A move sends the version read, so a change
// made by someone else in between is refused instead of overwritten.
export function OrderDetail() {
  const { storeId = '', orderId = '' } = useParams();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  // asking is a move that waits for its reason (ADR-0065).
  const [asking, setAsking] = useState<OrderStatus | null>(null);
  const [places, setPlaces] = useState<Map<string, string>>(new Map());
  const [confirm, setConfirm] = useState<{ confirmation?: OrderConfirmation; messages: CustomerMessage[] }>({ messages: [] });
  const readMessages = canReadMessages(useRole(storeId));
  const status = detail?.order.status;

  // The confirmation follows the order's status (an answer moves both).
  useEffect(() => {
    if (!readMessages || !status) return;
    let live = true;
    void fetchConfirmation(storeId, orderId).then((r) => {
      if (live) setConfirm(r);
    });
    return () => {
      live = false;
    };
  }, [storeId, orderId, readMessages, status]);

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

  async function move(to: OrderStatus, reason?: OutcomeReason) {
    if (!detail) return;
    setAsking(null);
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/orders/{order_id}/transitions', {
      params: { path: { store_id: storeId, order_id: orderId } },
      body: { status: to, note: '', version: detail.order.version, ...(reason ? { reason } : {}) },
    });
    const r = await fetchOrder(storeId, orderId);
    if (r.detail) setDetail(r.detail);
    setError(err ? problemMessage(err) : (r.error ?? ''));
    setBusy(false);
  }

  async function release() {
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/orders/{order_id}/release', {
      params: { path: { store_id: storeId, order_id: orderId } },
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
                  onClick={() => {
                    if (takesReason.has(s)) setAsking(s);
                    else void move(s);
                  }}
                >
                  {s === 'confirmed' ? 'تأكيد الطلب' : stepLabel[s]}
                </button>
              ))}
            </div>
          </div>
          {asking && (
            <div className="card reason-pick" role="group" aria-labelledby="h-reason">
              <p id="h-reason">لماذا {stepLabel[asking]}؟</p>
              {(Object.keys(reasonLabel) as OutcomeReason[]).map((r) => (
                <button key={r} type="button" disabled={busy} onClick={() => void move(asking, r)}>
                  {reasonLabel[r]}
                </button>
              ))}
              <button type="button" disabled={busy} onClick={() => void move(asking)}>
                سبب آخر
              </button>
              <button
                type="button"
                className="link"
                onClick={() => {
                  setAsking(null);
                }}
              >
                تراجع
              </button>
            </div>
          )}
          {o.status === 'pending' && o.risk_action === 'hold' && (
            <div className="notice risk-hold" role="note">
              <p>حجزنا هذا الطلب للمراجعة بحسب قواعد حماية الدفع عند الاستلام: لم نرسل للعميل رسالة تأكيد. راجعه واتصل بالعميل، ثم فك الحجز لتأكيده، أو ألغِه.</p>
              <button type="button" disabled={busy} onClick={() => void release()}>
                فك الحجز
              </button>
            </div>
          )}
          {o.status === 'pending' && o.risk_action === 'verify' && (
            <p className="notice" role="note">
              تحقّق هاتفيًا: قواعدك تطلب الاتصال بهذا العميل قبل التأكيد، حتى لو وافق على رسالة التأكيد.
            </p>
          )}
          {o.status === 'pending' && confirm.confirmation?.status === 'pending' && o.deposit_status !== 'awaiting' && (
            <p className="notice" role="note">
              أرسلنا للعميل رسالة تأكيد على {channelLabel(confirm.messages.find((m) => m.kind === 'order_confirmation')?.channel ?? 'whatsapp')}. يتحوّل الطلب وحده حين يرد، ويمكنك
              تأكيده أو إلغاؤه بنفسك قبل ذلك.
            </p>
          )}
          {o.status === 'pending' && o.payment_method === 'cod' && confirm.confirmation?.status !== 'pending' && (
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
                  {(o.discount_minor ?? 0) > 0 && (
                    <div className="discount">
                      <dt>
                        خصم الكوبون{' '}
                        {o.coupon_code && (
                          <span className="coupon-tag" dir="ltr">
                            {o.coupon_code}
                          </span>
                        )}
                      </dt>
                      <dd>−{money(o.discount_minor ?? 0, o.currency)}</dd>
                    </div>
                  )}
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
              {confirm.confirmation && (
                <OrderConfirmationCard
                  storeId={storeId}
                  confirmation={confirm.confirmation}
                  messages={confirm.messages}
                  prepaid={o.deposit_status === 'awaiting'}
                />
              )}
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
              <CustomerRiskCard storeId={storeId} orderId={orderId} status={o.status} />
              {o.attribution && Object.values(o.attribution).some(Boolean) && (
                <section className="card od-source" aria-labelledby="h-src">
                  <h2 id="h-src">مصدر الطلب</h2>
                  <dl className="src">
                    {sourceFields.map(([k, label]) =>
                      o.attribution?.[k] ? (
                        <div key={k}>
                          <dt>{label}</dt>
                          <dd dir="ltr">{o.attribution[k]}</dd>
                        </div>
                      ) : null,
                    )}
                  </dl>
                  <small className="muted">من رابط الإعلان الذي جاء منه العميل (UTM).</small>
                </section>
              )}
              {o.payment_method === 'cod' && (o.deposit_minor ?? 0) > 0 && <OrderPayments storeId={storeId} order={o} onChanged={reload} />}
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
                    <small className="muted">يحصّل المندوب {money(onDelivery(o), o.currency)} ويحوّلها لك شركة الشحن.</small>
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

// sourceFields are the campaign's parts, as the order card names them.
const sourceFields = [
  ['source', 'المصدر'],
  ['medium', 'الوسيلة'],
  ['campaign', 'الحملة'],
  ['content', 'المحتوى'],
  ['term', 'الكلمة'],
] as const;

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

// onDelivery is what the courier collects: the total, less a prepaid
// delivery once it is paid (ADR-0067).
function onDelivery(o: { total_minor: number; deposit_minor?: number; deposit_status?: string }): number {
  return o.deposit_status === 'paid' ? o.total_minor - (o.deposit_minor ?? 0) : o.total_minor;
}
