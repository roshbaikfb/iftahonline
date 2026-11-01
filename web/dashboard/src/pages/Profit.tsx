import { useEffect, useState, type CSSProperties } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { count, dayLabel } from '../lib/home';
import { problemMessage } from '../lib/problem';
import { byLabel, flow, income, lastDays, margin, returnRate, signed, type ProfitBy, type ProfitReport } from '../lib/profit';

const ranges = [
  { id: '7', label: '7 أيام', days: 7 },
  { id: '30', label: '30 يومًا', days: 30 },
  { id: '90', label: '90 يومًا', days: 90 },
  { id: 'custom', label: 'مخصّص', days: 0 },
] as const;
type RangeID = (typeof ranges)[number]['id'];

type Loaded = { report: ProfitReport } | { locked: true } | { error: string };

async function fetchProfit(storeId: string, from: string, to: string, by: ProfitBy): Promise<Loaded> {
  try {
    const { data, error, response } = await api.GET('/v1/stores/{store_id}/analytics/profit', {
      params: { path: { store_id: storeId }, query: { from, to, by } },
    });
    if (data) return { report: data };
    if (response.status === 403 && (error as { code?: string } | undefined)?.code === 'plan_limit') return { locked: true };
    return { error: problemMessage(error) };
  } catch (err) {
    return { error: problemMessage(err) };
  }
}

// Profit is the net profit dashboard (P8 8f, the approved mockup): the
// totals, where the profit came from, each day's profit, and a breakdown
// by product, campaign or governorate. A plan without the dashboard
// shows it locked, with the way to the plans.
export function Profit() {
  const { storeId = '' } = useParams();
  const [range, setRange] = useState<RangeID>('30');
  const [custom, setCustom] = useState(lastDays(30));
  const [by, setBy] = useState<ProfitBy>('product');
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [places, setPlaces] = useState<Map<string, string>>(new Map());
  const days = ranges.find((r) => r.id === range)?.days ?? 0;
  const { from, to } = days > 0 ? lastDays(days) : custom;

  useEffect(() => {
    let live = true;
    void fetchProfit(storeId, from, to, by).then((r) => {
      if (live) setLoaded(r);
    });
    return () => {
      live = false;
    };
  }, [storeId, from, to, by]);

  // Governorates by name, from the store's shipping areas.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/shipping', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setPlaces(new Map(data.areas.map((a) => [a.code, a.name_ar])));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  return (
    <Layout storeId={storeId}>
      <div className="profit">
        <nav className="crumbs" aria-label="مسار الصفحة">
          التقارير / <span>الربح الصافي</span>
        </nav>
        <div className="page-head">
          <div>
            <h1>الربح الصافي</h1>
            <p className="muted">ما بقي لك من الطلبات المسلّمة بعد تكلفة المنتجات والشحن والمرتجعات والإعلانات · يُحدَّث كل 10 دقائق</p>
          </div>
          <div className="actions">
            <div className="segmented" role="group" aria-label="الفترة">
              {ranges.map((r) => (
                <button
                  key={r.id}
                  type="button"
                  aria-pressed={range === r.id}
                  onClick={() => {
                    setRange(r.id);
                  }}
                >
                  {r.label}
                </button>
              ))}
            </div>
            <Link className="button" to={`/stores/${storeId}/profit/settings`}>
              التقديرات والإنفاق الإعلاني
            </Link>
          </div>
        </div>
        {range === 'custom' && (
          <div className="profit-dates" role="group" aria-label="الفترة المخصّصة">
            <label>
              من
              <input
                type="date"
                value={custom.from}
                max={custom.to}
                onChange={(e) => {
                  if (e.target.value) setCustom({ ...custom, from: e.target.value });
                }}
              />
            </label>
            <label>
              إلى
              <input
                type="date"
                value={custom.to}
                min={custom.from}
                onChange={(e) => {
                  if (e.target.value) setCustom({ ...custom, to: e.target.value });
                }}
              />
            </label>
          </div>
        )}
        {loaded && 'error' in loaded && <p role="alert">{loaded.error}</p>}
        {loaded && 'locked' in loaded && <Locked storeId={storeId} />}
        {loaded && 'report' in loaded && <Report storeId={storeId} report={loaded.report} by={by} onBy={setBy} places={places} />}
      </div>
    </Layout>
  );
}

