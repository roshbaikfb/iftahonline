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
      <div className="page-head">
        <h1>المدفوعات</h1>
        <Link className="button" to={`/stores/${storeId}/gateways`}>
          إعدادات الدفع بالبطاقة
        </Link>
      </div>
      {error && <p role="alert">{error}</p>}
      <section className="table-card">
        {items.length > 0 && (
          <div className="scroll">
            <table className="orders">
              <thead>
                <tr>
                  <th>الطلب</th>
                  <th>البوابة</th>
                  <th>المبلغ</th>
                  <th>المسترد</th>
                  <th>الحالة</th>
                  <th>التاريخ</th>
                </tr>
              </thead>
              <tbody>
                {items.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <Link to={`/stores/${storeId}/orders/${p.order_id}`}>الطلب</Link>
                    </td>
                    <td>{gatewayLabel(p.gateway)}</td>
                    <td className="num">{money(p.amount_minor, p.currency)}</td>
                    <td className="num">{p.refunded_minor > 0 ? money(p.refunded_minor, p.currency) : '—'}</td>
                    <td>
                      <span className={`status pay-${p.status}`}>{paymentLabel[p.status]}</span>
                    </td>
                    <td className="muted">{dateTime(p.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {loading && <p role="status" className="foot">جارٍ التحميل…</p>}
        {!loading && items.length === 0 && !error && <p className="empty flat">لا توجد مدفوعات بالبطاقة بعد.</p>}
        {!loading && items.length > 0 && (
          <div className="foot">
            <span>عرض {items.length} دفعة</span>
            {next && (
              <button type="button" className="secondary" onClick={() => void loadMore()}>
                تحميل المزيد
              </button>
            )}
          </div>
        )}
      </section>
    </Layout>
  );
}
