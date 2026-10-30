import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { conditionsLabel, couponState, discountLabel, validityLabel, type Coupon } from '../lib/coupons';
import { problemMessage } from '../lib/problem';

// Coupons lists the store's coupons (6g, the approved mockup): the code,
// the discount, its terms, how many orders used it, when it is valid and
// its state.
export function Coupons() {
  const { storeId = '' } = useParams();
  const [items, setItems] = useState<Coupon[] | null>(null);
  const [next, setNext] = useState('');
  const [store, setStore] = useState({ currency: 'EGP', timezone: 'Africa/Cairo' });
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([api.GET('/v1/stores/{store_id}/coupons', path), api.GET('/v1/stores/{store_id}', path)]).then(([c, s]) => {
      if (!live) return;
      if (s.data) setStore({ currency: s.data.currency, timezone: s.data.timezone });
      if (c.error) setError(problemMessage(c.error));
      else {
        setItems(c.data.items);
        setNext(c.data.next_cursor ?? '');
      }
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function more() {
    const { data, error: err } = await api.GET('/v1/stores/{store_id}/coupons', { params: { path: { store_id: storeId }, query: { cursor: next } } });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setItems([...(items ?? []), ...data.items]);
    setNext(data.next_cursor ?? '');
  }

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>الكوبونات</h1>
          <p className="muted">أكواد خصم يكتبها العميل في صفحة إتمام الطلب. كوبون واحد لكل طلب، والخصم على المنتجات فقط.</p>
        </div>
        <div className="actions">
          <Link className="button primary" to={`/stores/${storeId}/coupons/new`}>
            كوبون جديد
          </Link>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {items === null && !error && <p role="status">جارٍ التحميل…</p>}
      {items?.length === 0 && (
        <section className="card empty-state">
          <h2>لا كوبونات بعد</h2>
          <p className="muted">أنشئ كودًا مثل SUMMER10 وشاركه في إعلاناتك أو مع عملائك.</p>
          <Link className="button primary" to={`/stores/${storeId}/coupons/new`}>
            كوبون جديد
          </Link>
        </section>
      )}
      {items && items.length > 0 && (
        <section className="table-card">
          <div className="scroll">
            <table className="orders">
              <thead>
                <tr>
                  <th>الكود</th>
                  <th>الخصم</th>
                  <th>الشروط</th>
                  <th>الاستخدام</th>
                  <th>الصلاحية</th>
                  <th>الحالة</th>
                  <th>
                    <span className="visually-hidden">إجراءات</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {items.map((c) => {
                  const st = couponState(c, store.timezone);
                  const to = `/stores/${storeId}/coupons/${c.id}`;
                  return (
                    <tr key={c.id}>
                      <td>
                        <Link to={to} className="ccode" dir="ltr">
                          {c.code}
                        </Link>
                      </td>
                      <td>{discountLabel(c, store.currency)}</td>
                      <td className="muted">{conditionsLabel(c, store.currency)}</td>
                      <td>
                        <div className="uses">
                          {c.max_uses === undefined ? (
                            <>
                              <span>{c.uses}</span>
                              <small className="muted">بلا حد</small>
                            </>
                          ) : (
                            <>
                              <span>
                                {c.uses} من {c.max_uses}
                              </span>
                              <span className="bar" aria-hidden="true">
                                <span style={{ inlineSize: `${String(Math.min(100, Math.round((c.uses / c.max_uses) * 100)))}%` }} />
                              </span>
                            </>
                          )}
                        </div>
                      </td>
                      <td className="muted">{validityLabel(c, store.timezone)}</td>
                      <td>
                        <span className={`status ${st.cls}`}>{st.label}</span>
                      </td>
                      <td>
                        <Link to={to} aria-label={`تعديل ${c.code}`}>
                          تعديل
                        </Link>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="foot">
            <span className="muted small">الكوبون المستخدم في طلب لا يُحذف: أوقفه بدلًا من ذلك. يعود الاستخدام إن أُلغي الطلب قبل الشحن.</span>
            {next && (
              <button type="button" className="secondary small" onClick={() => void more()}>
                المزيد
              </button>
            )}
          </div>
        </section>
      )}
    </Layout>
  );
}
