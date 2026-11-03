import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { ConsoleLayout } from './ConsoleLayout';
import { DuplicatePayments } from './DuplicatePayments';
import { Growth } from './Growth';
import { attention, count, dateTimeAr, fetchStores, greeting, healthGroups, waiting, type AdminHealth, type Duplicate } from './lib';
import { useAdmin } from './session';
import { StoreCell, StoreStatusPill } from './StoresPage';

type Summary = components['schemas']['AdminStoreSummary'];
type Founding = components['schemas']['AdminFounding'];
type Metrics = components['schemas']['AdminMetrics'];

// refreshEvery is how often the home reads the health check again while
// the tab is visible.
export const refreshEvery = 60_000;

// fetchMetrics reads the revenue and new stores; null when it fails.
async function fetchMetrics(): Promise<Metrics | null> {
  try {
    const { data } = await api.GET('/admin/v1/metrics');
    return data ?? null;
  } catch {
    return null;
  }
}

const time = (iso: string) => new Date(iso).toLocaleTimeString('ar-EG-u-nu-latn', { hour: 'numeric', minute: '2-digit' });
const today = () => new Date().toLocaleDateString('ar-EG-u-nu-latn', { weekday: 'long', day: 'numeric', month: 'long' });

// Overview is the console's home (the approved mockup): what needs the
// owner's attention first, then the platform in four numbers, the
// systems in one line, payments to refund, and the newest stores.
export function Overview() {
  const { state } = useAdmin();
  const name = state.kind === 'signed-in' ? state.admin.name : '';
  const [health, setHealth] = useState<AdminHealth | null>(null);
  const [duplicates, setDuplicates] = useState<Duplicate[]>([]);
  const [founding, setFounding] = useState<Founding | null>(null);
  const [stores, setStores] = useState<Summary[]>([]);
  const [details, setDetails] = useState(false);
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [metricsFailed, setMetricsFailed] = useState(false);
  const [error, setError] = useState('');

  const loadHealth = useCallback(async () => {
    const { data, error: err } = await api.GET('/admin/v1/health');
    if (data) {
      setHealth(data);
      setError('');
    }
    if (err) setError(problemMessage(err));
  }, []);

  // The revenue and new stores are read on opening and on «تحديث الآن»,
  // not every minute: they change slowly.
  const showMetrics = useCallback((m: Metrics | null) => {
    if (m) setMetrics(m);
    setMetricsFailed(!m);
  }, []);

  const refresh = async () => {
    const [, m] = await Promise.all([loadHealth(), fetchMetrics()]);
    showMetrics(m);
  };

  useEffect(() => {
    let live = true;
    void Promise.all([
      api.GET('/admin/v1/health'),
      api.GET('/admin/v1/billing/duplicates'),
      api.GET('/admin/v1/founding'),
      fetchStores({ query: '', status: '' }, '', 5),
      fetchMetrics(),
    ]).then(([h, d, f, st, m]) => {
      if (!live) return;
      showMetrics(m);
      if (h.data) setHealth(h.data);
      if (h.error) setError(problemMessage(h.error));
      if (d.data) setDuplicates(d.data.items);
      if (f.data) setFounding(f.data);
      if (st.data) setStores(st.data.items);
    });
    return () => {
      live = false;
    };
  }, [showMetrics]);

  // The health check is read again every minute while the tab is seen.
  useEffect(() => {
    const id = window.setInterval(() => {
      if (!document.hidden) void loadHealth();
    }, refreshEvery);
    return () => {
      window.clearInterval(id);
    };
  }, [loadHealth]);

  const refunded = useCallback((id: string, reference: string) => {
    setDuplicates((xs) => xs.map((x) => (x.id === id ? { ...x, refunded_at: new Date().toISOString(), refund_reference: reference } : x)));
  }, []);

  const alerts = health ? attention(health, waiting(duplicates).length) : [];
  const s = health?.stores;
  const b = health?.billing;
  const j = health?.jobs;
  // Paying accounts, from the revenue: a store on the free plan is an
  // active subscription too, so the health's count is not the answer.
  const paying = metrics?.revenue?.reduce((n, r) => n + r.accounts, 0);
  const seats = founding?.program.seats ?? 0;
  const taken = founding?.members.length ?? 0;
  const jobs = !j
    ? { tone: 'down', text: 'تعذّرت القراءة' }
    : j.discarded_24h > 0
      ? { tone: 'down', text: `${count(j.discarded_24h)} فشلت` }
      : j.retryable > 0 || j.oldest_available_seconds > 300
        ? { tone: 'warn', text: `${count(j.available)} تنتظر · ${count(j.retryable)} تُعاد` }
        : { tone: 'ok', text: `${count(j.available)} تنتظر` };

  return (
    <ConsoleLayout>
      <div className="page-head home-head">
        <div>
          <h1>
            {greeting(new Date())}
            {name && `، ${name}`}
          </h1>
          <p className="muted">
            {today()}
            {health && ` · آخر فحص ${time(health.checked_at)}`} · يتحدّث كل دقيقة
          </p>
        </div>
        <button type="button" className="button secondary" onClick={() => void refresh()}>
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M20 12a8 8 0 1 1-2.3-5.6M20 4v5h-5" />
          </svg>
          تحديث الآن
        </button>
      </div>
      {error && <p role="alert">{error}</p>}

      {health && (
        <section className="needs" aria-labelledby="h-attention">
          <h2 id="h-attention">
            يحتاج انتباهك{' '}
            {alerts.length > 0 && (
              <span className="needs-count" aria-label={`${String(alerts.length)} أمور`}>
                {alerts.length}
              </span>
            )}
          </h2>
          {alerts.length === 0 ? (
            <p className="all-good" role="status">
              كل شيء يعمل. لا شيء ينتظرك الآن.
            </p>
          ) : (
            <ul className="needs-list">
              {alerts.map((a) => (
                <li key={a.key} className={`needs-card ${a.tone}`}>
                  <strong>
                    <span className="dot" aria-hidden="true" />
                    {a.title}
                  </strong>
                  <p>{a.body}</p>
                  {a.link && <a href={a.link.href}>{a.link.label}</a>}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      <section className="kpis" aria-label="أرقام سريعة">
        <div className="card kpi">
          <span className="muted">المتاجر النشطة</span>
          <strong>
            {s ? count(s.active) : '—'} {s && <small>من {count(s.total)}</small>}
          </strong>
          {s && <span className="kpi-note">{s.suspended > 0 ? `${count(s.suspended)} موقوف من المنصة` : `${count(s.closed)} مغلق`}</span>}
        </div>
        <div className="card kpi">
          <span className="muted">في الفترة التجريبية</span>
          <strong>{b ? count(b.trialing) : '—'}</strong>
          <span className="kpi-note">اشتراكات لم تُدفع بعد</span>
        </div>
        <div className="card kpi">
          <span className="muted">اشتراكات مدفوعة</span>
          <strong>{paying === undefined ? '—' : count(paying)}</strong>
          {b && <span className="kpi-note">{count(b.past_due)} متأخرة الدفع</span>}
        </div>
        <div className="card kpi">
          <span className="muted">مقاعد التجار المؤسسين</span>
          <strong>
            {founding ? count(taken) : '—'} {founding && <small>من {count(seats)}</small>}
          </strong>
          {founding &&
            (founding.program.enabled ? (
              <span className="seat-bar" role="img" aria-label={`${String(taken)} من ${String(seats)} مقعد`}>
                <span style={{ inlineSize: `${String(seats ? Math.min(100, (taken / seats) * 100) : 0)}%` }} />
              </span>
            ) : (
              <span className="kpi-note">البرنامج متوقف</span>
            ))}
        </div>
      </section>

      <Growth metrics={metrics} failed={metricsFailed} />

      {health && (
        <section className="card systems" aria-labelledby="h-systems">
          <div className="systems-line">
            <h2 id="h-systems">الأنظمة</h2>
            <span className="system">
              <span className={health.database ? 'dot ok' : 'dot down'} aria-hidden="true" />
              قاعدة البيانات <b className={health.database ? 'ok' : 'down'}>{health.database ? 'تعمل' : 'لا ترد'}</b>
            </span>
            <span className="system">
              <span className={health.redis ? 'dot ok' : 'dot down'} aria-hidden="true" />
              الذاكرة المؤقتة (Redis) <b className={health.redis ? 'ok' : 'down'}>{health.redis ? 'تعمل' : 'لا ترد'}</b>
            </span>
            <span className="system">
              <span className={`dot ${jobs.tone}`} aria-hidden="true" />
              طابور المهام <b className={jobs.tone}>{jobs.text}</b>
            </span>
            <button
              type="button"
              className="link-button"
              aria-expanded={details}
              aria-controls="all-numbers"
              onClick={() => {
                setDetails(!details);
              }}
            >
              {details ? 'إخفاء الأرقام' : 'كل الأرقام'}
            </button>
          </div>
          {details && (
            <div className="health-groups" id="all-numbers">
              {healthGroups(health).map((g) => (
                <section key={g.title} aria-label={g.title}>
                  <h3>{g.title}</h3>
                  <dl className="facts">
                    {g.items.map((it) => (
                      <div key={it.label} className="fact">
                        <dt>{it.label}</dt>
                        <dd className={it.warn ? 'warn' : ''}>{it.value}</dd>
                      </div>
                    ))}
                  </dl>
                </section>
              ))}
              <p className="muted small">آخر فحص: {dateTimeAr(health.checked_at)}</p>
            </div>
          )}
        </section>
      )}

      <DuplicatePayments items={duplicates} onRefunded={refunded} />

      <section className="table-card latest-stores" aria-labelledby="h-latest">
        <div className="card-head">
          <h2 id="h-latest">أحدث المتاجر</h2>
          <Link to="/stores">كل المتاجر ←</Link>
        </div>
        {stores.length === 0 ? (
          <p className="empty flat">لا توجد متاجر بعد.</p>
        ) : (
          <ul>
            {stores.map((st) => (
              <li key={st.store.id}>
                <StoreCell summary={st} />
                <StoreStatusPill status={st.store.status} />
                <span className="muted small">{dateTimeAr(st.store.created_at)}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </ConsoleLayout>
  );
}
