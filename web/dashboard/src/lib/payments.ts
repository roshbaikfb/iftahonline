import type { components } from '../api/client';

export type Payment = components['schemas']['Payment'];
export type PaymentRefund = components['schemas']['PaymentRefund'];

// Arabic labels of a card payment's states (ADR-0029).
export const paymentLabel: Record<components['schemas']['PaymentStatus'], string> = {
  pending: 'بانتظار الدفع',
  paid: 'مدفوع',
  failed: 'فشل',
  expired: 'انتهت المهلة',
  partially_refunded: 'مسترد جزئيًا',
  refunded: 'مسترد',
};

export const refundLabel: Record<PaymentRefund['status'], string> = {
  requested: 'قيد التنفيذ',
  succeeded: 'تم',
  failed: 'فشل',
};

// The order's own payment status (ADR-0029).
export const orderPaymentLabel: Record<components['schemas']['Order']['payment_status'], string> = {
  unpaid: 'لم يُحصَّل',
  awaiting: 'بانتظار الدفع',
  paid: 'مدفوع',
  partially_refunded: 'مسترد جزئيًا',
  refunded: 'مسترد',
};

// refundable is what is left to refund of a payment; the server also
// counts refunds still in flight.
export function refundable(p: Payment): number {
  return p.status === 'paid' || p.status === 'partially_refunded' ? p.amount_minor - p.refunded_minor : 0;
}

const gatewayName: Record<string, string> = { paymob: 'Paymob', fakepay: 'بوابة دفع تجريبية' };

// gatewayLabel names a gateway for merchants.
export function gatewayLabel(g: string): string {
  return gatewayName[g] ?? g;
}
