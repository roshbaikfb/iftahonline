import { useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { Editable } from './Can';
import { currencyLabel, major, toMinor, type CollectionSettings } from './collection';
import { ConsoleLayout } from './ConsoleLayout';

// CollectionPage is how pay-as-you-sell merchants may pay their fees
// (ADR-0083, the approved mockup): a prepaid balance and the card with a
// minimum, each on or off, and the minimum per currency.
export function CollectionPage() {
  const [settings, setSettings] = useState<CollectionSettings | null>(null);
  const [prepaid, setPrepaid] = useState(false);
  const [card, setCard] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/billing/collection').then((r) => {
      if (!live) return;
      if (r.data) {
        setSettings(r.data);
        setPrepaid(r.data.prepaid_enabled);
        setCard(r.data.card_enabled);
      } else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, []);

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!settings) return;
    const form = new FormData(e.currentTarget);
    const minimums = settings.minimums.map((m) => ({
      currency: m.currency,
      minimum_minor: toMinor(field(form, `min-${m.currency}`)),
    }));
    if (minimums.some((m) => !Number.isFinite(m.minimum_minor))) {
      setError('اكتب الحد الأدنى رقمًا، بحد أقصى خانتين بعد العلامة.');
      return;
    }
    setBusy(true);
    setError('');
    setNotice('');
    const { data, error: err } = await api.PUT('/admin/v1/billing/collection', {
      body: {
        prepaid_enabled: prepaid,
        card_enabled: card,
        minimums,
        reason: field(form, 'reason'),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setSettings(data);
    setNotice('حُفظت إعدادات التحصيل.');
  }

  return (
    <ConsoleLayout>
      <Editable p="billing.manage">
        <div>
          <h1>إعدادات التحصيل</h1>
          <p className="muted">كيف يدفع تجار «ادفع حسب مبيعاتك» رسومهم.</p>
        </div>
        {error && <p role="alert">{error}</p>}
        {notice && <p role="status">{notice}</p>}

        {settings && (
          <form className="collection-form" onSubmit={(e) => void save(e)}>
            <section className="card" aria-labelledby="h-ways">
              <h2 id="h-ways">طرق الدفع</h2>
              <label className="switch-row">
                <span>
                  <strong>رصيد مسبق</strong>
                  <small className="muted">يشحن التاجر رصيدًا، وتُخصم رسوم كل طلب عند تسليمه.</small>
                </span>
                <input
                  type="checkbox"
                  role="switch"
                  checked={prepaid}
                  onChange={(e) => {
                    setPrepaid(e.target.checked);
                  }}
                />
              </label>
              <label className="switch-row">
                <span>
                  <strong>بطاقة محفوظة مع حد أدنى</strong>
                  <small className="muted">تُسحب الرسوم عندما تبلغ الحد الأدنى أو في آخر الشهر.</small>
                </span>
                <input
                  type="checkbox"
                  role="switch"
                  checked={card}
                  onChange={(e) => {
                    setCard(e.target.checked);
                  }}
                />
              </label>
              <p className="muted small">
                إذا فُعّلت الطريقتان يختار التاجر بينهما. إيقاف طريقة لا يغيّر شيئًا للتجار الذين اختاروها من قبل؛ يُطبَّق فقط على الاختيارات الجديدة.
              </p>
            </section>

            <section className="card" aria-labelledby="h-minimums">
              <h2 id="h-minimums">الحد الأدنى</h2>
              <p className="muted small">هو أقل مبلغ للشحن، وهو أيضًا المبلغ الذي تُسحب عنده الرسوم من البطاقة. ويصل إشعار «رصيد منخفض» عند نصفه.</p>
              <div className="minimums">
                {settings.minimums.map((m) => (
                  <label key={m.currency}>
                    <span className="field-label">{currencyLabel[m.currency]}</span>
                    <input
                      name={`min-${m.currency}`}
                      inputMode="decimal"
                      dir="ltr"
                      required
                      pattern="[0-9]+([.][0-9]{1,2})?"
                      defaultValue={major(m.minimum_minor)}
                    />
                  </label>
                ))}
              </div>
            </section>

            <p className="notice">مدة المهلة (7 أيام) ومدة الباقة المجانية (30 يومًا) ثابتتان، ونسبة الرسوم تُعدَّل من صفحة الباقات.</p>
            <div className="inline-form">
              <label className="grow">
                <span className="field-label">السبب (إجباري)</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <button type="submit" className="button primary" disabled={busy}>
                احفظ
              </button>
            </div>
          </form>
        )}
      </Editable>
    </ConsoleLayout>
  );
}
