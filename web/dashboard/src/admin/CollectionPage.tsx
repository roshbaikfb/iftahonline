import { useEffect, useState, type SubmitEvent } from 'react';
import { useSearchParams } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { Editable } from './Can';
import { currencyLabel, major, toMinor, type CollectionSettings } from './collection';
import { ConsoleLayout } from './ConsoleLayout';
import { CountryRules } from './CountryRules';
import { FXRates } from './FXRates';

const tabs = [
  { id: 'ways', label: 'طرق التحصيل' },
  { id: 'countries', label: 'الدفع والضرائب حسب البلد' },
  { id: 'fx', label: 'أسعار الصرف' },
] as const;
type Tab = (typeof tabs)[number]['id'];

// CollectionPage is the console's collection settings, in three tabs
// (the approved mockups): how pay-as-you-sell merchants may pay their
// fees (ADR-0083: a prepaid balance and the card with a minimum, each on
// or off, and the minimum per currency); the payment rules and taxes by
// the merchant's country (ADR-0089); the exchange rates (ADR-0090).
export function CollectionPage() {
  const [params, setParams] = useSearchParams();
  const tab: Tab = tabs.find((t) => t.id === params.get('tab'))?.id ?? 'ways';
  return (
    <ConsoleLayout>
      <div>
        <h1>إعدادات التحصيل</h1>
        <p className="muted">طرق تحصيل الرسوم، وقواعد الدفع والضرائب حسب بلد التاجر، وأسعار الصرف.</p>
      </div>
      <div className="tabs collection-tabs" role="tablist" aria-label="الأقسام">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            onClick={() => {
              setParams(t.id === 'ways' ? {} : { tab: t.id });
            }}
          >
            {t.label}
          </button>
        ))}
      </div>
      {tab === 'ways' && <Ways />}
      {tab === 'countries' && (
        <Editable p="billing.manage">
          <CountryRules />
        </Editable>
      )}
      {tab === 'fx' && (
        <Editable p="billing.manage">
          <FXRates />
        </Editable>
      )}
    </ConsoleLayout>
  );
}

// Ways is how pay-as-you-sell merchants may pay their fees.
function Ways() {
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
    <Editable p="billing.manage">
      <p className="muted">كيف يدفع تجار «ادفع حسب مبيعاتك» رسومهم.</p>
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
  );
}
