import type { CSSProperties } from 'react';

import { count, dayLabel, type OrderStats } from '../lib/home';
import { money } from '../lib/orders';

// SalesChart is the home page's last 30 days (the approved mockup): one
// thin bar a day on a shared baseline, today's in green; each bar names
// its day, sales and orders on hover or focus, and a table carries the
// same figures for screen readers.
export function SalesChart({ stats }: { stats: OrderStats }) {
  const days = stats.daily;
  const top = Math.max(1, ...days.map((d) => d.sales_minor));
  const total = days.reduce((n, d) => n + d.sales_minor, 0);
  const first = days[0];
  return (
    <section className="card chart" aria-labelledby="chart-title">
      <div className="chart-head">
        <h2 id="chart-title">المبيعات · آخر 30 يوم</h2>
        <strong className="num">{money(total, stats.currency)}</strong>
      </div>
      <ol className="bars" aria-hidden="true">
        {days.map((d, i) => {
          const label = `${i === days.length - 1 ? 'اليوم' : dayLabel(d.date)}: ${money(d.sales_minor, stats.currency)} · ${d.orders === 0 ? 'لا طلبات' : count(d.orders, 'طلب واحد', 'طلبان', 'طلبات', 'طلبًا')}`;
          return (
            <li
              key={d.date}
              className={[i === days.length - 1 ? 'today' : '', i < 8 ? 'start' : i > days.length - 9 ? 'end' : ''].filter(Boolean).join(' ') || undefined}
              style={{ '--h': `${String(Math.round((d.sales_minor / top) * 100))}%` } as CSSProperties}
            >
              <span className="bar" />
              <span className="tip">{label}</span>
            </li>
          );
        })}
      </ol>
      <div className="chart-foot" aria-hidden="true">
        <span>{first ? dayLabel(first.date) : ''}</span>
        <span>اليوم</span>
      </div>
      <div className="visually-hidden">
        <table>
          <caption>المبيعات يومًا بيوم لآخر 30 يوم</caption>
          <thead>
            <tr>
              <th scope="col">اليوم</th>
              <th scope="col">المبيعات</th>
              <th scope="col">الطلبات</th>
            </tr>
          </thead>
          <tbody>
            {days.map((d) => (
              <tr key={d.date}>
                <td>{dayLabel(d.date)}</td>
                <td>{money(d.sales_minor, stats.currency)}</td>
                <td>{d.orders}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
