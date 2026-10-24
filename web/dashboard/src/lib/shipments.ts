import type { components } from '../api/client';

export type Shipment = components['schemas']['Shipment'];
export type ShipmentStatus = components['schemas']['ShipmentStatus'];

// Arabic labels of the shipment states (ADR-0027).
export const shipmentLabel: Record<ShipmentStatus, string> = {
  requested: 'قيد الإنشاء',
  created: 'أُنشئت',
  in_transit: 'في الطريق',
  out_for_delivery: 'خرجت للتسليم',
  delivered: 'سُلّمت',
  failed_attempt: 'محاولة فاشلة',
  returned: 'مرتجعة',
  cancelled: 'ملغاة',
  failed: 'رفضتها شركة الشحن',
};

export const shipmentStatuses = Object.keys(shipmentLabel) as ShipmentStatus[];

// live reports whether a shipment still counts for its order (one live
// shipment per order).
export function live(s: Shipment): boolean {
  return s.status !== 'cancelled' && s.status !== 'failed';
}

// cancellable mirrors the server's rule, only to show the button.
export function cancellable(s: Shipment): boolean {
  return (s.status === 'requested' || s.status === 'created') && !s.cancel_requested_at;
}

export const sourceLabel: Record<components['schemas']['ShipmentEvent']['source'], string> = {
  api: 'شركة الشحن',
  webhook: 'إشعار شركة الشحن',
  poll: 'متابعة دورية',
  merchant: 'المتجر',
};

const carrierName: Record<string, string> = {
  bosta: 'بوسطة',
  fake: 'شركة شحن تجريبية',
};

// carrierLabel names a carrier for merchants.
export function carrierLabel(c: string): string {
  return carrierName[c] ?? c;
}
