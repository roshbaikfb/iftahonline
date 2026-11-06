import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useNavigate } from 'react-router';

import { api } from '../api/client';
import { billingChanged, collectionLabel, dateAr, feeOf, lateText, leave, monthAr, rateText, supportLink, type Billing, type Collection, payGateway } from '../lib/billing';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { useRole } from '../lib/settings';

// BillingBanner is the unpaid store's banner on every screen, for its
// owner (the approved mockups 1, 2 and 5): pay the invoice, or move to
// «ادفع حسب مبيعاتك» at once (ADR-0087); an agency's store is told to
// talk to the platform's team; a balance that ran out is told to top up.
export function BillingBanner({ storeId }: { storeId: string }) {
  const role = useRole(storeId);
  const [billing, setBilling] = useState<Billing | null>(null);
  const [dialog, setDialog] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (role !== 'owner') return undefined;
    let live = true;
    const read = () => {
      void api.GET('/v1/stores/{store_id}/billing', { params: { path: { store_id: storeId } } }).then(({ data }) => {
        if (live && data) setBilling(data);
      });
    };
    read();
    window.addEventListener(billingChanged, read);
    return () => {
      live = false;
      window.removeEventListener(billingChanged, read);
    };
  }, [storeId, role]);

  if (!billing || !['past_due', 'suspended', 'cancelled'].includes(billing.status)) return null;
  const invoice = billing.open_invoices[0];
  const free = billing.status === 'suspended' && !billing.held;

  async function pay(id: string) {
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/billing/invoices/{invoice_id}/pay', {
      params: { path: { store_id: storeId, invoice_id: id } },
    });
    setBusy(false);
    if (data) leave.goTo(data.checkout_url);
    else setError(problemMessage(err));
  }

  let title: string;
  let text: string;
  if (!invoice) {
    // Pay as you sell from a balance that ran out (ADR-0084).
    title = 'نفد رصيدك';
    text = `عليك رسوم طلبات سُلّمت. اشحن رصيدك${billing.grace_until ? ` قبل ${dateAr(billing.grace_until)}` : ''}، وإلا ينتقل متجرك إلى الباقة المجانية. يُخصم المستحق من أول شحنة.`;
  } else if (free) {
    title = `متجرك الآن على الباقة المجانية حتى تُدفع فاتورة ${monthAr(invoice.period)}`;
    text = lateText(billing);
  } else {
    title = `لم ننجح في تحصيل فاتورة ${monthAr(invoice.period)}`;
    text = lateText(billing);
  }

  return (
    <div className={`bill-banner global ${free ? 'free' : 'late'}`} role="alert">
      <div>
        <strong>{title}</strong>
        <p>{text}</p>
        {billing.agency && <p>لتحويل متاجر الحساب إلى «ادفع حسب مبيعاتك» تواصل مع فريق المنصة؛ يرتّبه معك لكل المتاجر أو بعضها.</p>}
        {error && <p className="small">{error}</p>}
      </div>
      <div className="actions">
        {invoice ? (
          <button type="button" className="button success" disabled={busy} onClick={() => void pay(invoice.id)}>
            ادفع الآن {money(invoice.total_minor, invoice.currency)}
          </button>
        ) : (
          <Link className="button success" to={`/stores/${storeId}/billing/balance`}>
            اشحن الرصيد
          </Link>
        )}
        {billing.agency && billing.support_whatsapp && (
          <a className="button" href={supportLink(billing.support_whatsapp, invoice?.number)} target="_blank" rel="noreferrer">
            تواصل مع فريق المنصة
          </a>
        )}
        {billing.switch_offer && (
          <button
            type="button"
            className="button"
            onClick={() => {
              setDialog(true);
            }}
          >
            حوّل إلى «ادفع حسب مبيعاتك»
          </button>
        )}
      </div>
      {dialog && billing.switch_offer && (
        <SwitchDialog
          storeId={storeId}
          billing={billing}
          onDone={(b) => {
            setBilling(b);
            setDialog(false);
          }}
          onClose={() => {
            setDialog(false);
          }}
        />
      )}
    </div>
  );
}