function Locked({ storeId }: { storeId: string }) {
  return (
    <section className="card profit-locked" aria-labelledby="h-lock">
      <h2 id="h-lock">اعرف كم ربحت فعلًا</h2>
      <p className="muted">
        لوحة الربح الصافي تطرح من مبيعاتك تكلفة المنتجات والشحن والمرتجعات والإعلانات، لكل منتج وحملة ومحافظة. متاحة في الباقات المدفوعة.
      </p>
      <Link className="button primary" to={`/stores/${storeId}/billing/plans`}>
        اعرض الباقات
      </Link>
      <small className="muted">التقديرات والإنفاق الإعلاني متاحة لك الآن، وتُحسب منذ أول يوم.</small>
    </section>
  );
}

function Report({
  storeId,
  report: r,
  by,
  onBy,
  places,
}: {
  storeId: string;
  report: ProfitReport;
  by: ProfitBy;
  onBy: (b: ProfitBy) => void;
  places: Map<string, string>;
}) {
  const t = r.totals;
  const cur = r.currency;
  const top = Math.max(1, income(t));
  const best = r.days.reduce<(typeof r.days)[number] | null>((b, d) => (!b || d.profit_minor > b.profit_minor ? d : b), null);
  const peak = Math.max(1, ...r.days.map((d) => Math.abs(d.profit_minor)));
  const name = (key: string, label: string) => {
    if (by === 'product') return label || key;
    if (by === 'campaign') return key || 'بدون حملة';
    return places.get(key) ?? key;
  };
  return (
    <>
      {t.cost_missing > 0 && (
        <div className="notice warn" role="note">
          <span>
            <strong>{count(t.cost_missing, 'طلب واحد مسلّم', 'طلبان مسلّمان', 'طلبات مسلّمة', 'طلبًا مسلّمًا')} فيها منتجات بلا تكلفة مسجّلة.</strong> ربحها
            المعروض أعلى من الحقيقي.
          </span>
          <Link to={`/stores/${storeId}/products`}>أكمل تكاليف المنتجات</Link>
        </div>
      )}

      <section className="profit-kpis" aria-label="الملخص">
        <div className="kpi main">
          <span>صافي الربح</span>
          <strong className="num">{signed(t.profit_minor, cur)}</strong>
          <small>بعد كل التكاليف والإعلانات</small>
        </div>
        <div className="kpi">
          <span>هامش الربح</span>
          <strong className="num">{margin(t)}</strong>
          <small>من {signed(income(t), cur)} دخلت</small>
        </div>
        <div className="kpi">
          <span>طلبات مسلّمة</span>
          <strong className="num">{t.delivered}</strong>
          <small>
            من {t.orders} · {t.open} في الطريق
          </small>
        </div>
        <div className="kpi">
          <span>المرتجعات</span>
          <strong className="num loss">{t.returned}</strong>
          <small>كلّفتك {signed(t.return_cost_minor, cur)} شحنًا</small>
        </div>
      </section>

      <div className="profit-row">
        <section className="card profit-flow" aria-labelledby="h-where">
          <h2 id="h-where">من أين جاء الربح</h2>
          <dl>
            {flow(t).map((f) => (
              <div key={f.label} className={f.kind}>
                <dt>{f.label}</dt>
                <dd className="track" aria-hidden="true">
                  <span style={{ width: `${String(Math.min(100, Math.round((Math.abs(f.minor) / top) * 100)))}%` }} />
                </dd>
                <dd className="num">{signed(f.minor, cur)}</dd>
              </div>
            ))}
          </dl>
          <small className="muted">الشحن ورسوم الدفع عند الاستلام تقديرية حتى تصل تسوية شركة الشحن، ثم تُستبدل بالفعلي.</small>
        </section>

        <section className="card profit-days" aria-labelledby="h-days">
          <div className="chart-head">
            <h2 id="h-days">الربح يومًا بيوم</h2>
            {best && (
              <span className="muted">
                أعلى يوم: {dayLabel(best.day)} · {signed(best.profit_minor, cur)}
              </span>
            )}
          </div>
          {r.days.length === 0 ? (
            <p className="empty">لا طلبات في هذه الفترة بعد.</p>
          ) : (
            <>
              <ol className="pbars" aria-hidden="true">
                {r.days.map((d) => (
                  <li
                    key={d.day}
                    className={d.profit_minor < 0 ? 'loss' : undefined}
                    style={{ '--h': `${String(Math.max(2, Math.round((Math.abs(d.profit_minor) / peak) * 100)))}%` } as CSSProperties}
                    title={`${dayLabel(d.day)}: ${signed(d.profit_minor, cur)}`}
                  />
                ))}
              </ol>
              <table className="visually-hidden">
                <caption>ربح كل يوم</caption>
                <tbody>
                  {r.days.map((d) => (
                    <tr key={d.day}>
                      <th scope="row">{dayLabel(d.day)}</th>
                      <td>{signed(d.profit_minor, cur)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </section>
      </div>

      <section className="card profit-by" aria-labelledby="h-by">
        <div className="chart-head">
          <h2 id="h-by">الربح حسب</h2>
          <div className="tabs" role="tablist" aria-label="التقسيم">
            {(Object.keys(byLabel) as ProfitBy[]).map((b) => (
              <button
                key={b}
                type="button"
                role="tab"
                aria-selected={by === b}
                onClick={() => {
                  onBy(b);
                }}
              >
                {byLabel[b].tab}
              </button>
            ))}
          </div>
        </div>
        {r.rows.length === 0 ? (
          <p className="empty">لا شيء في هذه الفترة.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th scope="col">{byLabel[by].head}</th>
                  <th scope="col">مسلّمة</th>
                  <th scope="col">مرتجعة</th>
                  <th scope="col">الإيراد</th>
                  <th scope="col">التكلفة</th>
                  <th scope="col">شحن ومرتجعات</th>
                  <th scope="col">إعلانات</th>
                  <th scope="col">صافي الربح</th>
                  <th scope="col">الهامش</th>
                </tr>
              </thead>
              <tbody>
                {r.rows.map((row) => {
                  const f = row.figures;
                  const rate = returnRate(f);
                  return (
                    <tr key={row.key}>
                      <th scope="row">
                        {name(row.key, row.label)}
                        {f.cost_missing > 0 && by === 'product' && <small className="warn-text">التكلفة غير مسجّلة</small>}
                        {by === 'governorate' && rate > 0.2 && <small className="warn-text">نسبة المرتجعات {Math.round(rate * 100)}%</small>}
                      </th>
                      <td className="num">{f.delivered}</td>
                      <td className={rate > 0.2 ? 'num loss strong' : 'num'}>{f.returned}</td>
                      <td className="num">{signed(f.revenue_minor, cur)}</td>
                      <td className="num">{f.cost_missing > 0 && f.product_cost_minor === 0 ? '—' : signed(-f.product_cost_minor, cur)}</td>
                      <td className="num">{signed(-(f.shipping_cost_minor + f.cod_fees_minor + f.return_cost_minor), cur)}</td>
                      <td className="num">{f.ad_spend_minor > 0 ? signed(-f.ad_spend_minor, cur) : '—'}</td>
                      <td className={f.profit_minor < 0 ? 'num loss strong' : 'num gain strong'}>{signed(f.profit_minor, cur)}</td>
                      <td className="num">{margin(f)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        <small className="muted">{byLabel[by].foot}</small>
      </section>
    </>
  );
}
