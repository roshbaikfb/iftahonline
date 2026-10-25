import type { components } from '../api/client';

export type Order = components['schemas']['Order'];
export type OrderStatus = components['schemas']['OrderStatus'];

// Arabic labels of the order states (ADR-0021).
export const statusLabel: Record<OrderStatus, string> = {
  pending: 'قيد الانتظار',
  confirmed: 'مؤكد',
  ready_to_ship: 'جاهز للشحن',
  shipped: 'تم الشحن',
  delivered: 'تم التسليم',
  cancelled: 'ملغي',
  failed_delivery: 'فشل التسليم',
  returned: 'مرتجع',
};

export const statuses = Object.keys(statusLabel) as OrderStatus[];

// nextSteps mirrors the server's state machine, only to choose which
// buttons to show; the server decides (and checks the permission).
export const nextSteps: Record<OrderStatus, OrderStatus[]> = {
  pending: ['confirmed', 'cancelled'],
  confirmed: ['ready_to_ship', 'cancelled'],
  ready_to_ship: ['shipped', 'cancelled'],
  shipped: ['delivered', 'failed_delivery'],
  failed_delivery: ['shipped', 'returned'],
  delivered: ['returned'],
  cancelled: [],
  returned: [],
};

// The button for moving to a status.
export const stepLabel: Record<OrderStatus, string> = {
  pending: '',
  confirmed: 'تأكيد',
  ready_to_ship: 'جاهز للشحن',
  shipped: 'شُحن',
  delivered: 'تم التسليم',
  cancelled: 'إلغاء',
  failed_delivery: 'فشل التسليم',
  returned: 'مرتجع',
};

const minorDigits: Record<string, number> = { EGP: 2, SAR: 2, AED: 2 };
const symbols: Record<string, string> = { EGP: 'ج.م', SAR: 'ر.س', AED: 'د.إ' };

// money formats integer minor units the way the storefront does:
// Western digits, grouping, decimals only when there are any.
export function money(minor: number, currency: string): string {
  const digits = minorDigits[currency] ?? 2;
  const value = minor / 10 ** digits;
  const text = value.toLocaleString('en-US', {
    minimumFractionDigits: Number.isInteger(value) ? 0 : digits,
    maximumFractionDigits: digits,
  });
  return `${text} ${symbols[currency] ?? currency}`;
}

export function dateTime(iso: string): string {
  return new Date(iso).toLocaleString('ar-EG-u-nu-latn', { dateStyle: 'medium', timeStyle: 'short' });
}

// dayStart is the start of a local calendar day; "to" filters are
// exclusive, so they take the start of the day after (plusDays 1).
export function dayStart(day: string, plusDays = 0): string {
  const d = new Date(`${day}T00:00:00`);
  d.setDate(d.getDate() + plusDays);
  return d.toISOString();
}

export type OrderCounts = components['schemas']['OrderCounts'];

// The status tabs of the orders list, in the merchant's working order.
export const tabStatuses: OrderStatus[] = ['pending', 'confirmed', 'ready_to_ship', 'shipped', 'delivered', 'cancelled', 'failed_delivery', 'returned'];

// paymentPill is how an order's payment reads in a list: how it is paid
// and where that stands, with the pill colour of its state.
export function paymentPill(o: Pick<Order, 'payment_method' | 'payment_status'>): { label: string; cls: string } {
  if (o.payment_status === 'refunded' || o.payment_status === 'partially_refunded') {
    return { label: o.payment_status === 'refunded' ? 'مسترد' : 'مسترد جزئيًا', cls: 'pay-refunded' };
  }
  if (o.payment_method === 'card') {
    return o.payment_status === 'paid' ? { label: 'مدفوع بالبطاقة', cls: 'pay-paid' } : { label: 'بانتظار الدفع', cls: 'pay-awaiting' };
  }
  return o.payment_status === 'paid' ? { label: 'محصَّل', cls: 'pay-paid' } : { label: 'عند الاستلام', cls: '' };
}

// selectedLabel counts selected orders the way Arabic says it.
export function selectedLabel(n: number): string {
  if (n === 1) return 'طلب واحد محدد';
  if (n === 2) return 'طلبان محددان';
  if (n <= 10) return `${String(n)} طلبات محددة`;
  return `${String(n)} طلبًا محددًا`;
}
