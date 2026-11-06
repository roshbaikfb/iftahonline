import { useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { OTHERS, billingCurrency, byCountry, countryName, gatewayName, percent, toBps, type Gateway, type PaymentRule, type Tax } from './countries';
import { Dialog } from './Dialog';
import { useCan } from './session';

type Open =
  | { kind: 'rule'; rule: PaymentRule | null }
  | { kind: 'tax'; tax: Tax | null }
  | { kind: 'remove-rule'; country: string }
  | { kind: 'remove-tax'; country: string };

const countryRE = /^[A-Za-z]{2}$/;

// CountryRules is the console's payment rules and taxes by the
// merchant's country (ADR-0089, the approved mockup): how the platform
// bills a country's merchants and the gateways their storefronts may
// take cards with, and the tax on the platform's invoices. ZZ is every
// other country: it can be changed, never removed.
export function CountryRules() {
  const can = useCan();
  const edit = can('billing.manage');
  const [rules, setRules] = useState<PaymentRule[] | null>(null);
  const [gateways, setGateways] = useState<Gateway[]>([]);
  const [taxes, setTaxes] = useState<Tax[] | null>(null);
  const [open, setOpen] = useState<Open | null>(null);
  const [error, setError] = useState('');
  const [dialogError, setDialogError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    void Promise.all([api.GET('/admin/v1/billing/payment-rules'), api.GET('/admin/v1/billing/taxes')]).then(([r, t]) => {
      if (!live) return;
      if (r.data) {
        setRules(byCountry(r.data.items));
        setGateways(r.data.gateways.filter((g): g is Gateway => g in gatewayName));
      } else setError(problemMessage(r.error));
      if (t.data) setTaxes(byCountry(t.data.items));
      else setError(problemMessage(t.error));
    });
    return () => {
      live = false;
    };
  }, [version]);

  function show(o: Open) {
    setDialogError('');
    setNotice('');
    setOpen(o);
  }

  function done(err: unknown, message: string) {
    setBusy(false);
    if (err) {
      setDialogError(problemMessage(err));
      return;
    }
    setOpen(null);
    setNotice(message);
    setVersion((v) => v + 1);
  }

  async function saveRule(e: SubmitEvent<HTMLFormElement>, rule: PaymentRule | null) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const country = rule ? rule.country : field(form, 'country').toUpperCase();
    if (!countryRE.test(country)) {
      setDialogError('اكتب رمز البلد من حرفين، مثل SA.');
      return;
    }
    const billing = field(form, 'billing') as Gateway;
    const stores = gateways.filter((g) => form.get(`store-${g}`) === 'on');
    setBusy(true);
    const { error: err } = await api.PUT('/admin/v1/billing/payment-rules/{country}', {
      params: { path: { country } },
      body: {
        billing_gateway: billing,
        store_gateways: stores,
        reason: field(form, 'reason'),
      },
    });
    done(err, `حُفظت قاعدة ${countryName(country)}.`);
  }

  async function saveTax(e: SubmitEvent<HTMLFormElement>, tax: Tax | null) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const country = tax ? tax.country : field(form, 'country').toUpperCase();
    const bps = toBps(field(form, 'rate'));
    if (!countryRE.test(country)) {
      setDialogError('اكتب رمز البلد من حرفين، مثل SA.');
      return;
    }
    if (!Number.isFinite(bps) || bps > 5000) {
      setDialogError('اكتب النسبة رقمًا من 0 إلى 50، بحد أقصى خانتين بعد العلامة.');
      return;
    }
    setBusy(true);
    const { error: err } = await api.PUT('/admin/v1/billing/taxes/{country}', {
      params: { path: { country } },
      body: {
        rate_bps: bps,
        name_ar: field(form, 'name_ar'),
        name_en: field(form, 'name_en'),
        reason: field(form, 'reason'),
      },
    });
    done(err, `حُفظت ضريبة ${countryName(country)}.`);
  }

  async function remove(e: SubmitEvent<HTMLFormElement>, what: 'rule' | 'tax', country: string) {
    e.preventDefault();
    const reason = field(new FormData(e.currentTarget), 'reason');
    const params = { path: { country } };
    setBusy(true);
    const { error: err } =
      what === 'rule'
        ? await api.POST('/admin/v1/billing/payment-rules/{country}/remove', {
            params,
            body: { reason },
          })
        : await api.POST('/admin/v1/billing/taxes/{country}/remove', {
            params,
            body: { reason },
          });
    done(err, `${countryName(country)} يتبع الآن صف باقي البلاد.`);
  }

  const reasonField = (
    <label>
      <span className="field-label">السبب (إجباري، يُحفظ في سجل الأدمن)</span>
      <input name="reason" required minLength={3} maxLength={500} />
    </label>
  );
  const countryField = (
    <label>
      <span className="field-label">رمز البلد (ISO، حرفان)</span>
      <input name="country" required pattern="[A-Za-z]{2}" maxLength={2} dir="ltr" placeholder="SA" />
    </label>
  );

  return (
    <>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <section className="card table-card country-card" aria-labelledby="h-rules">
        <div className="card-head">
          <div>
            <h2 id="h-rules">قواعد الدفع</h2>
            <p className="muted small">عبر أي بوابة تحصّل المنصة من التاجر، وعبر أي بوابة يدفع زبائن متجره بالبطاقة. الدفع عند الاستلام متاح دائمًا.</p>
          </div>
          {edit && (
            <button
              type="button"
              className="button secondary"
              onClick={() => {
                show({ kind: 'rule', rule: null });
              }}
            >
              أضف بلدًا
            </button>
          )}
        </div>
        <div className="scroll">
          <table className="country-table">
            <thead>
              <tr>
                <th scope="col">البلد</th>
                <th scope="col">تحصيل المنصة</th>
                <th scope="col">بطاقات المتاجر</th>
                {edit && (
                  <th scope="col">
                    <span className="sr">إجراءات</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {rules?.map((r) => (
                <tr key={r.country} className={r.country === OTHERS ? 'others-row' : undefined}>
                  <td>
                    <strong>{countryName(r.country)}</strong>
                    {r.country === OTHERS && <div className="muted small">أي بلد ليست له قاعدة</div>}
                  </td>
                  <td>
                    {gatewayName[r.billing_gateway]} · {billingCurrency(r.country)}
                  </td>
                  <td>
                    {r.store_gateways.length === 0 ? (
                      <span className="muted">الاستلام فقط</span>
                    ) : (
                      r.store_gateways.map((g) => (
                        <span key={g} className={`gateway-chip gateway-${g}`}>
                          {gatewayName[g]}
                        </span>
                      ))
                    )}
                  </td>
                  {edit && (
                    <td className="row-actions">
                      <button
                        type="button"
                        className="link-button"
                        onClick={() => {
                          show({ kind: 'rule', rule: r });
                        }}
                      >
                        تعديل <span className="sr">قاعدة {countryName(r.country)}</span>
                      </button>
                      {r.country !== OTHERS && (
                        <button
                          type="button"
                          className="link-button danger"
                          onClick={() => {
                            show({ kind: 'remove-rule', country: r.country });
                          }}
                        >
                          حذف <span className="sr">قاعدة {countryName(r.country)}</span>
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="card table-card country-card" aria-labelledby="h-taxes">
        <div className="card-head">
          <div>
            <h2 id="h-taxes">الضرائب على فواتير المنصة</h2>
            <p className="muted small">تُحسب على الفواتير القادمة فقط، حسب بلد حساب الفوترة.</p>
          </div>
          {edit && (
            <button
              type="button"
              className="button secondary"
              onClick={() => {
                show({ kind: 'tax', tax: null });
              }}
            >
              أضف بلدًا
            </button>
          )}
        </div>
        <div className="scroll">
          <table className="country-table">
            <thead>
              <tr>
                <th scope="col">البلد</th>
                <th scope="col">اسم الضريبة</th>
                <th scope="col">النسبة</th>
                {edit && (
                  <th scope="col">
                    <span className="sr">إجراءات</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {taxes?.map((t) => (
                <tr key={t.country} className={t.country === OTHERS ? 'others-row' : undefined}>
                  <td>
                    <strong>{countryName(t.country)}</strong>
                  </td>
                  <td>
                    {t.name_ar} · <bdi>{t.name_en}</bdi>
                  </td>
                  <td>{percent(t.rate_bps)}%</td>
                  {edit && (
                    <td className="row-actions">
                      <button
                        type="button"
                        className="link-button"
                        onClick={() => {
                          show({ kind: 'tax', tax: t });
                        }}
                      >
                        تعديل <span className="sr">ضريبة {countryName(t.country)}</span>
                      </button>
                      {t.country !== OTHERS && (
                        <button
                          type="button"
                          className="link-button danger"
                          onClick={() => {
                            show({ kind: 'remove-tax', country: t.country });
                          }}
                        >
                          حذف <span className="sr">ضريبة {countryName(t.country)}</span>
                        </button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {open?.kind === 'rule' && (
        <Dialog
          title={open.rule ? `تعديل قاعدة ${countryName(open.rule.country)}` : 'قاعدة بلد جديد'}
          error={dialogError}
          onClose={() => {
            setOpen(null);
          }}
          onSubmit={(e) => void saveRule(e, open.rule)}
        >
          {!open.rule && countryField}
          <fieldset>
            <legend>تحصّل المنصة من التاجر عبر</legend>
            {gateways.map((g) => (
              <label key={g} className="radio">
                <input type="radio" name="billing" value={g} required defaultChecked={(open.rule?.billing_gateway ?? 'stripe') === g} />
                {gatewayName[g]}
              </label>
            ))}
          </fieldset>
          <fieldset>
            <legend>يدفع زبائن المتاجر بالبطاقة عبر</legend>
            {gateways.map((g) => (
              <label key={g} className="check">
                <input type="checkbox" name={`store-${g}`} defaultChecked={open.rule?.store_gateways.includes(g) ?? false} />
                {gatewayName[g]}
              </label>
            ))}
            <p className="muted small">يسري على الربط الجديد فقط؛ البوابة المربوطة من قبل تبقى تعمل.</p>
          </fieldset>
          {reasonField}
          <div className="actions">
            <button
              type="button"
              className="button secondary"
              onClick={() => {
                setOpen(null);
              }}
            >
              إلغاء
            </button>
            <button type="submit" className="button primary" disabled={busy}>
              احفظ
            </button>
          </div>
        </Dialog>
      )}

      {open?.kind === 'tax' && (
        <Dialog
          title={open.tax ? `ضريبة ${countryName(open.tax.country)}` : 'ضريبة بلد جديد'}
          error={dialogError}
          onClose={() => {
            setOpen(null);
          }}
          onSubmit={(e) => void saveTax(e, open.tax)}
        >
          {!open.tax && countryField}
          <label>
            <span className="field-label">النسبة %</span>
            <input
              name="rate"
              required
              inputMode="decimal"
              dir="ltr"
              pattern="[0-9]+([.][0-9]{1,2})?"
              defaultValue={open.tax ? percent(open.tax.rate_bps) : ''}
            />
          </label>
          <label>
            <span className="field-label">الاسم بالعربية</span>
            <input name="name_ar" required maxLength={60} defaultValue={open.tax?.name_ar ?? 'ضريبة القيمة المضافة'} />
          </label>
          <label>
            <span className="field-label">الاسم بالإنجليزية</span>
            <input name="name_en" required maxLength={60} dir="ltr" defaultValue={open.tax?.name_en ?? 'VAT'} />
          </label>
          {reasonField}
          <div className="actions">
            <button
              type="button"
              className="button secondary"
              onClick={() => {
                setOpen(null);
              }}
            >
              إلغاء
            </button>
            <button type="submit" className="button primary" disabled={busy}>
              احفظ
            </button>
          </div>
        </Dialog>
      )}

      {(open?.kind === 'remove-rule' || open?.kind === 'remove-tax') && (
        <Dialog
          title={`حذف ${open.kind === 'remove-rule' ? 'قاعدة' : 'ضريبة'} ${countryName(open.country)}`}
          intro={`سيتبع ${countryName(open.country)} صف باقي البلاد.`}
          error={dialogError}
          onClose={() => {
            setOpen(null);
          }}
          onSubmit={(e) => void remove(e, open.kind === 'remove-rule' ? 'rule' : 'tax', open.country)}
        >
          {reasonField}
          <div className="actions">
            <button
              type="button"
              className="button secondary"
              onClick={() => {
                setOpen(null);
              }}
            >
              إلغاء
            </button>
            <button type="submit" className="button danger" disabled={busy}>
              احذف
            </button>
          </div>
        </Dialog>
      )}
    </>
  );
}
