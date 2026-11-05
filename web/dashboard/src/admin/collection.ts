import type { components } from '../api/client';

export type CollectionSettings = components['schemas']['AdminCollectionSettings'];
export type StoreBalance = components['schemas']['AdminStoreBalance'];
export type BalanceEntry = components['schemas']['AdminBalanceEntry'];
export type Currency = CollectionSettings['minimums'][number]['currency'];
export type CreditReason = components['schemas']['AdminBalanceCreditInput']['reason'];

export const currencyLabel: Record<Currency, string> = {
  EGP: 'مصر · جنيه',
  SAR: 'السعودية · ريال',
  AED: 'الإمارات · درهم',
};

export const levelLabel: Record<StoreBalance['level'], string> = {
  ok: 'كافٍ',
  low: 'منخفض',
  debt: 'بالسالب أو نفد',
};

export const collectionLabel: Record<NonNullable<StoreBalance['collection']>, string> = {
  balance: 'رصيد مسبق',
  card: 'البطاقة المحفوظة',
};

export const reasonLabel: Record<CreditReason, string> = {
  transfer: 'تحويل بنكي مؤكَّد',
  compensation: 'تعويض',
  correction: 'تصحيح',
};

// entryLabel is what an entry was, as the console lists it.
export function entryLabel(e: BalanceEntry): string {
  switch (e.kind) {
    case 'topup':
      return 'شحن بالبطاقة';
    case 'fee':
      return 'رسوم طلب مسلَّم';
    case 'fee_refund':
      return 'رد رسوم طلب مرتجع';
    case 'invoice':
      return 'ضريبة الفاتورة الشهرية';
    case 'manual':
      return `إضافة يدوية: ${e.reason ? reasonLabel[e.reason] : ''}`;
    default:
      // A kind this console does not know yet (an extensible list, ADR-0032).
      return 'حركة';
  }
}

// toMinor reads an amount typed in major units ("300" or "12.5") as
// minor units, NaN for anything else.
export function toMinor(text: string): number {
  const t = text.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return Number.NaN;
  return Math.round(Number(t) * 100);
}

// major writes minor units as the amount typed in the form: 30000 → "300".
export const major = (minor: number) => String(minor / 100);

// signed writes an entry's amount with its sign, e.g. "+500.00".
export function signed(minor: number): string {
  const v = (Math.abs(minor) / 100).toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  return `${minor < 0 ? '−' : '+'}${v}`;
}
