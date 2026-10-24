import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime, money } from '../lib/orders';
import { gatewayLabel, paymentLabel, type Payment } from '../lib/payments';
import { problemMessage } from '../lib/problem';

async function fetchPage(storeId: string, cursor?: string) {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/payments', {
      params: { path: { store_id: storeId }, query: { limit: 50, ...(cursor ? { cursor } : {}) } },
    });
    if (error) return { items: [] as Payment[], error: problemMessage(error) };
    return { items: data.items, next: data.next_cursor };
  } catch (err) {
    return { items: [] as Payment[], error: problemMessage(err) };
  }
}

// Payments lists the store's card payments, newest first; each links to
// its order, where it can be refunded.
export function Payments() {
  const { storeId = '' } = useParams();
  const [items, setItems] = useState<Payment[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let live = true;
    void fetchPage(storeId).then((p) => {
      if (!live) return;
      setItems(p.items);
      setNext(p.next);
      setError(p.error ?? '');
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function loadMore() {
    if (!next) return;
    setLoading(true);
    const p = await fetchPage(storeId, next);
    setItems((prev) => [...prev, ...p.items]);
    setNext(p.next);
    setLoading(false);
  }

  return (
    <Layout storeId={storeId}>
      <h1>مدفوعات البطاقات</h1>
      {error && <p role="alert">{error}</p>}
      {!loading && items.length === 0 && !error && <p className="empty">لا توجد مدفوعات بالبطاقة بعد.</p>}
      {items.length > 0 && (
        <table className="orders">
          <thead>
            <tr>
              <th>التاريخ</th>
              <th>البوابة</th>
              <th>المبلغ</th>
              <th>المسترد</th>
              <th>الحالة</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((p) => (
              <tr key={p.id}>
                <td>{dateTime(p.created_at)}</td>
                <td>{gatewayLabel(p.gateway)}</td>
                <td>{money(p.amount_minor, p.currency)}</td>
                <td>{p.refunded_minor > 0 ? money(p.refunded_minor, p.currency) : '—'}</td>
                <td>
                  <span className={`status pay-${p.status}`}>{paymentLabel[p.status]}</span>
                </td>
                <td>
                  <Link to={`/stores/${storeId}/orders/${p.order_id}`}>الطلب</Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {next && (
        <button type="button" disabled={loading} onClick={() => void loadMore()}>
          المزيد
        </button>
      )}
    </Layout>
  );
}
