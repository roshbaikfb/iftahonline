import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { BlockDialog } from '../components/BlockDialog';
import { Layout } from '../components/Layout';
import { initials, telLink, waLink, type Customer } from '../lib/customers';
import { dateTime } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

interface Page {
  items: Customer[];
  next?: string | undefined;
  error?: string;
}

async function fetchPage(storeId: string, blocked: boolean, q: string, cursor?: string): Promise<Page> {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/customers', {
      params: { path: { store_id: storeId }, query: { blocked, limit: 50, ...(q ? { q } : {}), ...(cursor ? { cursor } : {}) } },
    });
    if (error) return { items: [], error: problemMessage(error) };
    return { items: data.items, next: data.next_cursor };
  } catch (err) {
    return { items: [], error: problemMessage(err) };
  }
}

// Customers lists the store's customers (the approved mockup): everyone
// who ordered, by phone, newest first, searchable by name or number; the
// block list in its own tab; a number blocked before it ever orders.
// The tab and the search live in the URL.
export function Customers() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const blocked = params.get('tab') === 'blocked';
  const q = params.get('q') ?? '';
  const [items, setItems] = useState<Customer[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState(true);
  const [blocking, setBlocking] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void fetchPage(storeId, blocked, q).then((p) => {
      if (!live) return;
      setItems(p.items);
      setNext(p.next);
      setError(p.error ?? '');
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [storeId, blocked, q, reloads]);

  function show(tab: 'all' | 'blocked') {
    setLoading(true);
    const p = new URLSearchParams(params);
    if (tab === 'blocked') p.set('tab', 'blocked');
    else p.delete('tab');
    setParams(p);
  }

  function search(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const text = field(new FormData(e.currentTarget), 'q').trim();
    const p = new URLSearchParams(params);
    if (text) p.set('q', text);
    else p.delete('q');
    setLoading(true);
    setParams(p);
  }

  async function more() {
    if (!next) return;
    const p = await fetchPage(storeId, blocked, q, next);
    if (p.error) {
      setError(p.error);
      return;
    }
    setItems((list) => [...list, ...p.items]);
    setNext(p.next);
  }

  async function unblock(c: Customer) {
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/customers/{customer_id}/unblock', {
      params: { path: { store_id: storeId, customer_id: c.id } },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setItems((list) => list.filter((x) => x.id !== c.id));
    setNotice(`أُلغي حظر ${c.name || c.phone}.`);
  }

  async function blockNumber(input: { phone: string; reason: string }): Promise<string | null> {
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/customers/blocks', {
      params: { path: { store_id: storeId } },
      body: { phone: input.phone, reason: input.reason },
    });
    if (err) return problemMessage(err);
    setBlocking(false);
    setNotice(`حُظر الرقم ${data.phone}.`);
    if (blocked) setReloads((n) => n + 1);
    return null;
  }

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>العملاء</h1>
          <p className="muted">كل من طلب من متجرك، برقم هاتفه. الأحدث أولًا.</p>
        </div>
        <button
          type="button"
          className="secondary"
          onClick={() => {
            setBlocking(true);
          }}
        >
          حظر رقم
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}

      <section className="table-card customers" aria-label="العملاء">
        <div className="customers-bar">
          <div className="tabs" role="tablist" aria-label="العرض">
            <button
              type="button"
              role="tab"
              aria-selected={!blocked}
              onClick={() => {
                show('all');
              }}
            >
              كل العملاء
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={blocked}
              onClick={() => {
                show('blocked');
              }}
            >
              المحظورون
            </button>
          </div>
          {blocked ? (
            <p className="muted small">المحظور لا يستطيع الطلب من متجرك، ويرى رسالة عامة لا تذكر الحظر.</p>
          ) : (
            <form className="customers-search" role="search" onSubmit={search} key={q}>
              <label className="grow">
                <span className="visually-hidden">ابحث بالاسم أو رقم الهاتف</span>
                <input name="q" type="search" defaultValue={q} placeholder="ابحث بالاسم أو رقم الهاتف" maxLength={64} />
              </label>
              <button type="submit" className="secondary">
                بحث
              </button>
            </form>
          )}
        </div>

        {loading ? (
          <p className="empty flat">جارٍ التحميل…</p>
        ) : items.length === 0 ? (
          <p className="empty flat">{blocked ? 'لا يوجد عملاء محظورون.' : q ? 'لا يوجد عميل بهذا الاسم أو الرقم.' : 'لم يطلب أحد من متجرك بعد.'}</p>
        ) : (
          <div className="scroll">
            <table>
              <thead>
                {blocked ? (
                  <tr>
                    <th scope="col">العميل</th>
                    <th scope="col">الهاتف</th>
                    <th scope="col">السبب</th>
                    <th scope="col">حُظر في</th>
                    <th scope="col">
                      <span className="visually-hidden">إجراء</span>
                    </th>
                  </tr>
                ) : (
                  <tr>
                    <th scope="col">العميل</th>
                    <th scope="col">الهاتف</th>
                    <th scope="col">الطلبات</th>
                    <th scope="col">آخر طلب</th>
                    <th scope="col">
                      <span className="visually-hidden">تواصل</span>
                    </th>
                  </tr>
                )}
              </thead>
              <tbody>
                {items.map((c) =>
                  blocked ? (
                    <tr key={c.id}>
                      <td>
                        <Link to={`/stores/${storeId}/customers/${c.id}`}>
                          <strong>{c.name || '—'}</strong>
                        </Link>
                      </td>
                      <td className="ltr-cell">{c.phone}</td>
                      <td className="muted">{c.block_reason || '—'}</td>
                      <td className="muted">{c.blocked_at ? dateTime(c.blocked_at) : '—'}</td>
                      <td className="end">
                        <button type="button" className="link-button" onClick={() => void unblock(c)}>
                          إلغاء الحظر
                        </button>
                      </td>
                    </tr>
                  ) : (
                    <tr key={c.id}>
                      <td>
                        <Link className="customer-cell" to={`/stores/${storeId}/customers/${c.id}`}>
                          <span className="avatar" aria-hidden="true">
                            {initials(c.name)}
                          </span>
                          <span>
                            <strong>{c.name || '—'}</strong>
                            {c.blocked && <span className="status blocked">محظور</span>}
                          </span>
                        </Link>
                      </td>
                      <td className="ltr-cell">{c.phone}</td>
                      <td>
                        <strong>{c.orders_count}</strong>
                      </td>
                      <td className="muted">{c.last_order_at ? dateTime(c.last_order_at) : '—'}</td>
                      <td className="end">
                        <span className="contact">
                          <a className="icon-link" href={telLink(c.phone)} aria-label={`اتصال بـ ${c.name || c.phone}`}>
                            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                              <path d="M5 4h4l2 5-2.5 1.5a11 11 0 0 0 5 5L15 13l5 2v4a2 2 0 0 1-2 2A16 16 0 0 1 3 6a2 2 0 0 1 2-2" />
                            </svg>
                          </a>
                          <a className="icon-link wa" href={waLink(c.phone)} target="_blank" rel="noopener noreferrer" aria-label={`واتساب ${c.name || c.phone}`}>
                            <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                              <path d="M21 12a9 9 0 0 1-13.5 7.8L3 21l1.2-4.4A9 9 0 1 1 21 12z" />
                            </svg>
                          </a>
                        </span>
                      </td>
                    </tr>
                  ),
                )}
              </tbody>
            </table>
          </div>
        )}
        {next && (
          <div className="foot center">
            <button type="button" className="secondary" onClick={() => void more()}>
              عرض المزيد
            </button>
          </div>
        )}
      </section>

      {blocking && (
        <BlockDialog
          title="حظر رقم"
          intro="احظر رقمًا يزعجك حتى لو لم يطلب من متجرك من قبل. لن يستطيع الطلب منك."
          askPhone
          onBlock={blockNumber}
          onClose={() => {
            setBlocking(false);
          }}
        />
      )}
    </Layout>
  );
}
