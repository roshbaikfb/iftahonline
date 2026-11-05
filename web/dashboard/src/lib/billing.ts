import type { components } from '../api/schema';
import { money } from './orders';

export type Billing = components['schemas']['Billing'];
export type BillingPlans = components['schemas']['BillingPlans'];
export type BillingPlan = components['schemas']['BillingPlan'];
export type BillingFeature = components['schemas']['BillingFeature'];
export type UsageMeter = components['schemas']['UsageMeter'];
export type Cycle = 'monthly' | 'annual';

// goTo leaves the dashboard for Paymob's page (a function so tests can
// see where it went).
export const leave = {
  goTo: (url: string) => {
    window.location.assign(url);
  },
};

export const statusLabel: Record<Billing['status'], string> = {
  trialing: 'تجربة مجانية',
  active: 'نشطة',
  past_due: 'متأخرة الدفع',
  suspended: 'موقوفة',
  cancelled: 'ملغاة',
};

export const cycleLabel: Record<Cycle, string> = {
  monthly: 'شهرية',
  annual: 'سنوية',
};

// dateAr is a day as merchants read it: 9 نوفمبر 2026.
export function dateAr(iso: string): string {
  return new Date(iso).toLocaleDateString('ar-EG-u-nu-latn', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
    timeZone: 'UTC',
  });
}

// lateText is what an unpaid store's banner says (ADR-0082): past due
// keeps its plan until the grace's end, then the free plan until
// closes_at, then the maintenance page.
export function lateText(b: Billing): string {
  const closes = b.closes_at ? ` وإذا لم تُدفع يُغلق المتجر يوم ${dateAr(b.closes_at)}.` : '';
  if (b.status === 'past_due') {
    const grace = b.grace_until ? ` قبل ${dateAr(b.grace_until)}` : '';
    return `ادفعها${grace} حتى يبقى متجرك على باقته، وإلا ينتقل إلى الباقة المجانية.${closes} بياناتك محفوظة.`;
  }
  if (b.held || b.status === 'cancelled') {
    return 'متجرك مغلق ويرى العملاء صفحة صيانة حتى تُدفع الفاتورة. بياناتك محفوظة.';
  }
  return `متجرك يعمل، لكن تظهر فيه إعلانات المنصة، والدفع بالبطاقة والكوبونات والبكسلات متوقفة.${closes} بياناتك محفوظة.`;
}

// monthAr is an invoice's month: أكتوبر 2026.
export function monthAr(isoDate: string): string {
  return new Date(`${isoDate.slice(0, 10)}T00:00:00Z`).toLocaleDateString('ar-EG-u-nu-latn', { month: 'long', year: 'numeric', timeZone: 'UTC' });
}

// daysLeft is the whole days until iso, never below zero.
export function daysLeft(iso: string, now: Date = new Date()): number {
  return Math.max(0, Math.ceil((new Date(iso).getTime() - now.getTime()) / 86_400_000));
}

const count = (n: number) => n.toLocaleString('en-US');

// The meters' labels and what reaching the limit means (approved: orders
// are never blocked).
export const meterLabel: Record<UsageMeter['feature'], string> = {
  orders_per_month: 'الطلبات',
  messages: 'رسائل واتساب وSMS',
  products: 'المنتجات',
  staff: 'فريق العمل',
  custom_domains: 'الدومينات الخاصة',
};
export const meterNote: Record<UsageMeter['feature'], string> = {
  orders_per_month: 'بعد الحد تُستقبل الطلبات عادي، وتصلك تنبيهات',
  messages: 'عند الحد تتوقف الرسائل الآلية فقط',
  products: 'إجمالي المنتجات في المتجر',
  staff: 'شامل المالك والدعوات المفتوحة',
  custom_domains: '',
};

export type Level = 'ok' | 'nearly' | 'reached';

// meterLevel warns at 80% and at the limit, as the notices do.
export function meterLevel(m: UsageMeter): Level {
  if (m.unlimited || m.limit === undefined) return 'ok';
  if (m.used >= m.limit) return 'reached'; // a limit of 0 is reached from the start
  return m.used / m.limit >= 0.8 ? 'nearly' : 'ok';
}

export function meterText(m: UsageMeter): string {
  if (m.unlimited || m.limit === undefined) return `${count(m.used)} / بلا حد`;
  return `${count(m.used)} / ${count(m.limit)}`;
}

export function meterPercent(m: UsageMeter): number {
  if (m.unlimited || m.limit === undefined) return 0;
  if (m.limit === 0) return 100;
  return Math.min(100, Math.round((m.used / m.limit) * 100));
}

