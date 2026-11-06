import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { billingChanged, collectionLabel, dateAr, entryText, leave, signedMoney, topUpAmounts, type BillingBalance, type Collection, payGateway } from '../lib/billing';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { useRole } from '../lib/settings';

const levelText: Record<BillingBalance['level'], string> = { ok: 'كافٍ', low: 'منخفض', debt: 'نفد' };

// Balance is the prepaid balance pay-as-you-sell fees are taken from
// (ADR-0084, ADR-0085; the approved mockup 4): what is left, a top-up
// on Paymob's page from the minimum, the way the fees are paid, and the
// entries.
export function Balance() {
  const { storeId = '' } = useParams();
  const role = useRole(storeId);
  const [balance, setBalance] = useState<BillingBalance | null>(null);
  const [amount, setAmount] = useState(0);
  const [other, setOther] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (role !== 'owner') return undefined;
    let live = true;
    const read = () => {
      void api.GET('/v1/stores/{store_id}/billing/balance', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
        if (!live) return;
        if (data) {
          setBalance(data);
          setAmount((a) => a || data.minimum_minor);
        } else setError(problemMessage(err));
      });
    };
    read();
    window.addEventListener(billingChanged, read);
    return () => {
      live = false;
      window.removeEventListener(billingChanged, read);
    };
  }, [storeId, role]);

  if (role !== 'owner') {
    return (
      <Layout storeId={storeId}>
        <p className="notice">الفوترة لمالك المتجر فقط.</p>
      </Layout>
    );
  }

  async function topUp(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!balance) return;
    const typed = other.trim();
    const chosen = typed ? Math.round(Number(typed) * 100) : amount;
    if (!Number.isFinite(chosen) || chosen < balance.minimum_minor) {
      setError(`أقل مبلغ للشحن ${money(balance.minimum_minor, balance.currency)}.`);
      return;
    }
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/billing/balance/topups', {
      params: { path: { store_id: storeId } },
      body: { amount_minor: chosen },
    });
    setBusy(false);
    if (data) leave.goTo(data.checkout_url);
    else setError(problemMessage(err));
  }

  async function choose(way: Collection) {
    setBusy(true);
    setError('');
    setNotice('');
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/billing/collection', {
      params: { path: { store_id: storeId } },
      body: { collection: way },
    });
    setBusy(false);
    if (!data) {
      setError(problemMessage(err));
      return;
    }
    setNotice(`صارت الرسوم تُدفع من ${collectionLabel[way]}.`);
    window.dispatchEvent(new Event(billingChanged));
  }

  const half = balance ? Math.floor(balance.minimum_minor / 2) : 0;
  const ways = balance ? [...new Set([...(balance.collection ? [balance.collection] : []), ...balance.offered])] : [];

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>الرصيد</h1>
          <p className="muted">تُخصم منه رسوم الطلبات المسلَّمة، وضريبتها مع الفاتورة الشهرية.</p>
        </div>
        <Link className="button" to={`/stores/${storeId}/billing`}>
          الفوترة والفواتير
        </Link>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}

      {balance && (
        <>
          {balance.level === 'low' && (
            <div className="bill-banner trial" role="status">
              <div>
                <strong>رصيدك منخفض</strong>
                <p>
                  رصيدك {money(balance.balance_minor, balance.currency)}، أقل من {money(half, balance.currency)}. اشحن قبل أن ينفد حتى لا يتوقف شيء في متجرك.
                </p>
              </div>
            </div>
          )}

          <div className="bill-grid">
            <section className="card" aria-labelledby="h-left">
              <div className="card-head bare">
                <h2 id="h-left">رصيدك الآن</h2>
                <span className={`status balance-${balance.level}`}>{levelText[balance.level]}</span>
              </div>
              <p className={`balance-now ${balance.balance_minor < 0 ? 'neg' : ''}`}>{money(balance.balance_minor, balance.currency)}</p>
              <p className="muted small">نخبرك بالبريد وهنا عندما يقل عن {money(half, balance.currency)}.</p>
              {balance.balance_minor <= 0 && (
                <p className="notice">
                  إذا نزل الرصيد تحت الصفر بسبب طلبات سُلّمت لا نوقف شيئًا في الحال: أمامك 7 أيام لتشحن، ثم يصير المتجر مجانيًا. يُخصم المستحق من أول شحنة.
                </p>
              )}
            </section>

            <form className="card" aria-labelledby="h-top" onSubmit={(e) => void topUp(e)}>
              <h2 id="h-top">اشحن رصيدك</h2>
              <div className="amounts" role="group" aria-label="المبلغ">
                {topUpAmounts(balance.minimum_minor).map((a) => (
                  <button
                    key={a}
                    type="button"
                    className={`amount ${amount === a && !other ? 'on' : ''}`}
                    aria-pressed={amount === a && !other}
                    onClick={() => {
                      setAmount(a);
                      setOther('');
                    }}
                  >
                    {money(a, balance.currency)}
                  </button>
                ))}
              </div>
              <label>
                <span className="field-label">أو مبلغ آخر ({money(balance.minimum_minor, balance.currency)} على الأقل)</span>
                <input
                  value={other}
                  inputMode="decimal"
                  dir="ltr"
                  pattern="[0-9]+([.][0-9]{1,2})?"
                  onChange={(e) => {
                    setOther(e.target.value);
                  }}
                />
              </label>
              <button type="submit" className="button success" disabled={busy}>
                ادفع {money(other.trim() ? Math.round(Number(other) * 100) || 0 : amount, balance.currency)} بالبطاقة
              </button>
              <p className="muted small">
                الدفع على صفحة {payGateway(balance.billing_gateway)}، وبيانات البطاقة لا تمر على خوادمنا. تفضّل التحويل البنكي؟ تواصل معنا وسنضيف الرصيد بعد التأكد.
              </p>
            </form>
          </div>

          {ways.length > 0 && (
            <section className="card" aria-labelledby="h-way">
              <h2 id="h-way">طريقة دفع الرسوم</h2>
              <div className="actions-row">
                {ways.map((w) => (
                  <button
                    key={w}
                    type="button"
                    className={`button ${balance.collection === w ? 'primary' : ''}`}
                    aria-pressed={balance.collection === w}
                    disabled={busy || balance.collection === w}
                    onClick={() => void choose(w)}
                  >
                    {collectionLabel[w]}
                  </button>
                ))}
              </div>
              <p className="muted small">
                البطاقة: نسحب الرسوم عندما تبلغ {money(balance.minimum_minor, balance.currency)} أو في آخر الشهر. الانتقال من الرصيد إلى البطاقة ينتظر الفاتورة
                الشهرية للرسوم المخصومة منه.
              </p>
            </section>
          )}

          <section className="table-card" aria-labelledby="h-entries">
            <div className="card-head">
              <h2 id="h-entries">الحركات</h2>
            </div>
            {balance.entries.length === 0 ? (
              <p className="empty flat">لا حركات بعد.</p>
            ) : (
              <div className="scroll">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">التاريخ</th>
                      <th scope="col">الحركة</th>
                      <th scope="col">المبلغ</th>
                      <th scope="col">الرصيد بعدها</th>
                    </tr>
                  </thead>
                  <tbody>
                    {balance.entries.map((e) => (
                      <tr key={e.id}>
                        <td className="muted">{dateAr(e.created_at)}</td>
                        <td>{entryText(e)}</td>
                        <td className={`ltr ${e.amount_minor > 0 ? 'plus' : ''}`}>{signedMoney(e.amount_minor, balance.currency)}</td>
                        <td className="ltr">{money(e.balance_after_minor, balance.currency)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      )}
    </Layout>
  );
}
