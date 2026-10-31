import { useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api, type components } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { dateTimeAr } from './lib';

type Duplicate = components['schemas']['AdminDuplicatePayment'];

// DuplicatePayments lists payments the gateway took for invoices already
// paid (ADR-0063): the admin refunds each on the gateway, then records
// the refund here. Nothing shows while none waits for a refund.
export function DuplicatePayments() {
  const [items, setItems] = useState<Duplicate[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/billing/duplicates').then(({ data }) => {
      if (live && data) setItems(data.items);
    });
    return () => {
      live = false;
    };
  }, []);

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
    setItems((xs) => xs.map((x) => (x.id === d.id ? { ...x, refunded_at: new Date().toISOString(), refund_reference: reference } : x)));
  }

  const waiting = items.filter((d) => !d.refunded_at);
  if (waiting.length === 0) return null;
  return (
    <section className="table-card" aria-labelledby="h-duplicates">
      <div className="card-head">
        <h2 id="h-duplicates">دفعات زائدة تحتاج استردادًا ({waiting.length})</h2>
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
            {waiting.map((d) => (
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
