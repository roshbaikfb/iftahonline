import { useEffect, useState, type SubmitEvent } from 'react';

import { api, type components } from '../api/client';
import { toMajor, toMinor } from '../lib/amounts';
import { dateTime, money } from '../lib/orders';
import { gatewayLabel, orderPaymentLabel, paymentLabel, refundable, refundLabel } from '../lib/payments';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

type Order = components['schemas']['Order'];
type Detail = components['schemas']['PaymentDetail'];

async function fetchPayments(storeId: string, orderId: string): Promise<{ list: Detail[]; error?: string }> {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/payments', {
      params: { path: { store_id: storeId }, query: { order_id: orderId } },
    });
    if (error) return { list: [], error: problemMessage(error) };
    const details = await Promise.all(
      data.items.map((p) =>
        api.GET('/v1/stores/{store_id}/payments/{payment_id}', { params: { path: { store_id: storeId, payment_id: p.id } } }),
      ),
    );
    return { list: details.flatMap((d) => (d.data ? [d.data] : [])) };
  } catch (err) {
    return { list: [], error: problemMessage(err) };
  }
}

// OrderPayments is a card order's payments with their refunds, and the
// refund form (ADR-0029): partial or full, asked from the gateway in the
// background, never more than is left.
export function OrderPayments({ storeId, order, onChanged }: { storeId: string; order: Order; onChanged: () => void }) {
  const [list, setList] = useState<Detail[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void fetchPayments(storeId, order.id).then((r) => {
      if (!live) return;
      setList(r.list);
      setError(r.error ?? '');
    });
    return () => {
      live = false;
    };
  }, [storeId, order.id, reloads]);

  async function refund(e: SubmitEvent<HTMLFormElement>, paymentId: string, left: number) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const amount = toMinor(field(form, 'amount'));
    if (amount === undefined || amount < 1 || amount > left) {
      setError(`المبلغ: من 0.01 إلى ${money(left, order.currency)}`);
      return;
    }
    if (!globalThis.confirm(`استرداد ${money(amount, order.currency)} إلى بطاقة العميل؟`)) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/payments/{payment_id}/refunds', {
      params: { path: { store_id: storeId, payment_id: paymentId } },
      body: { amount_minor: amount, reason: field(form, 'reason').trim() },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setNotice('طُلب الاسترداد؛ ستؤكده البوابة خلال لحظات.');
    setReloads((n) => n + 1);
    onChanged();
  }

  // A cash order's prepaid delivery is paid by card too (ADR-0067).
  const deposit = order.payment_method === 'cod' && (order.deposit_minor ?? 0) > 0;
  if (order.payment_method !== 'card' && !deposit) return null;
  const status = deposit ? (order.deposit_status ?? 'awaiting') : order.payment_status;
  return (
    <section className="card">
      <h2>{deposit ? 'التوصيل المدفوع مقدمًا' : 'الدفع بالبطاقة'}</h2>
      <p>
        حالة الدفع: <span className={`status pay-${status}`}>{orderPaymentLabel[status]}</span>
      </p>
      {deposit && (
        <small className="muted">
          طلبت قواعد الحماية أن يدفع العميل الشحن ورسوم الدفع عند الاستلام ({money(order.deposit_minor ?? 0, order.currency)}) بالبطاقة؛ يُلغى الطلب إن لم يدفع خلال ساعة.
        </small>
      )}
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {list.length === 0 && <p className="empty">لم يبدأ العميل الدفع بعد.</p>}
      {list.map(({ payment: p, refunds }) => {
        const left = refundable(p) - refunds.filter((r) => r.status === 'requested').reduce((s, r) => s + r.amount_minor, 0);
        return (
          <div className="panel" key={p.id}>
            <p>
              {gatewayLabel(p.gateway)} — {money(p.amount_minor, p.currency)} —{' '}
              <span className={`status pay-${p.status}`}>{paymentLabel[p.status]}</span> <small>{dateTime(p.created_at)}</small>
            </p>
            {p.transaction_id && (
              <p>
                رقم العملية: <span dir="ltr">{p.transaction_id}</span>
              </p>
            )}
            {p.failure && <p>السبب: {p.failure}</p>}
            {refunds.length > 0 && (
              <ul>
                {refunds.map((r) => (
                  <li key={r.id}>
                    استرداد {money(r.amount_minor, r.currency)} — {refundLabel[r.status]}
                    {r.reason && <q>{r.reason}</q>}
                    {r.failure && <small> ({r.failure})</small>}
                  </li>
                ))}
              </ul>
            )}
            {left > 0 && (
              <form className="refund" key={left} onSubmit={(e) => void refund(e, p.id, left)}>
                <label>
                  مبلغ الاسترداد
                  <input name="amount" inputMode="decimal" required defaultValue={toMajor(left)} />
                </label>
                <label>
                  السبب (اختياري)
                  <input name="reason" maxLength={300} />
                </label>
                <button type="submit" className="danger" disabled={busy}>
                  استرداد
                </button>
              </form>
            )}
          </div>
        );
      })}
    </section>
  );
}
