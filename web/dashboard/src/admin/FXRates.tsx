import { useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { rate, toMicros, type FXRate } from './countries';
import { Dialog } from './Dialog';
import { useCan } from './session';

type Currency = Exclude<FXRate['currency'], 'USD'>;

// The store currencies a rate converts to the dollar, in the console's
// order; the dollar itself has none.
const currencies: { code: Currency; name: string; unit: string }[] = [
  { code: 'EGP', name: 'الجنيه المصري', unit: 'جنيه' },
  { code: 'SAR', name: 'الريال السعودي', unit: 'ريال' },
  { code: 'AED', name: 'الدرهم الإماراتي', unit: 'درهم' },
];

// FXRates is each store currency's rate to the dollar (ADR-0090, the
// approved mockup): a pay-as-you-sell fee of an order in another
// currency than its account's converts at it, and keeps the rate it
// used. A currency without a rate is never converted at par: its fees
// wait.
export function FXRates() {
  const can = useCan();
  const edit = can('billing.manage');
  const [rates, setRates] = useState<Partial<Record<Currency, number>> | null>(null);
  const [open, setOpen] = useState<Currency | null>(null);
  const [error, setError] = useState('');
  const [dialogError, setDialogError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/billing/fx-rates').then(({ data, error: err }) => {
      if (!live) return;
      if (!data) {
        setError(problemMessage(err));
        return;
      }
      const m: Partial<Record<Currency, number>> = {};
      for (const r of data.items) if (r.currency !== 'USD') m[r.currency] = r.per_usd_micros;
      setRates(m);
    });
    return () => {
      live = false;
    };
  }, [version]);

  async function save(e: SubmitEvent<HTMLFormElement>, currency: Currency) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const micros = toMicros(field(form, 'rate'));
    if (!Number.isFinite(micros) || micros > 1_000_000_000_000) {
      setDialogError('اكتب السعر رقمًا أكبر من صفر، بحد أقصى ست خانات بعد العلامة.');
      return;
    }
    setBusy(true);
    const { error: err } = await api.PUT('/admin/v1/billing/fx-rates/{currency}', {
      params: { path: { currency } },
      body: { per_usd_micros: micros, reason: field(form, 'reason') },
    });
    setBusy(false);
    if (err) {
      setDialogError(problemMessage(err));
      return;
    }
    setOpen(null);
    setNotice('حُفظ سعر الصرف.');
    setVersion((v) => v + 1);
  }

  const missing = rates ? currencies.filter((c) => rates[c.code] === undefined) : [];
  const editing = currencies.find((c) => c.code === open);

  return (
    <>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      {missing.map((c) => (
        <p key={c.code} className="warn-note" role="note">
          سعر {c.name} غير محدد. أي رسم على طلب بال{c.unit} لحساب يدفع بالدولار ينتظر حتى تحدده، ولا يُحسب بسعر 1 = 1.
        </p>
      ))}
      <section className="card table-card country-card" aria-labelledby="h-fx">
        <div className="card-head">
          <div>
            <h2 id="h-fx">كم وحدة من كل عملة تساوي دولارًا واحدًا</h2>
            <p className="muted small">يسري على رسوم «ادفع حسب مبيعاتك» القادمة فقط، ويحتفظ كل رسم بالسعر الذي حُسب به.</p>
          </div>
        </div>
        <div className="scroll">
          <table className="country-table">
            <thead>
              <tr>
                <th scope="col">العملة</th>
                <th scope="col">السعر مقابل 1 دولار</th>
                {edit && (
                  <th scope="col">
                    <span className="sr">إجراءات</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {rates &&
                currencies.map((c) => {
                  const v = rates[c.code];
                  return (
                    <tr key={c.code}>
                      <td>
                        <strong>{c.name}</strong> <bdi className="muted">{c.code}</bdi>
                      </td>
                      <td>{v === undefined ? <span className="unset-chip">غير محدد</span> : <bdi className="ltr">{rate(v)}</bdi>}</td>
                      {edit && (
                        <td className="row-actions">
                          <button
                            type="button"
                            className="link-button"
                            onClick={() => {
                              setDialogError('');
                              setNotice('');
                              setOpen(c.code);
                            }}
                          >
                            {v === undefined ? 'حدد السعر' : 'تعديل'} <span className="sr">سعر {c.name}</span>
                          </button>
                        </td>
                      )}
                    </tr>
                  );
                })}
            </tbody>
          </table>
        </div>
      </section>

      {editing && rates && (
        <Dialog
          title={`سعر ${editing.name}`}
          error={dialogError}
          onClose={() => {
            setOpen(null);
          }}
          onSubmit={(e) => void save(e, editing.code)}
        >
          <label>
            <span className="field-label">1 دولار = (بال{editing.unit})</span>
            <input
              name="rate"
              required
              inputMode="decimal"
              dir="ltr"
              pattern="[0-9]+([.][0-9]{1,6})?"
              defaultValue={rates[editing.code] === undefined ? '' : rate(rates[editing.code] ?? 0)}
            />
            <small className="muted">حتى ست خانات بعد العلامة.</small>
          </label>
          <label>
            <span className="field-label">السبب (إجباري، يُحفظ مع السعر السابق)</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
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
              احفظ السعر
            </button>
          </div>
        </Dialog>
      )}
    </>
  );
}
