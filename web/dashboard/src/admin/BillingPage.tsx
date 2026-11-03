import { useCallback, useEffect, useState, type SubmitEvent } from 'react';
import { Link, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import {
  accountLink,
  dayLabel,
  exportHref,
  failReason,
  gatewayLabel,
  invoiceNote,
  invoiceQuery,
  invoiceStatusLabel,
  outcomeLabel,
  paymentQuery,
  periodLabel,
  stateOf,
  sums,
  type Invoice,
  type InvoiceQuery,
  type InvoiceState,
  type Outcome,
  type Payment,
  type PaymentQuery,
  type Summary,
} from './billing';
import { ConsoleLayout } from './ConsoleLayout';
import { Dialog } from './Dialog';
import { count, dateTimeAr } from './lib';
import { useCan } from './session';

const statuses: { value: InvoiceState | ''; label: string }[] = [
  { value: '', label: 'الكل' },
  { value: 'open', label: invoiceStatusLabel.open },
  { value: 'overdue', label: invoiceStatusLabel.overdue },
  { value: 'paid', label: invoiceStatusLabel.paid },
  { value: 'void', label: invoiceStatusLabel.void },
];
const outcomes: { value: Outcome | ''; label: string }[] = [
  { value: '', label: 'الكل' },
  ...(['approved', 'declined', 'no_method', 'manual', 'duplicate'] as const).map((o) => ({ value: o, label: outcomeLabel[o] })),
];

// useFilters keeps a page's filters in the address.
function useFilters() {
  const [params, setParams] = useSearchParams();
  const set = useCallback(
    (change: Record<string, string>) => {
      const p = new URLSearchParams(params);
      for (const [k, v] of Object.entries(change)) {
        if (v) p.set(k, v);
        else p.delete(k);
      }
      setParams(p);
    },
    [params, setParams],
  );
  return { params, set };
}

// SummaryCards are the page's four numbers, per currency, never added
// across currencies. Overdue opens the list filtered.
function SummaryCards({ s }: { s: Summary }) {
  const total = (list: Summary['open']) => list.reduce((n, c) => n + c.count, 0);
  const overdue = total(s.overdue);
  const top = s.failed.reasons[0];
  return (
    <section className="kpis billing-kpis" aria-label="ملخص">
      <div className="card kpi">
        <small>مفتوحة</small>
        <strong>{count(total(s.open))}</strong>
        {s.open.length > 0 && <span className="kpi-note">{sums(s.open)}</span>}
      </div>
      <Link to="/billing?status=overdue" className={overdue > 0 ? 'card kpi kpi-link warn' : 'card kpi kpi-link'}>
        <small>متأخرة</small>
        <strong>{count(overdue)}</strong>
        {s.overdue.length > 0 && <span className="kpi-note">{sums(s.overdue)}</span>}
        <span className="kpi-go">اعرضها ←</span>
      </Link>
      <div className="card kpi">
        <small>محصّل هذا الشهر</small>
        <strong className="kpi-money">{s.collected.length > 0 ? sums(s.collected) : '0'}</strong>
        {s.collected.length > 0 && <span className="kpi-note">منها ضريبة: {sums(s.collected, (c) => c.tax_minor)}</span>}
      </div>
      <Link to="/billing?tab=payments&outcome=declined" className="card kpi kpi-link">
        <small>مدفوعات فشلت آخر 7 أيام</small>
        <strong>{count(s.failed.count)}</strong>
        {top && <span className="kpi-note">أكثر سبب: {failReason(top.outcome, top.reason)}</span>}
      </Link>
    </section>
  );
}

// Who is the store or agency an invoice or a payment is for.
function Who({ kind, name, storeId, accountId }: { kind: 'store' | 'agency'; name: string; storeId?: string | undefined; accountId?: string }) {
  const to = accountLink(kind, storeId, accountId);
  return (
    <>
      {to ? <Link to={to}>{name}</Link> : <span>{name}</span>}
      {kind === 'agency' && <span className="kind-note"> وكالة</span>}
    </>
  );
}

// Page is what a list endpoint answers.
interface Page<T> {
  data?: { items: T[]; next_cursor?: string };
  error?: Parameters<typeof problemMessage>[0];
}

// fetchInvoices and fetchPayments read a page through filters, the
// address's filters as JSON.
const fetchInvoices = (filters: string, after: string) =>
  api.GET('/admin/v1/invoices', { params: { query: { ...(JSON.parse(filters) as InvoiceQuery), ...(after ? { cursor: after } : {}) } } });
const fetchPayments = (filters: string, after: string) =>
  api.GET('/admin/v1/payments', { params: { query: { ...(JSON.parse(filters) as PaymentQuery), ...(after ? { cursor: after } : {}) } } });

// usePages reads a list's first page whenever its filters change, and the
// next ones on request.
function usePages<T>(fetch: (filters: string, after: string) => Promise<Page<T>>, filters: string) {
  const [items, setItems] = useState<T[]>([]);
  const [cursor, setCursor] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');

  const show = useCallback((r: Page<T>, more: boolean) => {
    setLoaded(true);
    if (!r.data) {
      setError(problemMessage(r.error));
      return;
    }
    const page = r.data;
    setError('');
    setItems((prev) => (more ? [...prev, ...page.items] : page.items));
    setCursor(page.next_cursor ?? '');
  }, []);

  useEffect(() => {
    let live = true;
    void fetch(filters, '').then((r) => {
      if (live) show(r, false);
    });
    return () => {
      live = false;
    };
  }, [fetch, filters, show]);

  const more = () => void fetch(filters, cursor).then((r) => {
    show(r, true);
  });
  return { items, setItems, cursor, loaded, error, more };
}

function Invoices() {
  const { params, set } = useFilters();
  const query = invoiceQuery(params);
  const key = JSON.stringify(query);
  const { items, cursor, loaded, error, more } = usePages<Invoice>(fetchInvoices, key);

  function search(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    set({ q: field(form, 'q'), currency: field(form, 'currency') });
  }

  const now = new Date();
  const status = query.status ?? '';
  const can = useCan();
  return (
    <section className="table-card billing-list" aria-label="الفواتير">
      <div className="billing-bar">
        <div className="chips" role="group" aria-label="الحالة">
          {statuses.map((s) => (
            <button
              key={s.value}
              type="button"
              className={status === s.value ? 'chip on' : 'chip'}
              aria-pressed={status === s.value}
              onClick={() => {
                set({ status: s.value });
              }}
            >
              {s.label}
            </button>
          ))}
        </div>
        <form role="search" className="billing-search" onSubmit={search} key={key}>
          <label>
            <span className="field-label">العملة</span>
            <select name="currency" defaultValue={query.currency ?? ''}>
              <option value="">كل العملات</option>
              <option value="EGP">جنيه مصري</option>
              <option value="SAR">ريال سعودي</option>
              <option value="AED">درهم إماراتي</option>
            </select>
          </label>
          <label className="grow">
            <span className="visually-hidden">بحث</span>
            <input name="q" type="search" maxLength={100} placeholder="رقم الفاتورة أو المتجر أو الوكالة" defaultValue={query.q ?? ''} />
          </label>
          <button type="submit" className="secondary">
            بحث
          </button>
          {can('billing.manage') && (
            <a className="button secondary" href={exportHref(query)} download>
              تصدير CSV
            </a>
          )}
        </form>
      </div>
      {error && <p role="alert">{error}</p>}
      {loaded && items.length === 0 && !error && <p className="empty flat">{params.size ? 'لا فواتير تطابق هذه التصفية.' : 'لا فواتير بعد.'}</p>}
      {items.length > 0 && (
        <div className="scroll">
          <table className="billing-table">
            <thead>
              <tr>
                <th scope="col">الفاتورة</th>
                <th scope="col">لـ</th>
                <th scope="col">الفترة</th>
                <th scope="col">الإجمالي</th>
                <th scope="col">الاستحقاق</th>
                <th scope="col">الحالة</th>
              </tr>
            </thead>
            <tbody>
              {items.map((inv) => {
                const st = stateOf(inv);
                const note = invoiceNote(inv, now);
                return (
                  <tr key={inv.id}>
                    <td className="b-number">
                      <Link to={`/billing/invoices/${inv.id}`} className="ltr code">
                        {inv.number}
                      </Link>
                    </td>
                    <td className="b-who">
                      <Who kind={inv.account_kind} name={inv.account_name} storeId={inv.store_id} accountId={inv.account_id} />
                    </td>
                    <td className="b-period muted">{periodLabel(inv.period)}</td>
                    <td className="b-total num">{money(inv.total_minor, inv.currency)}</td>
                    <td className="b-due muted">{dayLabel(inv.due_at)}</td>
                    <td className="b-status">
                      <span className={`status inv-${st}`}>{invoiceStatusLabel[st]}</span>
                      {note && <span className="sub">{note}</span>}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {cursor && (
        <p className="foot billing-more">
          <button type="button" className="secondary" onClick={more}>
            عرض المزيد
          </button>
        </p>
      )}
    </section>
  );
}

function Payments() {
  const { params, set } = useFilters();
  const query = paymentQuery(params);
  const key = JSON.stringify(query);
  const { items, setItems, cursor, loaded, error, more } = usePages<Payment>(fetchPayments, key);
  const [refunding, setRefunding] = useState<Payment | null>(null);
  const [refundError, setRefundError] = useState('');

  async function refund(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!refunding) return;
    const form = new FormData(e.currentTarget);
    const { error: err } = await api.POST('/admin/v1/billing/duplicates/{payment_id}/refunded', {
      params: { path: { payment_id: refunding.id } },
      body: { reference: field(form, 'reference'), reason: field(form, 'reason') },
    });
    if (err) {
      setRefundError(problemMessage(err));
      return;
    }
    const id = refunding.id;
    const at = new Date().toISOString();
    setItems((prev) => prev.map((p) => (p.id === id ? { ...p, refunded_at: at } : p)));
    setRefunding(null);
  }

  const outcome = query.outcome ?? '';
  const can = useCan();
  return (
    <section className="table-card billing-list" aria-label="المدفوعات">
      <div className="billing-bar">
        <div className="chips" role="group" aria-label="النتيجة">
          {outcomes.map((o) => (
            <button
              key={o.value}
              type="button"
              className={outcome === o.value ? 'chip on' : 'chip'}
              aria-pressed={outcome === o.value}
              onClick={() => {
                set({ outcome: o.value });
              }}
            >
              {o.label}
            </button>
          ))}
        </div>
        <label className="billing-gateway">
          <span className="field-label">البوابة</span>
          <select
            value={query.gateway ?? ''}
            onChange={(e) => {
              set({ gateway: e.target.value });
            }}
          >
            <option value="">كل البوابات</option>
            <option value="paymob">Paymob</option>
            <option value="manual">تحويل</option>
          </select>
        </label>
      </div>
      {error && <p role="alert">{error}</p>}
      {loaded && items.length === 0 && !error && <p className="empty flat">{params.size > 1 ? 'لا مدفوعات تطابق هذه التصفية.' : 'لا مدفوعات بعد.'}</p>}
      {items.length > 0 && (
        <div className="scroll">
          <table className="billing-table payments-table">
            <thead>
              <tr>
                <th scope="col">الوقت</th>
                <th scope="col">الفاتورة</th>
                <th scope="col">لـ</th>
                <th scope="col">البوابة</th>
                <th scope="col">النتيجة</th>
                <th scope="col">المبلغ</th>
                <th scope="col">
                  <span className="visually-hidden">إجراء</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((p) => {
                const failed = p.outcome === 'declined' || p.outcome === 'no_method';
                const waiting = p.outcome === 'duplicate' && !p.refunded_at;
                return (
                  <tr key={p.id} className={waiting ? 'flagged' : undefined}>
                    <td className="b-due muted">{dateTimeAr(p.created_at)}</td>
                    <td className="b-number">
                      <Link to={`/billing/invoices/${p.invoice_id}`} className="ltr code">
                        {p.invoice_number}
                      </Link>
                    </td>
                    <td className="b-who">
                      <Who kind={p.account_kind} name={p.account_name} storeId={p.store_id} />
                    </td>
                    <td className="b-period">
                      {gatewayLabel(p.gateway)}
                      {p.reference && <span className="sub ltr">{p.reference}</span>}
                    </td>
                    <td className="b-status">
                      <span className={`status pay-${p.outcome}`}>{outcomeLabel[p.outcome]}</span>
                      {failed && <span className="sub">{failReason(p.outcome, p.reason ?? '')}</span>}
                      {p.outcome === 'duplicate' && <span className="sub">{p.refunded_at ? `اُسترد ${dayLabel(p.refunded_at)}` : 'دُفعت الفاتورة قبلها: استردها'}</span>}
                    </td>
                    <td className="b-total num">{money(p.amount_minor, p.currency)}</td>
                    <td className="b-action">
                      {waiting && can('billing.manage') && (
                        <button
                          type="button"
                          className="secondary"
                          onClick={() => {
                            setRefundError('');
                            setRefunding(p);
                          }}
                        >
                          تم الاسترداد
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {cursor && (
        <p className="foot billing-more">
          <button type="button" className="secondary" onClick={more}>
            عرض المزيد
          </button>
        </p>
      )}
      {refunding && (
        <Dialog
          title="سجّل الاسترداد"
          intro={`بعد أن تستردّ ${money(refunding.amount_minor, refunding.currency)} من بوابة الدفع. يُسجَّل في سجل الأدمن.`}
          error={refundError}
          onSubmit={(e) => void refund(e)}
          onClose={() => {
            setRefunding(null);
          }}
        >
          <label>
            رقم عملية الاسترداد
            <input name="reference" required maxLength={120} dir="ltr" />
          </label>
          <label>
            السبب
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <div className="actions">
            <button type="submit">سجّل</button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setRefunding(null);
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

// BillingPage is the platform's invoices and payment attempts (the
// approved mockup, ADR-0076): a summary per currency, then every
// account's invoices or payments, filtered in the address.
export function BillingPage() {
  const [params, setParams] = useSearchParams();
  const tab = params.get('tab') === 'payments' ? 'payments' : 'invoices';
  const [summary, setSummary] = useState<Summary | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/billing/summary').then((r) => {
      if (!live) return;
      if (r.data) setSummary(r.data);
      else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, []);

  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>الفواتير والمدفوعات</h1>
          <p className="muted">كل فواتير المنصة ومحاولات دفعها، لكل المتاجر والوكالات.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {summary && <SummaryCards s={summary} />}
      <div className="segmented-tabs" role="tablist" aria-label="العرض">
        {(
          [
            ['invoices', 'الفواتير'],
            ['payments', 'المدفوعات'],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            type="button"
            role="tab"
            aria-selected={tab === value}
            onClick={() => {
              setParams(value === 'payments' ? { tab: 'payments' } : {});
            }}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'payments' ? <Payments /> : <Invoices />}
    </ConsoleLayout>
  );
}
