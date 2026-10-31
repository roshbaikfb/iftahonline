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
