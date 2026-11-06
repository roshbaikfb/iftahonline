import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { addLine, maxLines, maxQty, orderMessage, picks, soldOut, subtotal, type Line, type Pick } from '../lib/neworder';
import { money } from '../lib/orders';
import { canManageOrders, useRole } from '../lib/settings';

type Area = components['schemas']['ShippingArea'];
type Quote = components['schemas']['ShippingQuote'];

interface Customer {
  phone: string;
  name: string;
  governorate: string;
  city: string;
  address: string;
  notes: string;
}

// orKept is the first text that is not empty: what the merchant typed,
// else what the store knows.
function orKept(...texts: (string | undefined)[]): string {
  return texts.find((t) => t !== undefined && t !== '') ?? '';
}

const empty: Customer = { phone: '', name: '', governorate: '', city: '', address: '', notes: '' };

// NewOrder enters an order that came by phone or WhatsApp (D5, the
// approved mockup): the customer (a known number fills in their last
// address), products searched by name, and the summary the storefront's
// checkout would show. It is cash on delivery and starts pending.
export function NewOrder() {
  const { storeId = '' } = useParams();
  const navigate = useNavigate();
  const allowed = canManageOrders(useRole(storeId));
  const [areas, setAreas] = useState<Area[]>([]);
  const [currency, setCurrency] = useState('');
  const [c, setC] = useState<Customer>(empty);
  const [known, setKnown] = useState<number | null>(null);
  const [query, setQuery] = useState('');
  const [found, setFound] = useState<Pick[] | null>(null);
  const [lines, setLines] = useState<Line[]>([]);
  const [typed, setTyped] = useState<Partial<Record<string, string>>>({});
  const [quote, setQuote] = useState<Quote | null>(null);
  const [coupon, setCoupon] = useState('');
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/shipping', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) {
        setAreas(data.areas);
        setCurrency(data.currency);
      }
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  // Products by name, a moment after typing stops.
  useEffect(() => {
    const q = query.trim();
    if (q.length < 2) return undefined;
    let live = true;
    const t = window.setTimeout(() => {
      void (async () => {
        const { data } = await api.GET('/v1/stores/{store_id}/products/search', { params: { path: { store_id: storeId }, query: { q, limit: 5 } } });
        const details = await Promise.all(
          (data?.items ?? []).map((h) => api.GET('/v1/stores/{store_id}/products/{product_id}', { params: { path: { store_id: storeId, product_id: h.id } } })),
        );
        if (live) setFound(picks(details.flatMap((d) => (d.data ? [d.data] : []))));
      })();
    }, 300);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [query, storeId]);

  // Delivery to the governorate, for the items' total.
  const sub = subtotal(lines);
  useEffect(() => {
    if (!c.governorate) return undefined;
    let live = true;
    void api
      .POST('/v1/stores/{store_id}/shipping/quote', {
        params: { path: { store_id: storeId } },
        body: { governorate: c.governorate, subtotal_minor: sub, ...(c.city.trim() ? { city: c.city.trim() } : {}) },
      })
      .then(({ data }) => {
        if (live) setQuote(data ?? null);
      });
    return () => {
      live = false;
    };
  }, [storeId, c.governorate, c.city, sub]);

  // A number the store knows: its name and last address, where the form is
  // still empty.
  async function lookUp() {
    const phone = c.phone.trim();
    if (phone.replace(/\D/g, '').length < 8) return;
    const path = { store_id: storeId };
    const [{ data: people }, { data: orders }] = await Promise.all([
      api.GET('/v1/stores/{store_id}/customers', { params: { path, query: { q: phone } } }),
      api.GET('/v1/stores/{store_id}/orders', { params: { path, query: { phone, limit: 1 } } }),
    ]);
    const who = people?.items[0];
    const last = orders?.items[0];
    setKnown(who ? who.orders_count : null);
    setC((now) => ({
      ...now,
      name: orKept(now.name, who?.name, last?.name),
      governorate: orKept(now.governorate, last?.governorate),
      city: orKept(now.city, last?.city),
      address: orKept(now.address, last?.address),
    }));
  }

  function set(k: keyof Customer, v: string) {
    setC((now) => ({ ...now, [k]: v }));
  }

  async function create(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (lines.length === 0) {
      setError('أضف منتجًا واحدًا على الأقل.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const { data, error: err } = await api.POST('/v1/stores/{store_id}/orders', {
        params: { path: { store_id: storeId }, header: { 'Idempotency-Key': key } },
        body: {
          name: c.name.trim(),
          phone: c.phone.trim(),
          governorate: c.governorate,
          city: c.city.trim(),
          address: c.address.trim(),
          notes: c.notes.trim(),
          ...(coupon.trim() ? { coupon_code: coupon.trim() } : {}),
          lines: lines.map((l) => ({ variant_id: l.variantId, qty: l.qty })),
        },
      });
      if (data) {
        void navigate(`/stores/${storeId}/orders/${data.order.id}`);
        return;
      }
      // Refused: nothing was made, so the next try is a new request.
      setKey(crypto.randomUUID());
      setError(orderMessage(err));
    } catch (err) {
      // The network failed: the same key makes a retry safe.
      setError(orderMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const cur = orKept(currency, lines[0]?.currency, 'EGP');
  const shownFound = query.trim().length >= 2 ? found : null;
  const shownQuote = c.governorate ? quote : null;
  const shipping = shownQuote?.deliverable ? shownQuote.shipping_minor : 0;
  const cod = shownQuote?.deliverable ? shownQuote.cod_fee_minor : 0;
  const areaName = areas.find((a) => a.code === c.governorate)?.name_ar;

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <Link to={`/stores/${storeId}/orders`} className="back">
            → الطلبات
          </Link>
          <h1>طلب جديد</h1>
          <p className="muted">لطلب وصلك بالتليفون أو واتساب. الأسعار من منتجاتك، والدفع عند الاستلام.</p>
        </div>
      </div>
      {!allowed && <p className="notice">إنشاء الطلبات لصاحب المتجر والمدير وخدمة العملاء.</p>}
      {allowed && (
        <form className="new-order" onSubmit={(e) => void create(e)}>
          <div className="new-order-main">
            <section className="card" aria-labelledby="no-customer">
              <h2 id="no-customer">العميل</h2>
              <div className="pair">
                <label>
                  الموبايل
                  <input
                    type="tel"
                    required
                    maxLength={64}
                    dir="ltr"
                    value={c.phone}
                    onChange={(e) => {
                      set('phone', e.target.value);
                      setKnown(null);
                    }}
                    onBlur={() => void lookUp()}
                  />
                </label>
                <label>
                  الاسم
                  <input
                    required
                    maxLength={120}
                    value={c.name}
                    onChange={(e) => {
                      set('name', e.target.value);
                    }}
                  />
                </label>
              </div>
              {known !== null && known > 0 && (
                <p className="muted">
                  عميل سابق: {known === 1 ? 'طلب واحد' : known === 2 ? 'طلبان' : `${String(known)} طلبات`}. ملأنا عنوانه الأخير، ويمكنك تعديله.
                </p>
              )}
              <div className="pair">
                <label>
                  المحافظة
                  <select
                    required
                    value={c.governorate}
                    onChange={(e) => {
                      set('governorate', e.target.value);
                    }}
                  >
                    <option value="">اختر</option>
                    {areas.map((a) => (
                      <option key={a.code} value={a.code}>
                        {a.name_ar}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  المدينة أو المنطقة
                  <input
                    required
                    maxLength={80}
                    value={c.city}
                    onChange={(e) => {
                      set('city', e.target.value);
                    }}
                  />
                </label>
              </div>
              <label>
                العنوان
                <input
                  required
                  maxLength={300}
                  value={c.address}
                  onChange={(e) => {
                    set('address', e.target.value);
                  }}
                />
              </label>
              <label>
                ملاحظات (اختياري)
                <textarea
                  rows={2}
                  maxLength={500}
                  value={c.notes}
                  onChange={(e) => {
                    set('notes', e.target.value);
                  }}
                />
              </label>
            </section>

            <section className="card" aria-labelledby="no-products">
              <h2 id="no-products">المنتجات</h2>
              <label>
                أضف منتجًا
                <input
                  type="search"
                  placeholder="ابحث بالاسم"
                  value={query}
                  onChange={(e) => {
                    setQuery(e.target.value);
                  }}
                />
              </label>
              {shownFound !== null &&
                (shownFound.length === 0 ? (
                  <p className="muted">لا منتجات بهذا الاسم.</p>
                ) : (
                  <ul className="picks" role="listbox" aria-label="نتائج البحث">
                    {shownFound.map((p) => (
                      <li key={p.variantId} role="option" aria-selected={false} aria-disabled={soldOut(p)}>
                        <button
                          type="button"
                          disabled={soldOut(p) || lines.length >= maxLines}
                          onClick={() => {
                            setLines((now) => addLine(now, p));
                          }}
                        >
                          <span>{p.name}</span>
                          <span className="muted">
                            {soldOut(p) ? 'نفد' : `${money(p.priceMinor, p.currency)}${p.available === null ? '' : ` · متاح ${String(p.available)}`}`}
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                ))}
              {lines.length > 0 && (
                <div className="table-wrap">
                  <table className="orders">
                    <thead>
                      <tr>
                        <th scope="col">المنتج</th>
                        <th scope="col">الكمية</th>
                        <th scope="col">السعر</th>
                        <th scope="col">
                          <span className="sr">إزالة</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {lines.map((l) => (
                        <tr key={l.variantId}>
                          <td>{l.name}</td>
                          <td>
                            <input
                              type="number"
                              className="qty"
                              min={1}
                              max={l.available === null ? maxQty : Math.min(maxQty, l.available)}
                              value={typed[l.variantId] ?? String(l.qty)}
                              aria-label={`كمية ${l.name}`}
                              onChange={(e) => {
                                // Typing may empty the field for a moment; only a number changes the line.
                                const text = e.target.value;
                                setTyped((now) => ({ ...now, [l.variantId]: text }));
                                const n = Math.floor(Number(text));
                                if (text === '' || !Number.isFinite(n) || n < 1) return;
                                const qty = Math.min(maxQty, n);
                                setLines((now) => now.map((x) => (x.variantId === l.variantId ? { ...x, qty } : x)));
                              }}
                              onBlur={() => {
                                setTyped((now) => ({ ...now, [l.variantId]: undefined }));
                              }}
                            />
                          </td>
                          <td>{money(l.priceMinor * l.qty, l.currency)}</td>
                          <td>
                            <button
                              type="button"
                              className="link"
                              aria-label={`أزل ${l.name}`}
                              onClick={() => {
                                setLines((now) => now.filter((x) => x.variantId !== l.variantId));
                              }}
                            >
                              ×
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          </div>

          <aside className="card new-order-sum" aria-labelledby="no-summary">
            <h2 id="no-summary">الملخص</h2>
            <div className="sum-row">
              <span>المنتجات</span>
              <span>{money(sub, cur)}</span>
            </div>
            <div className="sum-row">
              <span>الشحن{areaName ? ` (${areaName})` : ''}</span>
              <span>{shownQuote === null ? '—' : !shownQuote.deliverable ? 'لا توصيل' : shownQuote.free_shipping ? 'مجاني' : money(shipping, cur)}</span>
            </div>
            <div className="sum-row">
              <span>رسوم الدفع عند الاستلام</span>
              <span>{shownQuote === null ? '—' : money(cod, cur)}</span>
            </div>
            {shownQuote && !shownQuote.deliverable && <p role="alert">لا توصيل لهذه المحافظة في إعدادات الشحن.</p>}
            <label>
              كوبون (اختياري)
              <input
                dir="ltr"
                maxLength={32}
                value={coupon}
                onChange={(e) => {
                  setCoupon(e.target.value);
                }}
              />
            </label>
            {coupon.trim() && <p className="muted small">يُخصم الكوبون من الإجمالي عند إنشاء الطلب.</p>}
            <div className="sum-row total">
              <strong>الإجمالي</strong>
              <strong>{money(sub + shipping + cod, cur)}</strong>
            </div>
            <p className="muted small">نحسبها كما في صفحة الدفع بمتجرك. يُحجز المخزون عند الإنشاء.</p>
            {error && <p role="alert">{error}</p>}
            <button type="submit" disabled={busy || (shownQuote !== null && !shownQuote.deliverable)}>
              {busy ? 'جارٍ الإنشاء…' : 'أنشئ الطلب'}
            </button>
            <p className="muted small">يظهر في صفحة الطلبات «قيد الانتظار» كأي طلب، ويمر بالتأكيد كالمعتاد.</p>
          </aside>
        </form>
      )}
    </Layout>
  );
}
