import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import {
  accountLink,
  dayLabel,
  daysLate,
  daysText,
  failReason,
  gatewayLabel,
  invoiceStatusLabel,
  lineKindLabel,
  outcomeLabel,
  periodLabel,
  stateOf,
  type InvoiceDetail,
} from './billing';
import { ConsoleLayout } from './ConsoleLayout';
import { dateTimeAr } from './lib';

// Settle records an open invoice as paid by a transfer that arrived: its
// stores renew as on any payment, and the admin log keeps the reason.
function Settle({ id, onSettled }: { id: string; onSettled: (d: InvoiceDetail) => void }) {
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    const r = await api.POST('/admin/v1/invoices/{invoice_id}/settle', {
      params: { path: { invoice_id: id } },
      body: { reference: field(form, 'reference'), reason: field(form, 'reason') },
    });
    setBusy(false);
    if (r.error) {
      setError(r.response.status === 404 ? 'لم تعد الفاتورة مفتوحة. حدّث الصفحة.' : problemMessage(r.error));
      return;
    }
    onSettled(r.data);
  }

  return (
    <section className="card settle-card" aria-labelledby="h-settle">
      <h2 id="h-settle">سجّل كمدفوعة بتحويل</h2>
      <p className="muted small">بعد أن يصل التحويل. تُطبَّق كأي دفعة: يتجدد اشتراك المتجر، ويُسجَّل الإجراء في سجل الأدمن.</p>
      {error && <p role="alert">{error}</p>}
      <form className="settle-form" onSubmit={(e) => void submit(e)}>
        <label>
          رقم التحويل
          <input name="reference" required maxLength={120} dir="ltr" />
        </label>
        <label>
          السبب
          <input name="reason" required minLength={3} maxLength={500} />
        </label>
        <button type="submit" disabled={busy}>
          سجّل كمدفوعة
        </button>
      </form>
    </section>
  );
}

// InvoicePage is one invoice (the approved mockup): who it is for, its
// lines and VAT, a transfer to settle while it is open, and every
// payment attempt.
export function InvoicePage() {
  const { invoiceId = '' } = useParams();
  const [d, setD] = useState<InvoiceDetail | null>(null);
  const [error, setError] = useState('');
  const [settled, setSettled] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/invoices/{invoice_id}', { params: { path: { invoice_id: invoiceId } } }).then((r) => {
      if (!live) return;
      if (r.data) setD(r.data);
      else setError(r.response.status === 404 ? 'لا فاتورة بهذا الرقم.' : problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, [invoiceId]);

  if (!d) {
    return (
      <ConsoleLayout>
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
        <Link to="/billing">كل الفواتير</Link>
      </ConsoleLayout>
    );
  }
  const inv = d.invoice;
  const st = stateOf(inv);
  const who = accountLink(inv.account_kind, inv.store_id, inv.account_id);
  const pill = st === 'overdue' ? `متأخرة ${daysText(daysLate(inv.due_at, new Date()))}` : invoiceStatusLabel[st];
  return (
    <ConsoleLayout>
      <nav aria-label="مسار الصفحة" className="crumbs">
        <Link to="/billing">الفواتير</Link> / <span className="ltr">{inv.number}</span>
      </nav>
      <div className="invoice-head">
        <div>
          <h1>
            <span className="ltr">{inv.number}</span> <span className={`status inv-${st}`}>{pill}</span>
          </h1>
          <p className="muted">
            لـ {who ? <Link to={who}>{inv.account_name}</Link> : inv.account_name}
            {inv.account_kind === 'agency' && ' (وكالة)'} · فترة {periodLabel(inv.period)} · صدرت {dayLabel(inv.issued_at)} · مستحقة{' '}
            {dayLabel(inv.due_at)}
            {inv.paid_at && ` · دُفعت ${dayLabel(inv.paid_at)}`}
          </p>
        </div>
        <strong className="invoice-total">{money(inv.total_minor, inv.currency)}</strong>
      </div>
      {settled && <p role="status" className="notice ok">سُجّلت الفاتورة مدفوعة.</p>}

      <div className={inv.status === 'open' ? 'invoice-grid' : 'invoice-grid one'}>
        <section className="table-card" aria-labelledby="h-lines">
          <div className="card-head">
            <h2 id="h-lines">البنود</h2>
          </div>
          <table className="narrow invoice-lines">
            <tbody>
              {d.lines.map((l, i) => (
                <tr key={i}>
                  <td>
                    {l.description}
                    <span className="sub">
                      {lineKindLabel[l.kind]}
                      {l.quantity > 1 && ` · ${String(l.quantity)}`}
                    </span>
                  </td>
                  <td className="line-amount">{money(l.amount_minor, inv.currency)}</td>
                </tr>
              ))}
              <tr className="sum">
                <td>المجموع قبل الضريبة</td>
                <td className="line-amount">{money(inv.subtotal_minor, inv.currency)}</td>
              </tr>
              <tr className="sum">
                <td>ضريبة القيمة المضافة {String(inv.tax_bps / 100)}%</td>
                <td className="line-amount">{money(inv.tax_minor, inv.currency)}</td>
              </tr>
              <tr className="total">
                <td>
                  <strong>الإجمالي</strong>
                </td>
                <td className="line-amount">
                  <strong>{money(inv.total_minor, inv.currency)}</strong>
                </td>
              </tr>
            </tbody>
          </table>
        </section>
        {inv.status === 'open' && (
          <Settle
            id={inv.id}
            onSettled={(next) => {
              setD(next);
              setSettled(true);
            }}
          />
        )}
      </div>

      <section className="table-card" aria-labelledby="h-attempts">
        <div className="card-head">
          <h2 id="h-attempts">محاولات الدفع</h2>
          {inv.status === 'open' && inv.next_attempt_at && <span className="muted small">المحاولة القادمة {dayLabel(inv.next_attempt_at)}</span>}
        </div>
        {d.payments.length === 0 ? (
          <p className="empty flat">لا محاولات بعد.</p>
        ) : (
          <div className="scroll">
            <table className="billing-table">
              <thead>
                <tr>
                  <th scope="col">الوقت</th>
                  <th scope="col">البوابة</th>
                  <th scope="col">النتيجة</th>
                  <th scope="col">المبلغ</th>
                  <th scope="col">المرجع</th>
                </tr>
              </thead>
              <tbody>
                {d.payments.map((p) => (
                  <tr key={p.id}>
                    <td className="b-due muted">{dateTimeAr(p.created_at)}</td>
                    <td className="b-period">{gatewayLabel(p.gateway)}</td>
                    <td className="b-status">
                      <span className={`status pay-${p.outcome}`}>{outcomeLabel[p.outcome]}</span>
                      {(p.outcome === 'declined' || p.outcome === 'no_method') && <span className="sub">{failReason(p.outcome, p.reason ?? '')}</span>}
                    </td>
                    <td className="b-total num">{money(p.amount_minor, p.currency)}</td>
                    <td className="b-number ltr muted">{p.reference ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </ConsoleLayout>
  );
}
