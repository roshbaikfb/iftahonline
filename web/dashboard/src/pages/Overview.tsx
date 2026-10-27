import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { SalesChart } from '../components/SalesChart';
import { SamplesBanner } from '../components/SamplesBanner';
import { ago, change, count, greeting, ranges, type OrderStats, type StatsRange } from '../lib/home';
import { money, paymentPill, statusLabel } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { useSession } from '../session';

type Order = components['schemas']['Order'];
interface LowStock {
  total: number;
  items: { product_title: string; option_values: string[] }[];
}

interface Setup {
  product: boolean;
  carrier: boolean;
  zones: boolean;
  gateway: boolean;
  domain: boolean;
}

interface Held {
  minor: number;
  late: number;
  currency: string;
}

// Overview is the dashboard home (the approved mockup): a greeting and
// the period, what is left to set up, the period's figures, the last 30
// days, what needs the merchant, and the latest orders. Sales leave out
// cancelled and returned orders, which are shown apart (the owner,
// 2026-10-27).
export function Overview() {
  const { storeId = '' } = useParams();
  const { state } = useSession();
  const [range, setRange] = useState<StatsRange>('today');
  const [stats, setStats] = useState<OrderStats | null>(null);
  const [pending, setPending] = useState(0);
  const [held, setHeld] = useState<Held | null>(null);
  const [setup, setSetup] = useState<Setup | null>(null);
  const [failed, setFailed] = useState<{ n: number; why: string } | null>(null);
  const [low, setLow] = useState<LowStock | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [places, setPlaces] = useState<Map<string, string>>(new Map());
  const [error, setError] = useState('');
  const now = new Date();

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/orders/stats', { params: { path: { store_id: storeId }, query: { range } } }).then(({ data, error: err }) => {
      if (!live) return;
      if (data) setStats(data);
      else setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, [storeId, range]);

  // Everything that does not depend on the period, read once, in parallel.
  useEffect(() => {
    let live = true;
    const path = { path: { store_id: storeId } };
    void api.GET('/v1/stores/{store_id}/orders/counts', { params: path }).then(({ data }) => {
      if (live && data) setPending(data.pending);
    });
    const yearAgo = new Date(Date.now() - 365 * 24 * 3600 * 1000).toISOString();
    void api.GET('/v1/stores/{store_id}/shipping/cod', { params: { ...path, query: { from: yearAgo } } }).then(({ data }) => {
      if (!live || !data) return;
      const first = data.carriers[0];
      setHeld({
        minor: data.carriers.reduce((n, c) => n + c.held_minor, 0),
        late: data.carriers.reduce((n, c) => n + c.held_15_plus_minor, 0),
        currency: first?.currency ?? '',
      });
    });
    void Promise.all([
      api.GET('/v1/stores/{store_id}/products', { params: { ...path, query: { limit: 50 } } }),
      api.GET('/v1/stores/{store_id}/shipping/carriers', { params: path }),
      api.GET('/v1/stores/{store_id}/shipping', { params: path }),
      api.GET('/v1/stores/{store_id}/payments/gateways', { params: path }),
      api.GET('/v1/stores/{store_id}/domains', { params: path }),
    ]).then(([products, carriers, shipping, gateways, domains]) => {
      if (!live) return;
      setSetup({
        product: products.data?.items.some((p) => !p.sample) ?? false,
        carrier: carriers.data?.connections.some((c) => c.status === 'active') ?? false,
        zones: (shipping.data?.zones.length ?? 0) > 0,
        gateway: gateways.data?.connections.some((c) => c.status === 'active') ?? false,
        domain: domains.data?.items.some((d) => d.kind === 'custom' && d.status === 'verified') ?? false,
      });
      if (shipping.data) setPlaces(new Map(shipping.data.areas.map((a) => [a.code, a.name_ar])));
    });
    void api.GET('/v1/stores/{store_id}/shipping/shipments', { params: { ...path, query: { status: 'failed', limit: 20 } } }).then(({ data }) => {
      if (live && data) setFailed({ n: data.items.length, why: data.items[0]?.failure ?? '' });
    });
    void api.GET('/v1/stores/{store_id}/storefront/theme', { params: path }).then(async ({ data }) => {
      const threshold = data?.settings.low_stock ?? 5;
      if (threshold === 0) return; // the merchant turned the low-stock message off
      const res = await api.GET('/v1/stores/{store_id}/inventory/low', { params: { ...path, query: { threshold, limit: 3 } } });
      if (live && res.data) setLow(res.data);
    });
    void api.GET('/v1/stores/{store_id}/orders', { params: { ...path, query: { limit: 5 } } }).then(({ data }) => {
      if (live && data) setOrders(data.items);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  const firstName = state.kind === 'signed-in' ? (state.me.user.name.split(/\s+/)[0] ?? '') : '';
  const versus = ranges.find((r) => r.id === range)?.versus ?? '';
  const delta = stats ? change(stats.sales.total_minor, stats.previous.total_minor) : null;
  const steps = setup
    ? [
        { label: 'أضف أول منتج', done: setup.product, soon: true },
        { label: 'اربط شركة شحن', done: setup.carrier, to: 'carriers' },
        { label: 'حدّد مناطق الشحن', done: setup.zones, to: 'shipping' },
        { label: 'اربط بوابة الدفع', done: setup.gateway, to: 'gateways' },
        { label: 'اربط الدومين الخاص', done: setup.domain, soon: true },
      ]
    : [];
  const doneSteps = steps.filter((s) => s.done).length;

  return (
    <Layout storeId={storeId}>
      <div className="page-head home-head">
        <div>
          <h1>
            {greeting(now)}
            {firstName && ` يا ${firstName}`}
          </h1>
          <p className="muted">هذا ملخص متجرك.</p>
        </div>
        <div className="ranges" role="group" aria-label="الفترة">
          {ranges.map((r) => (
            <button
              key={r.id}
              type="button"
              aria-pressed={r.id === range}
              onClick={() => {
                setRange(r.id);
              }}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>
      <SamplesBanner storeId={storeId} />
      {error && <p role="alert">{error}</p>}

      {setup && doneSteps < steps.length && (
        <section className="card setup" aria-labelledby="setup-title">
          <div className="setup-head">
            <div>
              <h2 id="setup-title">أكمل إعداد متجرك</h2>
              <span className="muted">
                {doneSteps} من {steps.length} خطوات{doneSteps >= 3 ? ' · متجرك جاهز تقريبًا' : ''}
              </span>
            </div>
            <progress className="setup-bar" max={steps.length} value={doneSteps} aria-label="تقدّم الإعداد" />
          </div>
          <ol className="steps">
            {steps.map((s) => {
              const body = (
                <>
                  <span className="dot" aria-hidden="true">
                    {s.done && (
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </span>
                  <span className="label">{s.label}</span>
                  {s.done && <span className="visually-hidden">(تم)</span>}
                  {!s.done && s.soon && <span className="soon-tag">قريبًا</span>}
                </>
              );
              return (
                <li key={s.label} className={s.done ? 'done' : undefined}>
                  {!s.done && s.to ? (
                    <Link className="step" to={`/stores/${storeId}/${s.to}`}>
                      {body}
                    </Link>
                  ) : (
                    <span className="step">{body}</span>
                  )}
                </li>
              );
            })}
          </ol>
        </section>
      )}

      <div className="kpis">
        <Link className="kpi" to={`/stores/${storeId}/orders`}>
          <small>مبيعات {ranges.find((r) => r.id === range)?.label}</small>
          <strong>{stats ? money(stats.sales.total_minor, stats.currency) : '—'}</strong>
          {delta && <span className={delta.startsWith('+') ? 'up' : delta.startsWith('−') ? 'down' : undefined}>{`${delta} ${versus}`}</span>}
        </Link>
        <Link className="kpi" to={`/stores/${storeId}/orders`}>
          <small>طلبات {ranges.find((r) => r.id === range)?.label}</small>
          <strong>{stats ? stats.sales.orders : '—'}</strong>
          {stats && stats.sales.orders > 0 && (
            <span>
              {stats.cod_orders} عند الاستلام · {stats.card_orders} بالبطاقة
            </span>
          )}
        </Link>
        <Link className="kpi" to={`/stores/${storeId}/orders?status=pending`}>
          <small>بانتظار التأكيد</small>
          <strong>{pending}</strong>
          {pending > 0 && <span className="warn">أكّدها الآن ←</span>}
        </Link>
        {held && (
          <Link className="kpi" to={`/stores/${storeId}/cod`}>
            <small>لدى شركات الشحن</small>
            <strong>{money(held.minor, held.currency === '' ? (stats?.currency ?? 'EGP') : held.currency)}</strong>
            {held.late > 0 && <span className="down">{money(held.late, held.currency)} منذ أكثر من 15 يومًا</span>}
          </Link>
        )}
      </div>
      {stats && (stats.cancelled.orders > 0 || stats.returned.orders > 0) && (
        <p className="kpi-aside">
          <Link to={`/stores/${storeId}/orders?status=cancelled`}>
            ملغاة: {count(stats.cancelled.orders, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')} · {money(stats.cancelled.total_minor, stats.currency)}
          </Link>
          <Link to={`/stores/${storeId}/orders?status=returned`}>
            مرتجعة: {count(stats.returned.orders, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')} · {money(stats.returned.total_minor, stats.currency)}
          </Link>
          <span className="muted">لا تدخل في المبيعات.</span>
        </p>
      )}

      <div className="home-grid">
        {stats && <SalesChart stats={stats} />}
        <section className="card attention" aria-labelledby="attention-title">
          <h2 id="attention-title">يحتاج انتباهك</h2>
          <ul>
            {pending > 0 && (
              <li className="amber">
                <Link to={`/stores/${storeId}/orders?status=pending`}>
                  <strong>{count(pending, 'طلب ينتظر التأكيد', 'طلبان ينتظران التأكيد', 'طلبات تنتظر التأكيد', 'طلبًا ينتظر التأكيد')}</strong>
                  {stats?.oldest_pending_at && <small>{since(pending, stats.oldest_pending_at, now)}</small>}
                </Link>
              </li>
            )}
            {failed && failed.n > 0 && (
              <li className="red">
                <Link to={`/stores/${storeId}/shipments?status=failed`}>
                  <strong>{count(failed.n, 'شحنة رفضتها شركة الشحن', 'شحنتان رفضتهما شركة الشحن', 'شحنات رفضتها شركة الشحن', 'شحنة رفضتها شركة الشحن')}</strong>
                  {failed.why && <small>{failed.why}</small>}
                </Link>
              </li>
            )}
            {low && low.total > 0 && (
              <li className="blue">
                <div>
                  <strong>{count(low.total, 'منتج قارب على النفاد', 'منتجان قاربا على النفاد', 'منتجات قاربت على النفاد', 'منتجًا قارب على النفاد')}</strong>
                  <small>
                    {[low.items[0]?.product_title, ...(low.items[0]?.option_values ?? [])].filter(Boolean).join(' ')}
                    {low.total > 1 ? ' وغيره' : ''}
                  </small>
                </div>
              </li>
            )}
            {pending === 0 && (!failed || failed.n === 0) && (!low || low.total === 0) && <li className="calm">لا شيء يحتاج انتباهك الآن.</li>}
          </ul>
        </section>
      </div>

      <section className="table-card" aria-labelledby="recent-title">
        <div className="card-head">
          <h2 id="recent-title">آخر الطلبات</h2>
          <Link to={`/stores/${storeId}/orders`}>كل الطلبات</Link>
        </div>
        {orders.length === 0 ? (
          <p className="empty flat">لا توجد طلبات بعد. شارك رابط متجرك ليصلك أول طلب.</p>
        ) : (
          <div className="scroll">
            <table className="orders">
              <thead>
                <tr>
                  <th>الطلب</th>
                  <th>العميل</th>
                  <th>الإجمالي</th>
                  <th>الدفع</th>
                  <th>الحالة</th>
                </tr>
              </thead>
              <tbody>
                {orders.map((o) => {
                  const pay = paymentPill(o);
                  return (
                    <tr key={o.id}>
                      <td>
                        <Link to={`/stores/${storeId}/orders/${o.id}`} dir="ltr" className="code">
                          #{o.code}
                        </Link>
                        <small className="sub">{ago(o.created_at, now)}</small>
                      </td>
                      <td>
                        {o.name}
                        <small className="sub">
                          {o.city}، {places.get(o.governorate) ?? o.governorate}
                        </small>
                      </td>
                      <td className="num">{money(o.total_minor, o.currency)}</td>
                      <td>
                        <span className={`status ${pay.cls}`}>{pay.label}</span>
                      </td>
                      <td>
                        <span className={`status s-${o.status}`}>{statusLabel[o.status]}</span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Layout>
  );
}

// since says when the oldest waiting order came ("أقدمها منذ 3 ساعات");
// a date reads "من 25 أكتوبر".
function since(pending: number, iso: string, now: Date): string {
  const when = ago(iso, now);
  const phrase = /^(منذ|الآن|أمس)/.test(when) ? when : `من ${when}`;
  return `${pending === 1 ? 'وصل' : 'أقدمها'} ${phrase}`;
}
