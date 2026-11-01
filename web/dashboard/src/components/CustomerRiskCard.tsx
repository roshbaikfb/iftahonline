import { useEffect, useState } from 'react';

import { api } from '../api/client';
import { dateTime } from '../lib/orders';
import { bandLabel, type OrderRisk } from '../lib/risk';

// CustomerRiskCard is the order page's "customer rating" (ADR-0065): the
// band the platform's deliveries give the customer's phone, and what the
// customer did in this store. Another store's orders are never shown,
// only the band they help make. It reloads when the order moves.
export function CustomerRiskCard({ storeId, orderId, status }: { storeId: string; orderId: string; status: string }) {
  const [risk, setRisk] = useState<OrderRisk | null>(null);

  useEffect(() => {
    let live = true;
    void api
      .GET('/v1/stores/{store_id}/orders/{order_id}/risk', { params: { path: { store_id: storeId, order_id: orderId } } })
      .then(({ data }) => {
        if (live && data) setRisk(data);
      })
      .catch(() => undefined); // the card is an aid: without it the order page still works
    return () => {
      live = false;
    };
  }, [storeId, orderId, status]);

  if (!risk) return null;
  const pill = bandLabel[risk.band];
  const h = risk.history;
  const facts = [
    [h.delivered, 'استلم'],
    [h.returned, 'مرتجع'],
    [h.refused, 'رفض'],
    [h.no_answer, 'لم يرد'],
    [h.unconfirmed, 'لم يؤكد'],
  ] as const;
  return (
    <section className="card od-risk" aria-labelledby="h-risk">
      <div className="confirm-head">
        <h2 id="h-risk">تقييم العميل</h2>
        <span className={`status ${pill.cls}`}>{pill.label}</span>
      </div>
      <p className="risk-own">
        طلباته في متجرك: <strong>{h.orders}</strong>
        {facts
          .filter(([n]) => n > 0)
          .map(([n, label]) => (
            <span key={label}>
              {' · '}
              {label} {n}
            </span>
          ))}
      </p>
      {h.last_outcome_at && <small className="muted">آخر نتيجة: {dateTime(h.last_outcome_at)}</small>}
      <small className="muted">التقييم من نتائج التوصيل لهذا الرقم في كل المتاجر، دون كشف أي طلب من متجر آخر.</small>
    </section>
  );
}
