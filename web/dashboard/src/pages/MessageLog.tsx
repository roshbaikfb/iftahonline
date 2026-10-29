import { useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import {
  channelLabel,
  failureLabel,
  messageKindLabel,
  messageStatusLabel,
  messageTabs,
  orderCode,
  type CustomerMessage,
  type MessageStatus,
} from '../lib/messages';
import { dateTime } from '../lib/orders';
import { problemMessage } from '../lib/problem';

const pageSize = 50;

// fetchPage reads a page of the log: one status's (all with ''), or one
// order's whole (no paging).
async function fetchPage(storeId: string, status: MessageStatus | '', order: string, cursor: string) {
  const { data, error } = await api.GET('/v1/stores/{store_id}/messages', {
    params: {
      path: { store_id: storeId },
      query: order ? { order_id: order } : { limit: pageSize, ...(status && { status }), ...(cursor && { cursor }) },
    },
  });
  return error ? { error: problemMessage(error) } : { data };
}

// MessageLog lists the messages sent to customers (6g, the approved
// mockup), newest first, by status; ?order= shows one order's.
export function MessageLog() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const status = (params.get('status') ?? '') as MessageStatus | '';
  const order = params.get('order') ?? '';
  // The page read, for the view it was read for: another tab or order
  // shows "loading" until its own arrives.
  const view = `${status}|${order}`;
  const [page, setPage] = useState<{ view: string; items: CustomerMessage[]; next: string } | null>(null);
  const [error, setError] = useState('');
  const items = page?.view === view ? page.items : null;
  const next = page?.view === view ? page.next : '';

  useEffect(() => {
    let live = true;
    void fetchPage(storeId, status, order, '').then((r) => {
      if (!live) return;
      setError(r.error ?? '');
      if (r.data) setPage({ view: `${status}|${order}`, items: r.data.items, next: r.data.next_cursor ?? '' });
    });
    return () => {
      live = false;
    };
  }, [storeId, status, order]);

  async function more() {
    const r = await fetchPage(storeId, status, order, next);
    setError(r.error ?? '');
    if (!r.data) return;
    setPage({ view, items: [...(items ?? []), ...r.data.items], next: r.data.next_cursor ?? '' });
  }

  return (
    <Layout storeId={storeId}>
      <nav className="crumbs" aria-label="مسار الصفحة">
        <Link to={`/stores/${storeId}/messages`}>رسائل العملاء</Link> / <span>السجل</span>
      </nav>
      <div className="page-head">
        <div>
          <h1>سجل الرسائل</h1>
          <p className="muted">كل رسالة أرسلناها لعملائك، وما حدث لها. أرقام العملاء تظهر مخفية جزئيًا.</p>
        </div>
      </div>

      {order ? (
        <p className="filter-chip">
          رسائل الطلب{' '}
          <Link to={`/stores/${storeId}/orders/${order}`} dir="ltr" className="code">
            #{orderCode(order)}
          </Link>{' '}
          · <Link to={`/stores/${storeId}/messages/log`}>عرض كل الرسائل</Link>
        </p>
      ) : (
        <div className="tabs" role="tablist" aria-label="حالة الرسالة">
          {messageTabs.map((t) => (
            <button
              key={t.status || 'all'}
              type="button"
              role="tab"
              aria-selected={status === t.status}
              onClick={() => {
                setParams(t.status ? { status: t.status } : {});
              }}
            >
              {t.label}
            </button>
          ))}
        </div>
      )}
      {error && <p role="alert">{error}</p>}

      <section className="table-card">
        <div className="scroll">
          <table className="orders">
            <thead>
              <tr>
                <th>الوقت</th>
                <th>الطلب</th>
                <th>العميل</th>
                <th>الرسالة</th>
                <th>القناة</th>
                <th>الحالة</th>
                <th>رد العميل</th>
              </tr>
            </thead>
            <tbody>
              {(items ?? []).map((m) => (
                <tr key={m.id}>
                  <td className="muted">{dateTime(m.created_at)}</td>
                  <td>
                    {m.order_id ? (
                      <Link to={`/stores/${storeId}/orders/${m.order_id}`} dir="ltr" className="code">
                        #{orderCode(m.order_id)}
                      </Link>
                    ) : (
                      '—'
                    )}
                  </td>
                  <td>
                    <span dir="ltr">{m.to}</span>
                  </td>
                  <td>{messageKindLabel[m.kind]}</td>
                  <td>
                    <span className="chan">
                      {channelLabel(m.channel)} {m.fallback && <small>بديل عن واتساب</small>}
                    </span>
                  </td>
                  <td>
                    <span className={`status m-${m.status}`}>{messageStatusLabel[m.status]}</span>
                    {m.status === 'failed' && <small className="sub">{failureLabel(m.error_code)}</small>}
                  </td>
                  <td>
                    {m.reply === 'confirm' && <span className="reply-yes">أكّد</span>}
                    {m.reply === 'cancel' && <span className="reply-no">ألغى</span>}
                    {!m.reply && <span className="muted">—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {items === null && !error && <p role="status">جارٍ التحميل…</p>}
        {items?.length === 0 && <p className="empty flat">لا رسائل هنا بعد.</p>}
        <div className="foot">
          <span className="muted small">يُحفظ السجل ستة أشهر.</span>
          {next && (
            <button type="button" className="secondary small" onClick={() => void more()}>
              المزيد
            </button>
          )}
        </div>
      </section>
    </Layout>
  );
}
