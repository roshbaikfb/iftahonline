import type { components } from '../api/client';

export type GrowthData = components['schemas']['AdminGrowth'];
export type Currency = components['schemas']['AdminCollectedSeries']['currency'];
export type Section = GrowthData['errors'][number];

// The console's growth page (ADR-0081): what each chart shows, from the
// server's figures, in one currency at a time (never converted).

export const currencyNames: Record<Currency, string> = { EGP: 'جنيه مصري', SAR: 'ريال سعودي', AED: 'درهم إماراتي', USD: 'دولار أمريكي' };

// monthName is a month's Arabic name: «نوفمبر»; with the year: «نوفمبر 2026».
export function monthName(iso: string, year = false): string {
  return new Date(`${iso}T00:00:00Z`).toLocaleDateString('ar-EG-u-nu-latn', { month: 'long', ...(year ? { year: 'numeric' } : {}), timeZone: 'UTC' });
}

// dayName is «4 نوفمبر».
export function dayName(iso: string): string {
  return new Date(`${iso}T00:00:00Z`).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', timeZone: 'UTC' });
}

// nextMonth is the first day of the month after iso's.
export function nextMonth(iso: string): string {
  const d = new Date(`${iso}T00:00:00Z`);
  return new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1)).toISOString().slice(0, 10);
}

// shownCurrencies are those with any figure, EGP first; EGP alone when
// nothing has any.
export function shownCurrencies(g: GrowthData): Currency[] {
  const seen = new Set<Currency>();
  for (const s of g.collected ?? []) seen.add(s.currency);
  for (const d of g.mrr ?? []) if (d.mrr_minor > 0) seen.add(d.currency);
  for (const p of g.plans ?? []) seen.add(p.currency);
  const out = (['EGP', 'SAR', 'AED'] as const).filter((c) => seen.has(c));
  return out.length > 0 ? out : ['EGP'];
}

// collectedIn is the currency's amount for each of the 12 months.
export function collectedIn(g: GrowthData, c: Currency): number[] {
  const series = g.collected?.find((s) => s.currency === c);
  return g.months.map((m) => series?.months.find((x) => x.month === m)?.amount_minor ?? 0);
}

// mrrIn are the currency's daily readings, oldest first.
export function mrrIn(g: GrowthData, c: Currency): { day: string; minor: number }[] {
  return (g.mrr ?? []).filter((d) => d.currency === c).map((d) => ({ day: d.day, minor: d.mrr_minor }));
}

// change is the move from the first reading to the last, as a signed
// percent with one decimal («+4.6%»); none without two readings or a base.
export function change(points: { minor: number }[]): string | null {
  const first = points[0]?.minor ?? 0;
  const last = points.at(-1)?.minor ?? 0;
  if (points.length < 2 || first <= 0) return null;
  const pct = Math.round(((last - first) / first) * 1000) / 10;
  return `${pct > 0 ? '+' : ''}${String(pct)}%`;
}

// percent is part of whole, whole percent; 0 of none.
export const percent = (part: number, whole: number) => (whole > 0 ? Math.round((part / whole) * 100) : 0);

// conversionRows are the last six months before this one: the newest
// months are still in their trials, so this one is left out.
export function conversionRows(g: GrowthData) {
  const months = (g.stores ?? []).slice(0, -1).slice(-6);
  return months.map((s) => ({ month: s.month, created: s.created, converted: s.converted, pct: percent(s.converted, s.created) }));
}

// plansIn are the currency's plans today, each with its share of the
// currency's recurring revenue.
export function plansIn(g: GrowthData, c: Currency) {
  const plans = (g.plans ?? []).filter((p) => p.currency === c);
  const total = plans.reduce((n, p) => n + p.mrr_minor, 0);
  return plans.map((p) => ({ ...p, share: percent(p.mrr_minor, total) }));
}

// churnStarts is the month whose figure comes first, when none has yet:
// the first month the recording saw from its start.
export function churnStarts(g: GrowthData): string | null {
  const first = g.mrr?.[0]?.day;
  if (!first) return null;
  const month = `${first.slice(0, 7)}-01`;
  return Number(first.slice(8, 10)) <= 3 ? month : nextMonth(month);
}

// line is an SVG path through points scaled into w×h, with a margin so
// the top and bottom readings stay inside.
export function line(values: number[], w: number, h: number): string {
  if (values.length === 0) return '';
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = hi - lo || 1;
  const pad = h * 0.1;
  const x = (i: number) => (values.length === 1 ? w / 2 : (i / (values.length - 1)) * w);
  const y = (v: number) => h - pad - ((v - lo) / span) * (h - 2 * pad);
  return values.map((v, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)} ${y(v).toFixed(1)}`).join(' ');
}
