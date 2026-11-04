import { useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { collectionLabel, entryLabel, levelLabel, reasonLabel, signed, toMinor, type CreditReason, type StoreBalance } from './collection';
import { Dialog } from './Dialog';
import { dateTimeAr } from './lib';
import { useCan } from './session';

const reasons = Object.keys(reasonLabel) as CreditReason[];

// BalanceCard is a store's prepaid balance in the console (ADR-0083, the
// approved mockup): its level, the way it pays, its latest entries, and
// «إضافة رصيد» with a reason, a reference and a note. An agency's store
// has none.
export function BalanceCard({ storeId, storeName }: { storeId: string; storeName: string }) {
  const can = useCan();
  const [balance, setBalance] = useState<StoreBalance | null>(null);
  const [agency, setAgency] = useState(false);
  const [open, setOpen] = useState(false);
  const [all, setAll] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void api
      .GET('/admin/v1/stores/{store_id}/balance', {
        params: { path: { store_id: storeId } },
      })
      .then((r) => {
        if (!live) return;
        if (r.data) setBalance(r.data);
        else if ((r.error as { code?: string } | undefined)?.code === 'agency_managed') setAgency(true);
        else setError(problemMessage(r.error));
      });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function credit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const amount = toMinor(field(form, 'amount'));
    if (!Number.isFinite(amount) || amount <= 0) {
      setError('اكتب المبلغ رقمًا أكبر من صفر، بحد أقصى خانتين بعد العلامة.');
      return;
    }
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/admin/v1/stores/{store_id}/balance/credits', {
      params: { path: { store_id: storeId } },
      body: {
        amount_minor: amount,
        reason: field(form, 'reason') as CreditReason,
        reference: field(form, 'reference'),
        note: field(form, 'note'),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setBalance(data);
    setOpen(false);
    setNotice(`أُضيف ${money(amount, data.currency)} إلى الرصيد.`);
  }

  if (agency) {
    return (
      <section className="card store-card" aria-labelledby="h-balance">
        <h2 id="h-balance">الرصيد</h2>
        <p className="muted small">متجر تابع لوكالة: الوكالة تدفع فاتورة واحدة لمتاجرها، ولا رصيد له.</p>
      </section>
    );
  }
  if (!balance) {
    return error ? (
      <section className="card store-card" aria-labelledby="h-balance">
        <h2 id="h-balance">الرصيد</h2>
        <p role="alert">{error}</p>
      </section>
    ) : null;
  }
  const entries = all ? balance.entries : balance.entries.slice(0, 5);

  return (
    <section className="card store-card" aria-labelledby="h-balance">
      <div className="card-head bare">
        <h2 id="h-balance">الرصيد</h2>
        <span className={`status balance-${balance.level}`}>{levelLabel[balance.level]}</span>
      </div>
      <p className={`balance-amount ${balance.balance_minor < 0 ? 'neg' : ''}`}>{money(balance.balance_minor, balance.currency)}</p>
      <dl className="store-facts">
        <dt>طريقة التحصيل</dt>
        <dd className={balance.collection ? '' : 'muted'}>{balance.collection ? collectionLabel[balance.collection] : 'لم يختر بعد'}</dd>
        <dt>الحد الأدنى</dt>
        <dd>{money(balance.minimum_minor, balance.currency)}</dd>
      </dl>
      {notice && <p role="status">{notice}</p>}
      {entries.length === 0 ? (
        <p className="muted small">لا حركات بعد.</p>
      ) : (
        <ul className="balance-entries">
          {entries.map((e) => (
            <li key={e.id}>
              <span>
                {entryLabel(e)}
                {e.reference ? <small className="muted ltr"> · {e.reference}</small> : null}
                <small className="muted"> · {dateTimeAr(e.created_at)}</small>
              </span>
              <span className={`ltr ${e.amount_minor < 0 ? '' : 'plus'}`}>{signed(e.amount_minor)}</span>
            </li>
          ))}
        </ul>
      )}
      <div className="actions-row">
        {can('billing.manage') && (
          <button
            type="button"
            className="button primary"
            onClick={() => {
              setError('');
              setOpen(true);
            }}
          >
            إضافة رصيد
          </button>
        )}
        {balance.entries.length > 5 && !all && (
          <button
            type="button"
            className="link-button"
            onClick={() => {
              setAll(true);
            }}
          >
            كل الحركات
          </button>
        )}
      </div>

      {open && (
        <Dialog
          title={`إضافة رصيد لـ «${storeName}»`}
          intro={
            balance.balance_minor < 0
              ? `يُخصم المستحق (${money(-balance.balance_minor, balance.currency)}) أولًا. تُسجَّل الإضافة في سجل التدقيق باسمك.`
              : 'تُسجَّل الإضافة في سجل التدقيق باسمك.'
          }
          error={error}
          onSubmit={(e) => void credit(e)}
          onClose={() => {
            setOpen(false);
          }}
        >
          <label>
            <span className="field-label">المبلغ ({balance.currency})</span>
            <input name="amount" inputMode="decimal" dir="ltr" required pattern="[0-9]+([.][0-9]{1,2})?" />
          </label>
          <label>
            <span className="field-label">السبب</span>
            <select name="reason" defaultValue="transfer">
              {reasons.map((r) => (
                <option key={r} value={r}>
                  {reasonLabel[r]}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-label">المرجع (رقم التحويل أو ملاحظة قصيرة)</span>
            <input name="reference" required maxLength={120} />
          </label>
          <label>
            <span className="field-label">ملاحظة لسجل التدقيق</span>
            <input name="note" required minLength={3} maxLength={500} />
          </label>
          <div className="actions">
            <button type="submit" className="button primary" disabled={busy}>
              أضف الرصيد
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setOpen(false);
              }}
            >
              إلغاء
            </button>
          </div>
        </Dialog>
      )}
    </section>
  );
}