// SwitchDialog moves the store to «ادفع حسب مبيعاتك» (the approved
// mockup 3): the fee with an example, the invoice voided, and how the
// fees are paid. The balance then asks for a top-up.
function SwitchDialog({ storeId, billing, onDone, onClose }: { storeId: string; billing: Billing; onDone: (b: Billing) => void; onClose: () => void }) {
  const navigate = useNavigate();
  const offer = billing.switch_offer;
  const ways = (offer?.offered ?? []).filter((w) => w !== 'card' || Boolean(billing.card));
  const [way, setWay] = useState<Collection>(ways[0] ?? 'balance');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  if (!offer) return null;
  const invoice = billing.open_invoices[0];
  const currency = invoice?.currency ?? 'EGP';
  const example = 85000;

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/billing/switch-to-pays', {
      params: { path: { store_id: storeId } },
      body: { collection: way },
    });
    setBusy(false);
    if (!data) {
      setError(problemMessage(err));
      return;
    }
    onDone(data);
    window.dispatchEvent(new Event(billingChanged));
    if (way === 'balance') void navigate(`/stores/${storeId}/billing/balance`);
  }

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form className="modal switch-dialog" role="dialog" aria-labelledby="switch-title" onSubmit={(e) => void submit(e)}>
        <h2 id="switch-title">التحويل إلى «ادفع حسب مبيعاتك»</h2>
        <dl className="facts">
          <dt>الاشتراك الشهري</dt>
          <dd>لا يوجد</dd>
          <dt>الرسوم</dt>
          <dd>{rateText(offer.rate_bps)} من كل طلب يُسلَّم</dd>
          <dt>مثال: طلب بـ{money(example, currency)}</dt>
          <dd>{money(feeOf(example, offer.rate_bps), currency)}</dd>
          {invoice && (
            <>
              <dt>
                فاتورة {monthAr(invoice.period)} ({money(invoice.total_minor, invoice.currency)})
              </dt>
              <dd>تُلغى</dd>
            </>
          )}
        </dl>
        <p className="muted small">
          تُضاف ضريبة القيمة المضافة على الرسوم في الفاتورة الشهرية. لا رسوم على الشحن، ولا على الطلبات الملغاة أو المرتجعة. يرجع متجرك كاملًا في الحال.
        </p>
        <fieldset className="ways">
          <legend>كيف تدفع الرسوم؟</legend>
          {offer.offered.map((w) => {
            const disabled = w === 'card' && !billing.card;
            return (
              <label key={w} className={`way ${way === w ? 'on' : ''}`}>
                <input
                  type="radio"
                  name="way"
                  value={w}
                  checked={way === w}
                  disabled={disabled}
                  onChange={() => {
                    setWay(w);
                  }}
                />
                <span>
                  <strong>{w === 'card' && billing.card ? `${collectionLabel.card} ${billing.card}` : collectionLabel[w]}</strong>
                  <small className="muted">
                    {w === 'balance'
                      ? 'تشحن رصيدًا، ونخصم رسوم كل طلب عند تسليمه.'
                      : disabled
                        ? `لا توجد بطاقة محفوظة: تُحفظ بطاقتك عند أول دفع على صفحة ${payGateway(billing.billing_gateway)}.`
                        : 'نسحب الرسوم عندما تبلغ الحد الأدنى أو في آخر الشهر، أيهما أسبق.'}
                  </small>
                </span>
              </label>
            );
          })}
        </fieldset>
        <label className="check">
          <input type="checkbox" required /> فهمت أن باقتي الحالية تنتهي الآن، ويمكنني الرجوع إليها لاحقًا من صفحة الباقات.
        </label>
        {error && <p role="alert">{error}</p>}
        <div className="actions">
          <button type="submit" className="button primary" disabled={busy || ways.length === 0}>
            {way === 'balance' ? 'حوّل واشحن الرصيد' : 'حوّل الآن'}
          </button>
          <button type="button" className="secondary" onClick={onClose}>
            رجوع
          </button>
        </div>
      </form>
    </div>
  );
}
