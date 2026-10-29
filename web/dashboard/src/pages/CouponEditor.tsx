import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { effectOn, emptyForm, normalizeCode, randomCode, serverErrors, summaryLines, toForm, toInput, type Coupon, type CouponKind, type Form } from '../lib/coupons';
import { timezones } from '../lib/settings';
import { problemMessage } from '../lib/problem';

const kinds: { id: CouponKind; label: string; short: string }[] = [
  { id: 'percent', label: 'نسبة مئوية', short: 'نسبة' },
  { id: 'fixed', label: 'مبلغ ثابت', short: 'مبلغ' },
  { id: 'free_shipping', label: 'شحن مجاني', short: 'شحن مجاني' },
];

const symbol: Record<string, string> = { EGP: 'ج.م', SAR: 'ر.س', AED: 'د.إ' };

// The shopper's preview prices an order whose items cost 500.
const sampleSubtotal = 50000;

// CouponEditor makes or changes a coupon (6g, the approved mockups): its
// code and discount, its terms and dates in the store's time zone, on or
// off, with a live summary and what the shopper sees at checkout. On a
// phone the summary leads and the buttons stay at the bottom.
export function CouponEditor() {
  const { storeId = '', couponId } = useParams();
  const navigate = useNavigate();
  const [store, setStore] = useState({ currency: 'EGP', timezone: 'Africa/Cairo', slug: '' });
  const [coupon, setCoupon] = useState<Coupon | null>(null);
  const [form, setForm] = useState<Form | null>(couponId ? null : emptyForm);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    const loadCoupon = couponId
      ? api.GET('/v1/stores/{store_id}/coupons/{coupon_id}', { params: { path: { store_id: storeId, coupon_id: couponId } } })
      : Promise.resolve(null);
    void Promise.all([api.GET('/v1/stores/{store_id}', path), loadCoupon]).then(([s, c]) => {
      if (!live || !s.data) return;
      const st = { currency: s.data.currency, timezone: s.data.timezone, slug: s.data.slug };
      setStore(st);
      if (!c) return;
      if (c.error) {
        setError(problemMessage(c.error));
        return;
      }
      setCoupon(c.data);
      setForm(toForm(c.data, st.timezone));
    });
    return () => {
      live = false;
    };
  }, [storeId, couponId]);

  const list = `/stores/${storeId}/coupons`;
  const title = couponId ? 'تعديل الكوبون' : 'كوبون جديد';

  if (!form) {
    return (
      <Layout storeId={storeId}>
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const edit = (next: Partial<Form>) => {
    setForm({ ...form, ...next });
    setErrors({});
  };
  // The city alone: "القاهرة" of "القاهرة (مصر)".
  const zone = (timezones.find((t) => t.id === store.timezone)?.label ?? store.timezone).split(' (')[0];
  const lines = summaryLines(form, store.currency, store.timezone);
  const unit = symbol[store.currency] ?? store.currency;

  async function save() {
    if (!form) return;
    const { input, errors: bad } = toInput(form, store.timezone);
    if (!input) {
      setErrors(bad);
      setError('لم يُحفظ: راجع الحقول المعلّمة.');
      return;
    }
    setBusy(true);
    setError('');
    const res = couponId
      ? await api.PUT('/v1/stores/{store_id}/coupons/{coupon_id}', { params: { path: { store_id: storeId, coupon_id: couponId } }, body: input })
      : await api.POST('/v1/stores/{store_id}/coupons', { params: { path: { store_id: storeId } }, body: input });
    setBusy(false);
    if (res.error) {
      const fields = serverErrors(res.error);
      setErrors(fields);
      setError(Object.keys(fields).length > 0 ? 'لم يُحفظ: راجع الحقول المعلّمة.' : problemMessage(res.error));
      return;
    }
    void navigate(list);
  }

  async function remove() {
    if (!couponId || !globalThis.confirm('حذف هذا الكوبون نهائيًا؟')) return;
    setBusy(true);
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/coupons/{coupon_id}', { params: { path: { store_id: storeId, coupon_id: couponId } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    void navigate(list);
  }

  const field = (name: string) => ({ 'aria-invalid': Boolean(errors[name]) || undefined });
  const fieldError = (name: string) => errors[name] && <span className="field-error">{errors[name]}</span>;
  return (
    <Layout storeId={storeId}>
      <nav className="crumbs" aria-label="مسار الصفحة">
        <Link to={list}>الكوبونات</Link> / <span>{coupon ? coupon.code : title}</span>
      </nav>
      <div className="page-head">
        <h1>{title}</h1>
        {/* On a phone these stay at the bottom of the screen. */}
        <div className="actions savebar">
          <Link className="button" to={list}>
            إلغاء
          </Link>
          <button type="button" className="primary" disabled={busy} onClick={() => void save()}>
            {busy ? 'جارٍ الحفظ…' : 'حفظ الكوبون'}
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      <p className="summary-line phone-only" aria-hidden="true">
        {lines.join(' · ')}
      </p>

      <div className="ed-grid">
        <div className="settings-cards">
          <section className="card editor-card" aria-labelledby="h-what">
            <h2 id="h-what">الكوبون</h2>
            <div>
              <label className="field-label" htmlFor="coupon-code">
                الكود
              </label>
              <div className="code-row">
                <input
                  id="coupon-code"
                  dir="ltr"
                  maxLength={32}
                  autoComplete="off"
                  value={form.code}
                  {...field('code')}
                  onChange={(e) => {
                    edit({ code: e.target.value });
                  }}
                  onBlur={() => {
                    edit({ code: normalizeCode(form.code) });
                  }}
                />
                <button
                  type="button"
                  className="secondary"
                  onClick={() => {
                    edit({ code: randomCode(store.slug) });
                  }}
                >
                  كود عشوائي
                </button>
              </div>
              {fieldError('code')}
              <p className="muted small">حروف إنجليزية وأرقام و - و _. يكتبه العميل بأي حالة أحرف.</p>
            </div>
            <div>
              <span className="field-label" id="kind-label">
                نوع الخصم
              </span>
              <div className="segmented seg3" role="group" aria-labelledby="kind-label">
                {kinds.map((k) => (
                  <button
                    key={k.id}
                    type="button"
                    aria-pressed={form.kind === k.id}
                    aria-label={k.label}
                    onClick={() => {
                      edit({ kind: k.id });
                    }}
                  >
                    <span className="desk-only" aria-hidden="true">
                      {k.label}
                    </span>
                    <span className="phone-only" aria-hidden="true">
                      {k.short}
                    </span>
                  </button>
                ))}
              </div>
            </div>
            {form.kind === 'percent' && (
              <div className="field-label">
                <label htmlFor="c-percent">النسبة</label>
                <span className="suffix">
                  <input
                    id="c-percent"
                    type="number"
                    inputMode="numeric"
                    min={1}
                    max={100}
                    dir="ltr"
                    value={form.percent}
                    {...field('percent')}
                    onChange={(e) => {
                      edit({ percent: e.target.value });
                    }}
                  />
                  <span>%</span>
                </span>
                {fieldError('percent')}
              </div>
            )}
            {form.kind === 'fixed' && (
              <div className="field-label">
                <label htmlFor="c-amount">المبلغ</label>
                <span className="suffix">
                  <input
                    id="c-amount"
                    inputMode="decimal"
                    dir="ltr"
                    value={form.amount}
                    {...field('amount_minor')}
                    onChange={(e) => {
                      edit({ amount: e.target.value });
                    }}
                  />
                  <span>{unit}</span>
                </span>
                {fieldError('amount_minor')}
              </div>
            )}
            <p className="muted small">الخصم على المنتجات فقط، لا على الشحن ولا رسوم الدفع عند الاستلام، ولا يتجاوز مجموع المنتجات.</p>
          </section>

          <section className="card editor-card" aria-labelledby="h-limits">
            <h2 id="h-limits">الشروط</h2>
            <div className="field-label narrow">
              <label htmlFor="c-min">أقل مجموع للمنتجات (اختياري)</label>
              <span className="suffix">
                <input
                  id="c-min"
                  inputMode="decimal"
                  dir="ltr"
                  placeholder="بلا حد"
                  value={form.min}
                  {...field('min_subtotal_minor')}
                  onChange={(e) => {
                    edit({ min: e.target.value });
                  }}
                />
                <span>{unit}</span>
              </span>
              {fieldError('min_subtotal_minor')}
            </div>
            <div className="two-cols">
              <label className="field-label">
                عدد مرات الاستخدام كله (اختياري)
                <input
                  type="number"
                  inputMode="numeric"
                  min={1}
                  dir="ltr"
                  placeholder="بلا حد"
                  value={form.maxUses}
                  {...field('max_uses')}
                  onChange={(e) => {
                    edit({ maxUses: e.target.value });
                  }}
                />
                {fieldError('max_uses')}
              </label>
              <label className="field-label">
                لكل عميل (برقم موبايله)
                <input
                  type="number"
                  inputMode="numeric"
                  min={1}
                  dir="ltr"
                  placeholder="بلا حد"
                  value={form.perCustomer}
                  {...field('max_per_customer')}
                  onChange={(e) => {
                    edit({ perCustomer: e.target.value });
                  }}
                />
                {fieldError('max_per_customer')}
              </label>
            </div>
            <div className="two-cols">
              <label className="field-label">
                يبدأ (اختياري)
                <input
                  type="datetime-local"
                  value={form.startsAt}
                  onChange={(e) => {
                    edit({ startsAt: e.target.value });
                  }}
                />
              </label>
              <label className="field-label">
                ينتهي (اختياري)
                <input
                  type="datetime-local"
                  value={form.endsAt}
                  {...field('ends_at')}
                  onChange={(e) => {
                    edit({ endsAt: e.target.value });
                  }}
                />
                {fieldError('ends_at')}
              </label>
            </div>
            <p className="muted small">بتوقيت متجرك ({zone}).</p>
          </section>
        </div>

        <div className="ed-side">
          <section className="card editor-card ed-active" aria-labelledby="h-active">
            <div className="toggle-row">
              <div>
                <h2 id="h-active">مفعّل</h2>
                <p className="muted small">أوقفه في أي وقت دون حذفه.</p>
              </div>
              <button
                type="button"
                className="toggle"
                aria-pressed={form.active}
                aria-label="مفعّل"
                onClick={() => {
                  edit({ active: !form.active });
                }}
              />
            </div>
          </section>
          <section className="card editor-card desk-only" aria-labelledby="h-sum">
            <h2 id="h-sum">الملخص</h2>
            <ul className="summary" data-testid="coupon-summary">
              {lines.map((l) => (
                <li key={l}>{l}</li>
              ))}
            </ul>
            <div className="shopper">
              <small className="muted">في صفحة إتمام الطلب، لطلب منتجاته {sampleSubtotal / 100} {unit}:</small>
              <div className="applied">
                <span>
                  كوبون{' '}
                  <span className="ccode" dir="ltr">
                    {normalizeCode(form.code) || '—'}
                  </span>
                </span>
                <span>{effectOn(form, sampleSubtotal, store.currency)}</span>
              </div>
            </div>
          </section>
          {coupon && (
            <section className="card editor-card" aria-labelledby="h-del">
              <h2 id="h-del">الاستخدام</h2>
              {coupon.uses > 0 ? (
                <p className="muted small">استُخدم في {coupon.uses} طلب، فلا يُحذف ولا يتغير كوده: أوقفه حين ينتهي العرض.</p>
              ) : (
                <>
                  <p className="muted small">لم يُستخدم بعد.</p>
                  <button type="button" className="danger" disabled={busy} onClick={() => void remove()}>
                    حذف الكوبون
                  </button>
                </>
              )}
            </section>
          )}
        </div>
      </div>
    </Layout>
  );
}
