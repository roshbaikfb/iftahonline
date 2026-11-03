import type { components } from '../api/client';
import { money } from '../lib/orders';
import { count } from './lib';

type Metrics = components['schemas']['AdminMetrics'];

const dayLabel = (iso: string) => new Date(`${iso}T00:00:00Z`).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', timeZone: 'UTC' });

// Growth fills the home's revenue slot (ADR-0075): the monthly recurring
// revenue per currency, and the new stores of the last 7 and 30 days
// with a bar per day. A section the server could not read says so.
export function Growth({ metrics, failed }: { metrics: Metrics | null; failed: boolean }) {
  const unread = (section: 'revenue' | 'new_stores') => failed || (metrics?.errors.includes(section) ?? false);
  const revenue = metrics?.revenue;
  const fresh = metrics?.new_stores;
  const peak = Math.max(1, ...(fresh?.daily.map((d) => d.count) ?? []));

  return (
    <section className="card growth" aria-label="الإيراد والنمو">
      <div>
        <span className="muted">الإيراد الشهري المتكرر</span>
        {unread('revenue') ? (
          <strong className="unread">تعذّرت القراءة</strong>
        ) : !revenue ? (
          <strong className="unread">…</strong>
        ) : revenue.length === 0 ? (
          <strong>لا اشتراكات مدفوعة بعد</strong>
        ) : (
          <ul className="mrr">
            {revenue.map((r) => (
              <li key={r.currency}>
                <strong>{money(r.mrr_minor, r.currency)}</strong>
                <small>من {count(r.accounts)} حساب</small>
              </li>
            ))}
          </ul>
        )}
        <small className="kpi-note">الاشتراكات النشطة دون الضريبة، والسنوي مقسومًا على 12. لا تدخل التجربة ولا المتأخرة عن الدفع.</small>
      </div>
      <div>
        <span className="muted">متاجر جديدة</span>
        {unread('new_stores') ? (
          <strong className="unread">تعذّرت القراءة</strong>
        ) : !fresh ? (
          <strong className="unread">…</strong>
        ) : (
          <strong>
            {count(fresh.last_7d)} <small>آخر 7 أيام</small> · {count(fresh.last_30d)} <small>آخر 30 يومًا</small>
          </strong>
        )}
      </div>
      {fresh && !unread('new_stores') && (
        <figure className="spark">
          <svg
            viewBox="0 0 120 32"
            preserveAspectRatio="none"
            role="img"
            aria-label={`المتاجر الجديدة يومًا بيوم من ${dayLabel(fresh.daily[0]?.day ?? '')} إلى اليوم: ${fresh.daily.map((d) => d.count).join('، ')}`}
          >
            {fresh.daily.map((d, i) => {
              const h = d.count === 0 ? 1 : Math.max(3, (d.count / peak) * 32);
              return <rect key={d.day} x={i * 4 + 0.5} y={32 - h} width="3" height={h} rx="0.8" className={d.count === 0 ? 'none' : ''} />;
            })}
          </svg>
          <figcaption className="muted small">آخر 30 يومًا، يومًا بيوم</figcaption>
        </figure>
      )}
    </section>
  );
}
