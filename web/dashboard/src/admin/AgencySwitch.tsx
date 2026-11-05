import { useState, type SubmitEvent } from 'react';

import { api, type components } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

type Detail = components['schemas']['AdminAgencyDetail'];

// AgencySwitch moves stores of an unpaid agency to «ادفع حسب مبيعاتك»,
// as agreed with it (ADR-0087 §7; the approved mockup 8): each gets its
// own account, the agency's invoice is voided, and the stores that stay
// are invoiced again at the agency's price.
export function AgencySwitch({ detail, names, onDone }: { detail: Detail; names: Map<string, string>; onDone: (d: Detail, n: number) => void }) {
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const invoice = detail.open_invoices?.[0];
  if (!invoice) return null;
  const staying = detail.store_ids.filter((s) => !chosen.has(s));

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/admin/v1/agencies/{agency_id}/switch-to-pays', {
      params: { path: { agency_id: detail.agency.id } },
      body: { store_ids: [...chosen], collection: field(form, 'collection') as 'balance' | 'card', reason: field(form, 'reason') },
    });
    setBusy(false);
    if (!data) {
      setError(problemMessage(err));
      return;
    }
    const n = chosen.size;
    setChosen(new Set());
    onDone(data, n);
  }

  return (
    <form className="card agency-switch" aria-labelledby="h-switch" onSubmit={(e) => void submit(e)}>
      <h2 id="h-switch">تحويل متاجر إلى «ادفع حسب مبيعاتك»</h2>
      <p className="muted">
        فاتورة <span className="ltr">{invoice.number}</span> ({money(invoice.total_minor, invoice.currency)}) غير مدفوعة. بعد الاتفاق مع الوكالة اختر المتاجر
        التي تتحول.
      </p>
      <div className="switch-stores">
        {detail.store_ids.map((s) => (
          <label key={s} className="check">
            <input
              type="checkbox"
              checked={chosen.has(s)}
              onChange={(e) => {
                const next = new Set(chosen);
                if (e.target.checked) next.add(s);
                else next.delete(s);
                setChosen(next);
              }}
            />{' '}
            {names.get(s) ?? s}
          </label>
        ))}
      </div>
      <label>
        <span className="field-label">طريقة التحصيل للمتاجر المحوَّلة</span>
        <select name="collection" defaultValue="balance">
          <option value="balance">رصيد مسبق</option>
          <option value="card">البطاقة المحفوظة للوكالة (تُنسخ لكل متجر)</option>
        </select>
      </label>
      <dl className="store-facts">
        <dt>الفاتورة الحالية</dt>
        <dd>تُلغى</dd>
        <dt>المتاجر المحوَّلة</dt>
        <dd>تخرج من حساب الوكالة، ولكل متجر حسابه</dd>
        {staying.length > 0 && chosen.size > 0 && (
          <>
            <dt>{staying.length === 1 ? `«${names.get(staying[0] ?? '') ?? ''}»` : `${String(staying.length)} متاجر باقية`}</dt>
            <dd>فاتورة جديدة بسعر الوكالة، وتستمر المهلة</dd>
          </>
        )}
      </dl>
      <label>
        <span className="field-label">ملاحظة الاتفاق (إجباري)</span>
        <input name="reason" required minLength={3} maxLength={500} />
      </label>
      {error && <p role="alert">{error}</p>}
      <div className="actions-row">
        <button type="submit" className="button primary" disabled={busy || chosen.size === 0}>
          {chosen.size === 0 ? 'اختر متجرًا على الأقل' : `حوّل ${String(chosen.size)} ${chosen.size === 1 ? 'متجرًا' : 'متاجر'} وألغِ الفاتورة`}
        </button>
      </div>
    </form>
  );
}