// planPrice is a plan's headline for a cycle: its price, pay as you
// sell's rate, or free.
export function planPrice(p: BillingPlan, cycle: Cycle, currency: string): { price: string; note: string } {
  if (p.kind === 'free') return { price: money(0, currency), note: 'للأبد' };
  if (p.kind === 'pays') {
    const rate = p.fee ? `${(p.fee.rate_bps / 100).toLocaleString('en-US')}%` : '';
    return { price: rate, note: 'من الطلبات المسلّمة فقط' };
  }
  const monthly = p.monthly_minor;
  const annual = p.annual_minor;
  if (cycle === 'annual') {
    if (annual === undefined) return { price: '—', note: 'غير متاحة سنويًا' };
    return { price: money(annual, currency), note: monthly !== undefined ? `سنويًا — بدل ${money(monthly * 12, currency)}` : 'سنويًا' };
  }
  return monthly !== undefined ? { price: money(monthly, currency), note: 'شهريًا' } : { price: '—', note: 'غير متاحة شهريًا' };
}

// sold reports whether a paid plan can be bought for cycle.
export function sold(p: BillingPlan, cycle: Cycle): boolean {
  if (p.kind !== 'subscription') return true;
  return (cycle === 'annual' ? p.annual_minor : p.monthly_minor) !== undefined;
}

// grantText is a grant in a comparison cell.
export function grantText(p: BillingPlan, f: BillingFeature): string {
  const g = p.grants.find((x) => x.feature === f.key);
  if (!g) return '—';
  if (f.kind === 'flag') return g.value > 0 ? '✓' : '—';
  if (g.unlimited) return 'بلا حد';
  return g.value > 0 ? count(g.value) : '—';
}

// planPoints are a plan card's lines: its counted grants, then the flags
// it has, at most five.
export function planPoints(p: BillingPlan, features: BillingFeature[]): string[] {
  const out: string[] = [];
  for (const f of features) {
    const g = p.grants.find((x) => x.feature === f.key);
    if (!g) continue;
    if (f.kind === 'flag') continue;
    if (g.unlimited) out.push(`${f.name_ar}: بلا حد`);
    else if (g.value > 0) out.push(`${f.name_ar}: ${count(g.value)}`);
  }
  for (const f of features) {
    const g = p.grants.find((x) => x.feature === f.key);
    if (f.kind === 'flag' && g && g.value > 0) out.push(f.name_ar);
  }
  return out.slice(0, 5);
}

// annualSaving is how many months of a monthly price a year saves.
export function annualSaving(p: BillingPlan): number {
  if (p.monthly_minor === undefined || p.annual_minor === undefined || p.monthly_minor === 0) return 0;
  return Math.round(12 - p.annual_minor / p.monthly_minor);
}

// The prepaid balance and the switch to pay as you sell (ADR-0084–0087).
export type BillingBalance = components['schemas']['BillingBalance'];
export type BalanceEntry = components['schemas']['BillingBalanceEntry'];
export type Collection = NonNullable<Billing['collection']>;

export const collectionLabel: Record<Collection, string> = { balance: 'رصيد مسبق', card: 'البطاقة المحفوظة' };

// rateText writes basis points as a percentage: 150 → "1.5%".
export const rateText = (bps: number) => `${String(bps / 100)}%`;

// feeOf is the fee of an order whose goods cost base, at bps, rounded
// half up as billing does (ADR-0048).
export const feeOf = (base: number, bps: number) => Math.floor((base * bps + 5000) / 10000);

// topUpAmounts are the amounts offered on the balance page: the minimum,
// then about 5/3 and 10/3 of it (300 → 300, 500, 1000), in whole tens.
export function topUpAmounts(minimum: number): number[] {
  const tens = (v: number) => Math.round(v / 1000) * 1000;
  return [minimum, Math.max(tens((minimum * 5) / 3), minimum), Math.max(tens((minimum * 10) / 3), minimum)].filter(
    (v, i, all) => all.indexOf(v) === i,
  );
}

// entryText is what a balance entry was, as the merchant reads it.
export function entryText(e: BalanceEntry): string {
  switch (e.kind) {
    case 'topup':
      return 'شحن بالبطاقة';
    case 'manual':
      return 'إضافة من فريق المنصة';
    case 'fee':
      return 'رسوم طلب مسلَّم';
    case 'fee_refund':
      return 'رد رسوم طلب مرتجع';
    case 'invoice':
      return `ضريبة الفاتورة${e.reference ? ` ${e.reference}` : ''}`;
    default:
      // A kind this dashboard does not know yet (an extensible list, ADR-0032).
      return 'حركة';
  }
}

// signedMoney writes an entry's amount with its sign: "+300 ج.م", "−12.75 ج.م".
export function signedMoney(minor: number, currency: string): string {
  return `${minor < 0 ? '−' : '+'}${money(Math.abs(minor), currency)}`;
}

// billingChanged tells the screens the store's billing moved (a switch,
// a change of the way to pay), so they read it again.
export const billingChanged = 'billing:changed';
