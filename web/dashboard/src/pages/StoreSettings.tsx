import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { problemMessage } from '../lib/problem';
import { fieldErrors as toFieldErrors, type FieldError } from '../lib/sections';
import { currencies, timezones } from '../lib/settings';
import { storefrontURL } from '../lib/storefront';

type Store = components['schemas']['Store'];
type Settings = components['schemas']['StoreSettings'];
type PaymentSettings = components['schemas']['PaymentSettings'];

// StoreSettings is the store's name, language and clock, and the payment
// method checkout starts on (U6d). Currency and address are fixed here.
export function StoreSettings() {
  const { storeId = '' } = useParams();
  const [store, setStore] = useState<Store | null>(null);
  const [form, setForm] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState('');
  const [pay, setPay] = useState<PaymentSettings | null>(null);
  const [payNote, setPayNote] = useState('');

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void api.GET('/v1/stores/{store_id}', path).then(({ data, error: err }) => {
      if (!live) return;
      if (!data) {
        setError(problemMessage(err));
        return;
      }
      setStore(data);
      setForm({ name: data.name, locale: data.locale, timezone: data.timezone as Settings['timezone'] });
    });
    // Only the owner chooses how shoppers pay; others do not see it.
    void api.GET('/v1/stores/{store_id}/payments/settings', path).then(({ data }) => {
      if (live && data) setPay(data);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  const dirty = Boolean(store && form && (form.name !== store.name || form.locale !== store.locale || form.timezone !== store.timezone));

  async function save() {
    if (!form) return;
    setBusy(true);
    const { data, error: err } = await api.PATCH('/v1/stores/{store_id}', { params: { path: { store_id: storeId } }, body: { ...form, name: form.name.trim() } });
    setBusy(false);
    if (err) {
      const fields = (err as { errors?: FieldError[] }).errors ?? [];
      setErrors(toFieldErrors(fields));
      setError(fields.length ? 'لم تُحفظ: راجع الحقول المعلّمة.' : problemMessage(err));
      return;
    }
    setStore(data);
    setForm({ name: data.name, locale: data.locale, timezone: data.timezone as Settings['timezone'] });
    setErrors({});
    setError('');
    setNotice('حُفظت الإعدادات.');
  }

  async function choosePayment(method: PaymentSettings['default_method']) {
    setPayNote('');
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/payments/settings', { params: { path: { store_id: storeId } }, body: { default_method: method } });
    if (err) {
      setPayNote(problemMessage(err));
      return;
    }
    setPay(data);
    setPayNote('حُفظ.');
  }

  if (!store || !form) {
    return (
      <Layout storeId={storeId}>
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const edit = (next: Partial<Settings>) => {
    setForm({ ...form, ...next });
    setNotice('');
  };

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>إعدادات المتجر</h1>
          <p className="muted">اسم متجرك ولغته وتوقيته، وطريقة الدفع التي يبدأ بها العميل.</p>
        </div>
        <div className="actions">
          <button type="button" className="primary" disabled={busy || !dirty} onClick={() => void save()}>
            {busy ? 'جارٍ الحفظ…' : 'حفظ التغييرات'}
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <div className="settings-cards">
        <section className="card editor-card" aria-labelledby="h-store">
          <h2 id="h-store">بيانات المتجر</h2>
          <label className="field-label narrow">
            اسم المتجر
            <input
              value={form.name}
              maxLength={120}
              aria-invalid={Boolean(errors['name']) || undefined}
              onChange={(e) => {
                edit({ name: e.target.value });
              }}
            />
            {errors['name'] && <span className="field-error">{errors['name']}</span>}
          </label>
          <p className="muted small">يظهر في رأس صفحات المتجر إن لم يكن لك شعار، وفي رسائل العملاء.</p>
          <div>
            <span className="field-label" id="locale-label">
              لغة المتجر
            </span>
            <div className="segmented narrow-seg" role="group" aria-labelledby="locale-label">
              {(['ar', 'en'] as const).map((l) => (
                <button
                  key={l}
                  type="button"
                  aria-pressed={form.locale === l}
                  onClick={() => {
                    edit({ locale: l });
                  }}
                >
                  {l === 'ar' ? 'العربية' : 'English'}
                </button>
              ))}
            </div>
            <p className="muted small">اللغة التي تُفتح بها صفحات المتجر للعملاء، ويمكنهم التبديل.</p>
          </div>
          <label className="field-label narrow">
            المنطقة الزمنية
            <select
              value={form.timezone}
              onChange={(e) => {
                edit({ timezone: e.target.value as Settings['timezone'] });
              }}
            >
              {timezones.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.label}
                </option>
              ))}
            </select>
          </label>
          <p className="muted small">تُحسب بها أيام التقارير ومواعيد العروض.</p>
        </section>

        <section className="card editor-card" aria-labelledby="h-fixed">
          <h2 id="h-fixed">ثابتة</h2>
          <dl className="facts">
            <dt>رابط المتجر</dt>
            <dd>
              <a href={storefrontURL(store.slug)} target="_blank" rel="noreferrer" dir="ltr">
                {storefrontURL(store.slug).replace(/^https?:\/\//, '')}
              </a>
            </dd>
            <dt>العملة</dt>
            <dd>{currencies[store.currency] ?? store.currency}</dd>
          </dl>
          <p className="muted small">
            العملة ورابط المنصة لا يتغيران. لتستخدم دومينك الخاص: <Link to={`/stores/${storeId}/domains`}>الدومينات</Link>.
          </p>
        </section>

        {pay && (
          <section className="card editor-card" aria-labelledby="h-pay">
            <h2 id="h-pay">طريقة الدفع الافتراضية</h2>
            <p className="muted">الطريقة المختارة مسبقًا في صفحة إتمام الطلب. يستطيع العميل دائمًا تغييرها.</p>
            <div className="choices" role="radiogroup" aria-labelledby="h-pay">
              <label className="choice">
                <input
                  type="radio"
                  name="default-pay"
                  checked={pay.default_method === 'cod'}
                  onChange={() => void choosePayment('cod')}
                />
                <span>
                  <strong>الدفع عند الاستلام</strong>
                  <span className="muted small">الأنسب لأغلب العملاء في مصر والخليج.</span>
                </span>
              </label>
              <label className={`choice${pay.card_enabled ? '' : ' off'}`}>
                <input
                  type="radio"
                  name="default-pay"
                  disabled={!pay.card_enabled}
                  checked={pay.default_method === 'card'}
                  onChange={() => void choosePayment('card')}
                />
                <span>
                  <strong>الدفع بالبطاقة</strong>
                  <span className="muted small">
                    {pay.card_enabled ? (
                      'يبدأ العميل بالبطاقة، ويمكنه اختيار الدفع عند الاستلام.'
                    ) : (
                      <>
                        اربط بوابة دفع أولًا من <Link to={`/stores/${storeId}/gateways`}>الدفع بالبطاقة</Link>.
                      </>
                    )}
                  </span>
                </span>
              </label>
            </div>
            {payNote && <p role="status" className="muted small">{payNote}</p>}
          </section>
        )}
      </div>
    </Layout>
  );
}
