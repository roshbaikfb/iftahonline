import type { components } from '../api/client';
import type { paths } from '../api/schema';
import { money } from '../lib/orders';

export type Invoice = components['schemas']['AdminInvoice'];
export type InvoiceDetail = components['schemas']['AdminInvoiceDetail'];
export type Payment = components['schemas']['AdminPayment'];
export type Summary = components['schemas']['AdminBillingSummary'];
export type CurrencyTotal = components['schemas']['AdminCurrencyTotal'];
export type Outcome = components['schemas']['AdminPaymentOutcome'];
export type InvoiceQuery = NonNullable<paths['/admin/v1/invoices']['get']['parameters']['query']>;
export type PaymentQuery = NonNullable<paths['/admin/v1/payments']['get']['parameters']['query']>;

// InvoiceState is an invoice's status as the console shows it: an open
// invoice past due is overdue.
export type InvoiceState = 'open' | 'overdue' | 'paid' | 'void';

export const invoiceStatusLabel: Record<InvoiceState, string> = {
  open: 'مفتوحة',
  overdue: 'متأخرة',
  paid: 'مدفوعة',
  void: 'ملغاة',
};

export const stateOf = (inv: Invoice): InvoiceState => (inv.overdue ? 'overdue' : inv.status);

export const outcomeLabel: Record<Outcome, string> = {
  approved: 'نجحت',
  declined: 'رُفضت',
  no_method: 'بلا بطاقة',
  manual: 'تحويل يدوي',
  duplicate: 'مكررة',
};

export const lineKindLabel: Record<InvoiceDetail['lines'][number]['kind'], string> = {
  subscription: 'اشتراك',
  sell_fees: 'رسوم المبيعات',
  fee_credit: 'رصيد رسوم مستردة',
  founding_discount: 'خصم التاجر المؤسس',
};

export function gatewayLabel(g: string): string {
  if (g === 'paymob') return 'Paymob';
  if (g === 'manual') return 'تحويل';
  return g || '—';
}

// sums are totals in each currency, side by side; never added together.
export const sums = (list: CurrencyTotal[], pick: (c: CurrencyTotal) => number = (c) => c.total_minor) =>
  list.map((c) => money(pick(c), c.currency)).join(' · ');

const plural = (n: number, one: string, two: string, few: string, many: string) =>
  n === 1 ? one : n === 2 ? two : n <= 10 ? `${String(n)} ${few}` : `${String(n)} ${many}`;

export const daysText = (n: number) => plural(n, 'يوم', 'يومين', 'أيام', 'يومًا');
export const attemptsText = (n: number) => plural(n, 'محاولة', 'محاولتان', 'محاولات', 'محاولة');

// daysLate counts whole days since due.
export const daysLate = (due: string, now: Date) => Math.max(1, Math.floor((now.getTime() - new Date(due).getTime()) / 86_400_000));

export const dayLabel = (iso: string) => new Date(iso).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long' });

export const periodLabel = (date: string) => new Date(`${date}T00:00:00Z`).toLocaleDateString('ar-EG-u-nu-latn', { month: 'long', year: 'numeric', timeZone: 'UTC' });

// invoiceNote is the line under an invoice's status: how late and why,
// when it is due, or when it was paid.
export function invoiceNote(inv: Invoice, now: Date): string {
  const parts: string[] = [];
  switch (stateOf(inv)) {
    case 'overdue':
      parts.push(`متأخرة ${daysText(daysLate(inv.due_at, now))}`);
      break;
    case 'open':
      parts.push(`مستحقة ${dayLabel(inv.due_at)}`);
      break;
    case 'paid':
      return inv.paid_at ? `دُفعت ${dayLabel(inv.paid_at)}` : '';
    case 'void':
      return '';
  }
  if (inv.attempts > 0) parts.push(attemptsText(inv.attempts));
  if (inv.last_error) parts.push(inv.last_error);
  return parts.join(' · ');
}

// failReason is a failed payment's reason in words.
export function failReason(outcome: string, reason: string): string {
  if (reason) return reason;
  return outcome === 'no_method' ? 'لا بطاقة محفوظة' : 'بلا سبب من البوابة';
}

// accountLink is where a store's or an agency's page is.
export function accountLink(kind: 'store' | 'agency', storeId: string | undefined, accountId?: string): string | undefined {
  if (kind === 'store') return storeId ? `/stores/${storeId}` : undefined;
  return accountId ? `/agencies/${accountId}` : undefined;
}

const isStatus = (v: string | null): v is NonNullable<InvoiceQuery['status']> => v === 'open' || v === 'overdue' || v === 'paid' || v === 'void';
const isCurrency = (v: string | null): v is NonNullable<InvoiceQuery['currency']> => v === 'EGP' || v === 'SAR' || v === 'AED';
const isOutcome = (v: string | null): v is Outcome => v !== null && Object.hasOwn(outcomeLabel, v);

// invoiceQuery reads the invoices' filters from the address.
export function invoiceQuery(params: URLSearchParams): InvoiceQuery {
  const q: InvoiceQuery = {};
  const status = params.get('status');
  if (isStatus(status)) q.status = status;
  const currency = params.get('currency');
  if (isCurrency(currency)) q.currency = currency;
  const text = params.get('q')?.trim();
  if (text) q.q = text;
  return q;
}

// paymentQuery reads the payments' filters from the address.
export function paymentQuery(params: URLSearchParams): PaymentQuery {
  const q: PaymentQuery = {};
  const outcome = params.get('outcome');
  if (isOutcome(outcome)) q.outcome = outcome;
  const gateway = params.get('gateway');
  if (gateway && /^[a-z_]{1,32}$/.test(gateway)) q.gateway = gateway;
  return q;
}

// exportHref is the CSV of the invoices with the list's filters.
export function exportHref(q: InvoiceQuery): string {
  const p = new URLSearchParams();
  if (q.status) p.set('status', q.status);
  if (q.currency) p.set('currency', q.currency);
  if (q.q) p.set('q', q.q);
  return `/admin/v1/invoices/export${p.size ? `?${p.toString()}` : ''}`;
}
