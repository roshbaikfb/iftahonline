import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { BlockDialog } from '../components/BlockDialog';
import { CustomerRiskCard } from '../components/CustomerRiskCard';
import { Layout } from '../components/Layout';
import { initials, ordersRead, summarize, telLink, waLink, type Customer } from '../lib/customers';
import { dateTime, money, statusLabel, type Order } from '../lib/orders';
import { problemMessage } from '../lib/problem';

// CustomerDetail is one customer (the approved mockup): their number to
// call or message, what their orders came to (summed over the latest
// 100), the platform's rating of their phone, their orders newest first,
// and the block with its reason.
export function CustomerDetail() {
  const { storeId = '', customerId = '' } = useParams();
  const [customer, setCustomer] = useState<Customer | null>(null);
  const [orders, setOrders] = useState<Order[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [blocking, setBlocking] = useState(false);

  useEffect(() => {
    let live = true;
    void Promise.all([
      api.GET('/v1/stores/{store_id}/customers/{customer_id}', { params: { path: { store_id: storeId, customer_id: customerId } } }),
      api.GET('/v1/stores/{store_id}/orders', {
        params: { path: { store_id: storeId }, query: { customer_id: customerId, limit: ordersRead } },
      }),
    ]).then(([c, o]) => {
      if (!live) return;
      if (c.data) setCustomer(c.data);
      else setError(problemMessage(c.error));
      if (o.data) setOrders(o.data.items);
    });
    return () => {
      live = false;
    };
  }, [storeId, customerId]);

  async function block(input: { reason: string }): Promise<string | null> {
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/customers/{customer_id}/block', {
      params: { path: { store_id: storeId, customer_id: customerId } },
      body: { reason: input.reason },
    });
    if (err) return problemMessage(err);
    setCustomer(data);
    setBlocking(false);
    setNotice('حُظر العميل. لن يستطيع الطلب من متجرك.');
    return null;
  }

  async function unblock() {
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/customers/{customer_id}/unblock', {
      params: { path: { store_id: storeId, customer_id: customerId } },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setCustomer(data);
    setNotice('أُلغي الحظر.');
  }

  const sum = summarize(orders);
  const currency = orders[0]?.currency ?? 'EGP';
  const partial = orders.length >= ordersRead && customer !== null && customer.orders_count > orders.length;

  return (
    <Layout storeId={storeId}>
      <nav className="crumbs muted small" aria-label="مسار الصفحة">
        <Link to={`/stores/${storeId}/customers`}>العملاء</Link> / {customer ? customer.name || customer.phone : '…'}
      </nav>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}

      {customer && (
        <>
          <section className="card customer-head" aria-label="العميل">
            <div className="who">
              <span className="avatar big" aria-hidden="true">
                {initials(customer.name)}
              </span>
              <div>
                <h1>{customer.name || '—'}</h1>
                <span className="ltr-cell">{customer.phone}</span>
                <span className="muted small">عميل منذ {dateTime(customer.created_at)}</span>
              </div>
            </div>
            <div className="actions">
              <a className="button secondary" href={telLink(customer.phone)}>
                <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M5 4h4l2 5-2.5 1.5a11 11 0 0 0 5 5L15 13l5 2v4a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2" />
                </svg>
                اتصال
              </a>
              <a className="button success" href={waLink(customer.phone)} target="_blank" rel="noopener noreferrer">
                <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M21 12a9 9 0 0 1-13.5 7.8L3 21l1.2-4.4A9 9 0 1 1 21 12z" />
                </svg>
                واتساب
              </a>
              {customer.blocked ? (
                <button type="button" className="secondary" onClick={() => void unblock()}>
                  إلغاء الحظر
                </button>
              ) : (
                <button
                  type="button"
                  className="danger"
                  onClick={() => {
                    setBlocking(true);
                  }}
                >
                  <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                    <circle cx="12" cy="12" r="9" />
                    <path d="M5.6 5.6l12.8 12.8" />
                  </svg>
                  حظر العميل
                </button>
              )}
            </div>
          </section>

          {customer.blocked && (
            <p className="notice blocked-note" role="note">
              <strong>محظور</strong>
              {customer.blocked_at ? ` منذ ${dateTime(customer.blocked_at)}` : ''}
              {customer.block_reason ? ` · ${customer.block_reason}` : ''} — لا يستطيع الطلب من متجرك، ويرى رسالة عامة لا تذكر الحظر.
            </p>
          )}

          <section className="customer-stats" aria-label="ملخص">
            <div className="card">
              <span className="muted">الطلبات</span>
              <strong>{customer.orders_count}</strong>
            </div>
            <div className="card">
              <span className="muted">سُلّمت</span>
              <strong className="ok">{sum.delivered}</strong>
            </div>
            <div className="card">
              <span className="muted">رُفضت أو رجعت</span>
              <strong className="warn">{sum.failed}</strong>
            </div>
            <div className="card">
              <span className="muted">دفع في المسلّم</span>
              <strong>{money(sum.deliveredMinor, currency)}</strong>
            </div>
          </section>
          {partial && <p className="muted small">الملخص من آخر {orders.length} طلب.</p>}

          {orders[0] && <CustomerRiskCard storeId={storeId} orderId={orders[0].id} status={orders[0].status} />}

          <section className="table-card" aria-labelledby="h-orders">
            <div className="card-head">
              <h2 id="h-orders">طلباته</h2>
              <span className="muted small">الأحدث أولًا</span>
            </div>
            {orders.length === 0 ? (
              <p className="empty flat">لا توجد طلبات.</p>
            ) : (
              <div className="scroll">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">الطلب</th>
                      <th scope="col">التاريخ</th>
                      <th scope="col">الحالة</th>
                      <th scope="col">الإجمالي</th>
                    </tr>
                  </thead>
                  <tbody>
                    {orders.map((o) => (
                      <tr key={o.id}>
                        <td>
                          <Link to={`/stores/${storeId}/orders/${o.id}`} className="ltr-cell">
                            #{o.code}
                          </Link>
                        </td>
                        <td className="muted">{dateTime(o.created_at)}</td>
                        <td>
                          <span className={`status s-${o.status}`}>{statusLabel[o.status]}</span>
                        </td>
                        <td>
                          <strong>{money(o.total_minor, o.currency)}</strong>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}

      {blocking && (
        <BlockDialog
          title="حظر العميل"
          intro="لن يستطيع هذا الرقم الطلب من متجرك. يمكنك إلغاء الحظر في أي وقت."
          onBlock={block}
          onClose={() => {
            setBlocking(false);
          }}
        />
      )}
    </Layout>
  );
}
