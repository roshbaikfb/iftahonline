import type { components } from '../api/client';
import { money } from './orders';

export type ProfitReport = components['schemas']['ProfitReport'];
export type ProfitFigures = components['schemas']['ProfitFigures'];
export type ProfitBy = ProfitReport['by'];
export type ProfitSettings = components['schemas']['ProfitSettings'];
export type AdSpend = components['schemas']['AdSpend'];
export type AdPlatform = AdSpend['platform'];

// isoDay is a date as the API's days are written (the store's day; the
// merchant's browser stands for it).
export function isoDay(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${String(d.getFullYear())}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// lastDays is the range of the last n days, today included.
export function lastDays(n: number, today = new Date()): { from: string; to: string } {
  const from = new Date(today);
  from.setDate(from.getDate() - (n - 1));
  return { from: isoDay(from), to: isoDay(today) };
}

// signed is an amount with a minus sign for a cost or a loss.
export function signed(minor: number, currency: string): string {
  return minor < 0 ? `−${money(-minor, currency)}` : money(minor, currency);
}

// income is what came in: the items, and what customers paid for delivery.
export const income = (f: ProfitFigures) => f.revenue_minor + f.delivery_paid_minor;

// margin is profit over income, rounded to a whole percent ("—" with none).
export function margin(f: ProfitFigures): string {
  const i = income(f);
  return i > 0 ? `${String(Math.round((f.profit_minor / i) * 100))}%` : '—';
}

export interface FlowRow {
  label: string;
  minor: number; // costs negative
  kind: 'in' | 'out' | 'net';
}

// flow is «من أين جاء الربح» (the approved mockup): what came in, each
// cost, then what is left.
export function flow(f: ProfitFigures): FlowRow[] {
  return [
    { label: 'المنتجات (بعد الخصومات)', minor: f.revenue_minor, kind: 'in' },
    { label: 'ما دفعه العملاء للتوصيل', minor: f.delivery_paid_minor, kind: 'in' },
    { label: 'تكلفة المنتجات', minor: -f.product_cost_minor, kind: 'out' },
    { label: 'الشحن', minor: -f.shipping_cost_minor, kind: 'out' },
    { label: 'رسوم الدفع عند الاستلام', minor: -f.cod_fees_minor, kind: 'out' },
    { label: 'المرتجعات', minor: -f.return_cost_minor, kind: 'out' },
    { label: 'الإعلانات', minor: -f.ad_spend_minor, kind: 'out' },
    { label: 'صافي الربح', minor: f.profit_minor, kind: 'net' },
  ];
}

export const byLabel: Record<ProfitBy, { tab: string; head: string; foot: string }> = {
  product: {
    tab: 'المنتجات',
    head: 'المنتج',
    foot: 'شحن الطلب ومرتجعاته يُقسَم على منتجاته بنسبة إيراد كل منتج. الإعلانات لا تُنسب لمنتج، فتظهر في الحملات فقط.',
  },
  campaign: {
    tab: 'الحملات',
    head: 'الحملة',
    foot: 'الطلب يُنسب للحملة من رابط الإعلان (utm_campaign)، والإنفاق من إدخالك باسم الحملة نفسه.',
  },
  governorate: {
    tab: 'المحافظات',
    head: 'المحافظة',
    foot: 'المحافظات عالية المرتجعات مرشّحة لقاعدة «ادفع التوصيل مقدمًا» في حماية الدفع عند الاستلام.',
  },
};

// returnRate is a row's returned share of its settled orders, 0–1.
export function returnRate(f: ProfitFigures): number {
  const settled = f.delivered + f.returned;
  return settled > 0 ? f.returned / settled : 0;
}

export const platformLabel: Record<AdPlatform, string> = {
  meta: 'Meta',
  tiktok: 'TikTok',
  google: 'Google',
  snap: 'Snapchat',
  other: 'أخرى',
};
