import type { components } from '../api/client';
import { bpToPercent, percentToBp, toMajor, toMinor } from '../lib/amounts';

export type Plan = components['schemas']['AdminPlan'];
export type PlanInput = components['schemas']['AdminPlanInput'];
export type Feature = components['schemas']['AdminFeature'];
export type Kind = Plan['kind'];

export const currencies = ['EGP', 'SAR', 'AED'] as const;
export const cycles = ['monthly', 'annual'] as const;
type Currency = (typeof currencies)[number];
type Cycle = (typeof cycles)[number];

export const kindLabel: Record<Kind, string> = { free: 'مجانية', pays: 'نسبة من المبيعات', subscription: 'اشتراك', agency: 'وكالة' };
export const featureKindLabel: Record<Feature['kind'], string> = { flag: 'تشغيل/إيقاف', count: 'عدد', monthly: 'شهري' };

// Draft is the editor's form: text as typed, read back by toInput.
export interface Draft {
  code: string;
  kind: Kind;
  name_ar: string;
  name_en: string;
  trial_days: string;
  sort: string;
  public: boolean;
  prices: Record<string, string>; // "EGP-monthly" -> "649.00"
  fees: Record<string, { rate: string; min: string; max: string }>; // by currency
  grants: Record<string, { value: string; unlimited: boolean }>; // by feature; empty value: not granted
}

export const priceKey = (c: Currency, cy: Cycle) => `${c}-${cy}`;

// draftFrom is the next version's form, from the version on sale (or a
// new plan of kind when there is none).
export function draftFrom(p: Plan | undefined, kind: Kind = 'subscription'): Draft {
  const d: Draft = {
    code: p?.code ?? '',
    kind: p?.kind ?? kind,
    name_ar: p?.name_ar ?? '',
    name_en: p?.name_en ?? '',
    trial_days: String(p?.trial_days ?? 0),
    sort: String(p?.sort ?? 0),
    public: p?.public ?? true,
    prices: {},
    fees: {},
    grants: {},
  };
  for (const pr of p?.prices ?? []) d.prices[priceKey(pr.currency, pr.cycle)] = toMajor(pr.amount_minor);
  for (const f of p?.fees ?? []) {
    d.fees[f.currency] = { rate: bpToPercent(f.rate_bps), min: toMajor(f.min_minor), max: f.max_minor === undefined ? '' : toMajor(f.max_minor) };
  }
  for (const g of p?.grants ?? []) d.grants[g.feature] = { value: g.unlimited ? '' : String(g.value ?? 0), unlimited: g.unlimited };
  return d;
}

const intOf = (s: string) => (/^\d+$/.test(s.trim()) ? Number(s.trim()) : undefined);

// toInput reads a draft into the publish request, or says what is wrong
// with it (the server checks the rest: a price in every currency, ...).
export function toInput(d: Draft, reason: string): PlanInput | string {
  const trial = intOf(d.trial_days);
  const sort = intOf(d.sort);
  if (trial === undefined) return 'أيام التجربة رقم صحيح';
  if (sort === undefined) return 'الترتيب رقم صحيح';
  const input: PlanInput = { code: d.code.trim(), kind: d.kind, name_ar: d.name_ar.trim(), name_en: d.name_en.trim(), trial_days: trial, sort, public: d.public, grants: [], reason };
  if (d.kind === 'subscription' || d.kind === 'agency') {
    const prices: NonNullable<PlanInput['prices']> = [];
    for (const c of currencies) {
      for (const cy of cycles) {
        const text = d.prices[priceKey(c, cy)]?.trim() ?? '';
        if (!text) continue;
        const minor = toMinor(text);
        if (minor === undefined) return `سعر ${c} ${cy === 'monthly' ? 'الشهري' : 'السنوي'} غير صحيح`;
        prices.push({ currency: c, cycle: cy, amount_minor: minor });
      }
    }
    input.prices = prices;
  }
  if (d.kind === 'pays') {
    const fees: NonNullable<PlanInput['fees']> = [];
    for (const c of currencies) {
      const f = d.fees[c];
      if (!f?.rate.trim()) continue;
      const rate = percentToBp(f.rate);
      const min = f.min.trim() ? toMinor(f.min) : 0;
      const max = f.max.trim() ? toMinor(f.max) : undefined;
      if (rate === undefined || min === undefined || (f.max.trim() && max === undefined)) return `رسوم ${c} غير صحيحة`;
      fees.push(max === undefined ? { currency: c, rate_bps: rate, min_minor: min } : { currency: c, rate_bps: rate, min_minor: min, max_minor: max });
    }
    input.fees = fees;
  }
  for (const [feature, g] of Object.entries(d.grants)) {
    if (g.unlimited) {
      input.grants.push({ feature, unlimited: true });
      continue;
    }
    if (!g.value.trim()) continue;
    const value = intOf(g.value);
    if (value === undefined) return `قيمة «${feature}» رقم صحيح`;
    input.grants.push({ feature, unlimited: false, value });
  }
  return input;
}

// byCode groups the catalog's versions by plan, newest first, the plan on
// sale (or its newest version) as each group's head.
export function byCode(plans: Plan[]): { code: string; head: Plan; versions: Plan[] }[] {
  const groups = new Map<string, Plan[]>();
  for (const p of plans) groups.set(p.code, [...(groups.get(p.code) ?? []), p]);
  return [...groups.entries()].flatMap(([code, versions]) => {
    const sorted = [...versions].sort((a, b) => b.version - a.version);
    const head = sorted.find((v) => v.active) ?? sorted[0];
    return head ? [{ code, head, versions: sorted }] : [];
  });
}
