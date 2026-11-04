import { useEffect, useState, type ReactNode } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { money } from '../lib/orders';
import { ConsoleLayout } from './ConsoleLayout';
import {
  change,
  churnStarts,
  collectedIn,
  conversionRows,
  currencyNames,
  dayName,
  line,
  monthName,
  mrrIn,
  plansIn,
  shownCurrencies,
  type Currency,
  type GrowthData,
  type Section,
} from './growth';
import { count } from './lib';

// short is a bar's top, thousands as «41.2k».
const short = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n));

// Bars are one value a month, the month under way lighter; the numbers
// sit in a table under «عرض الأرقام».
function Bars({
  months,
  values,
  label,
  show,
  top,
  tone,
}: {
  months: string[];
  values: number[];
  label: string;
  show: (v: number) => string;
  top: (v: number) => string;
  tone: 'navy' | 'blue';
}) {
  const peak = Math.max(1, ...values);
  return (
    <>
      <div className={`growth-bars ${tone}`} role="img" aria-label={label}>
        {values.map((v, i) => (
          <span key={months[i]} className={i === values.length - 1 ? 'growth-bar now' : 'growth-bar'}>
            <small>{v > 0 ? top(v) : ''}</small>
            <i style={{ blockSize: `${String(Math.max(2, Math.round((v / peak) * 100)))}%` }} />
          </span>
        ))}
      </div>
      <div className="bar-months" aria-hidden="true">
        {months.map((m) => (
          <span key={m}>{monthName(m)}</span>
        ))}
      </div>
      <details className="figures">
        <summary>عرض الأرقام</summary>
        <table>
          <tbody>
            {months.map((m, i) => (
              <tr key={m}>
                <th scope="row">{monthName(m, true)}</th>
                <td>{show(values[i] ?? 0)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </>
  );
}

function Card({ id, title, note, unread, children }: { id: string; title: string; note?: string | undefined; unread: boolean; children: ReactNode }) {
  return (
    <section className="card chart" aria-labelledby={id}>
      <div className="chart-head">
        <h2 id={id}>{title}</h2>
        {note && <span className="muted small">{note}</span>}
      </div>
      {unread ? <p className="unread">تعذّرت القراءة. حدّث الصفحة بعد قليل.</p> : children}
    </section>
  );
}

// GrowthPage is the console's growth page (ADR-0081, the approved
// mockup): the last 12 Cairo months in one currency at a time.
export function GrowthPage() {
  const [g, setG] = useState<GrowthData | null>(null);
  const [error, setError] = useState('');
  const [currency, setCurrency] = useState<Currency>('EGP');

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/growth').then(({ data, error: err }) => {
      if (!live) return;
      if (data) {
        setG(data);
        setCurrency(shownCurrencies(data)[0] ?? 'EGP');
      }
      if (err) setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, []);

  const unread = (s: Section) => g?.errors.includes(s) ?? false;
  const head = (
    <div className="page-head">
      <div>
        <h1>النمو</h1>
        <p className="muted">آخر 12 شهرًا بتوقيت القاهرة. المبالغ قبل الضريبة، وكل عملة وحدها.</p>
      </div>
      {g && shownCurrencies(g).length > 1 && (
        <div className="segmented-tabs" role="group" aria-label="العملة">
          {shownCurrencies(g).map((c) => (
            <button
              key={c}
              type="button"
              aria-pressed={c === currency}
              onClick={() => {
                setCurrency(c);
              }}
            >
              {currencyNames[c]}
            </button>
          ))}
        </div>
      )}
    </div>
  );
  if (!g) {
    return (
      <ConsoleLayout>
        {head}
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </ConsoleLayout>
    );
  }

  const months = g.months;
  const collected = collectedIn(g, currency);
  const mrr = mrrIn(g, currency);
  const stores = g.stores ?? [];
  const conversion = conversionRows(g);
  const reference = conversion.at(-3);
  const plans = plansIn(g, currency);
  const starts = churnStarts(g);
  const sum = (m: number) => money(m, currency);
  const now = stores.at(-1);
  const before = stores.at(-2);

  return (
    <ConsoleLayout>
      {head}
      <section className="kpis" aria-label="أرقام هذا الشهر">
        <div className="card kpi">
          <span className="muted">الإيراد الشهري المتكرر</span>
          <strong>{unread('mrr') || mrr.length === 0 ? '—' : sum(mrr.at(-1)?.minor ?? 0)}</strong>
          {mrr[0] && change(mrr) && <span className="kpi-note">{`${change(mrr) ?? ''} منذ ${dayName(mrr[0].day)}`}</span>}
        </div>
        <div className="card kpi">
          <span className="muted">محصّل هذا الشهر</span>
          <strong>{unread('collected') ? '—' : sum(collected.at(-1) ?? 0)}</strong>
          {!unread('collected') && months.length > 1 && (
            <span className="kpi-note">{`${monthName(months.at(-2) ?? '')} كله: ${sum(collected.at(-2) ?? 0)}`}</span>
          )}
        </div>
        <div className="card kpi">
          <span className="muted">متاجر جديدة هذا الشهر</span>
          <strong>{now ? count(now.created) : '—'}</strong>
          {before && <span className="kpi-note">{`${monthName(before.month)} كله: ${count(before.created)}`}</span>}
        </div>
        <div className="card kpi">
          <span className="muted">{reference ? `تحويل متاجر ${monthName(reference.month)}` : 'التحويل'}</span>
          <strong>{reference ? `${String(reference.pct)}%` : '—'}</strong>
          {reference && <span className="kpi-note">{`${count(reference.converted)} من ${count(reference.created)} دفعوا`}</span>}
        </div>
      </section>

      <div className="charts">
        <Card id="h-collected" title="الإيراد المحصّل شهريًا" note="من الفواتير المدفوعة" unread={unread('collected')}>
          <Bars
            months={months}
            values={collected}
            tone="navy"
            show={sum}
            top={(v) => short(Math.round(v / 100))}
            label={`الإيراد المحصّل (${currencyNames[currency]}) من ${monthName(months[0] ?? '', true)} إلى ${monthName(months.at(-1) ?? '', true)}`}
          />
        </Card>

        <Card
          id="h-mrr"
          title="الإيراد الشهري المتكرر"
          note={mrr[0] ? `يُسجَّل يوميًا منذ ${dayName(mrr[0].day)}` : undefined}
          unread={unread('mrr')}
        >
          {mrr.length === 0 ? (
            <p className="empty-chart">لم يُسجَّل بعد. يبدأ خلال ساعة من تشغيل العامل (worker)، ثم يُسجَّل كل يوم.</p>
          ) : (
            <>
              <svg
                className="mrr-line"
                viewBox="0 0 480 160"
                preserveAspectRatio="none"
                role="img"
                aria-label={`الإيراد الشهري المتكرر يومًا بيوم منذ ${dayName(mrr[0]?.day ?? '')}: من ${sum(mrr[0]?.minor ?? 0)} إلى ${sum(mrr.at(-1)?.minor ?? 0)}`}
              >
                <path d={line(mrr.map((p) => p.minor), 480, 160)} />
              </svg>
              <div className="bar-months ends" aria-hidden="true">
                <span>{dayName(mrr[0]?.day ?? '')}</span>
                <span>اليوم</span>
              </div>
              <p className="muted small">لا يوجد تاريخ قبل بدء التسجيل، فالمنصة لم تكن تحفظه. يكتمل الرسم شهرًا بعد شهر.</p>
            </>
          )}
        </Card>

        <Card id="h-new" title="المتاجر الجديدة شهريًا" note="كل العملات" unread={unread('stores')}>
          <Bars
            months={months}
            values={stores.map((s) => s.created)}
            tone="blue"
            show={(v) => count(v)}
            top={short}
            label={`المتاجر الجديدة من ${monthName(months[0] ?? '', true)} إلى ${monthName(months.at(-1) ?? '', true)}`}
          />
          <p className="muted small">الشهر الحالي حتى اليوم.</p>
        </Card>

        <Card id="h-conv" title="التحويل إلى الدفع" note="من متاجر كل شهر، كم دفع اشتراكًا" unread={unread('stores')}>
          <ul className="conversion">
            {conversion.map((c) => (
              <li key={c.month}>
                <span>{monthName(c.month)}</span>
                <span className="meter" aria-hidden="true">
                  <i style={{ inlineSize: `${String(Math.min(100, c.pct))}%` }} />
                </span>
                <span>{`${count(c.converted)} من ${count(c.created)} (${String(c.pct)}%)`}</span>
              </li>
            ))}
          </ul>
          <p className="muted small">الأشهر الأخيرة لم تكتمل: كثير من متاجرها ما زال في التجربة.</p>
        </Card>

        <Card id="h-churn" title="المشتركون والإلغاء" unread={unread('churn')}>
          {(g.churn ?? []).length === 0 ? (
            <div className="empty-chart">
              <strong>{starts ? `يبدأ في ${monthName(starts, true)}` : 'يبدأ مع أول تسجيل'}</strong>
              <span className="muted">أول شهر كامل بعد بدء التسجيل: كم مشتركًا دافعًا بدأ الشهر، وكم انضم، وكم ألغى أو توقف.</span>
              {g.paying !== undefined && <span className="muted">{`اليوم: ${count(g.paying)} مشترك دافع.`}</span>}
            </div>
          ) : (
            <table className="churn">
              <thead>
                <tr>
                  <th scope="col">الشهر</th>
                  <th scope="col">في بدايته</th>
                  <th scope="col">انضم</th>
                  <th scope="col">ألغى أو توقف</th>
                  <th scope="col">نسبة الإلغاء</th>
                </tr>
              </thead>
              <tbody>
                {(g.churn ?? []).map((c) => (
                  <tr key={c.month}>
                    <th scope="row">{monthName(c.month, true)}</th>
                    <td>{count(c.start)}</td>
                    <td>{count(c.joined)}</td>
                    <td>{count(c.lost)}</td>
                    <td>{`${(c.rate_bps / 100).toFixed(1)}%`}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>

        <Card id="h-mix" title="توزيع الباقات اليوم" note="الحسابات الدافعة وحصتها من الإيراد" unread={unread('plans')}>
          {plans.length === 0 ? (
            <p className="empty-chart">لا اشتراكات مدفوعة بهذه العملة اليوم.</p>
          ) : (
            <ul className="plan-mix">
              {plans.map((p) => (
                <li key={p.plan}>
                  <span className="plan-mix-head">
                    <strong>{p.name || p.plan}</strong>
                    <span>{`${count(p.accounts)} حساب · ${String(p.share)}% من الإيراد`}</span>
                  </span>
                  <span className="meter" aria-hidden="true">
                    <i style={{ inlineSize: `${String(p.share)}%` }} />
                  </span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </ConsoleLayout>
  );
}
