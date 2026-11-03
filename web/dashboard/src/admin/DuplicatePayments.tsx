import { useState } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { dateTimeAr, waiting, type Duplicate } from './lib';

// DuplicatePayments lists payments the gateway took for invoices already
// paid (ADR-0063): the admin refunds each on the gateway, then records
// the refund here. Nothing shows while none waits for a refund. The home
// reads the list, so it can count them among what needs attention.
export function DuplicatePayments({ items, onRefunded }: { items: Duplicate[]; onRefunded: (id: string, reference: string) => void }) {
  const [error, setError] = useState('');

  async function refunded(d: Duplicate) {
    const reference = window.prompt('رقم عملية الاسترداد على بوابة الدفع');
    if (!reference) return;
    const reason = window.prompt('السبب');
    if (!reason) return;
    setError('');
    const { error: err } = await api.POST('/admin/v1/billing/duplicates/{payment_id}/refunded', {
      params: { path: { payment_id: d.id } },
      body: { reference, reason },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    onRefunded(d.id, reference);
  }

  const open = waiting(items);
  if (open.length === 0) return null;
  return (
    <section className="table-card dup-card" aria-labelledby="h-duplicates">
      <div className="card-head">
        <h2 id="h-duplicates">دفعات زائدة تحتاج استردادًا ({open.length})</h2>
        <span className="muted small">دفعات لفواتير كانت مدفوعة بالفعل. استردّها من بوابة الدفع ثم سجّلها هنا.</span>
      </div>
      {error && <p role="alert">{error}</p>}
      <div className="scroll">
        <table>
          <thead>
            <tr>
              <th scope="col">المتجر</th>
              <th scope="col">الفاتورة</th>
              <th scope="col">المبلغ</th>
              <th scope="col">عملية البوابة</th>
              <th scope="col">التاريخ</th>
              <th scope="col">
                <span className="visually-hidden">إجراء</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {open.map((d) => (
              <tr key={d.id}>
                <td>
                  <Link to={`/stores/${d.store_id}`}>صفحة المتجر</Link>
                </td>
                <td className="ltr">{d.invoice_number}</td>
                <td>{money(d.amount_minor, d.currency)}</td>
                <td className="ltr">
                  {d.gateway} · {d.reference}
                </td>
                <td className="muted">{dateTimeAr(d.created_at)}</td>
                <td>
                  <button type="button" className="link-button" onClick={() => void refunded(d)}>
                    تم الاسترداد
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
