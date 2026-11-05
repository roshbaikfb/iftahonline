import type { components } from '../api/client';
import { bpToPercent, percentToBp, toMajor, toMinor } from '../lib/amounts';
import { money } from '../lib/orders';

export type Plan = components['schemas']['AdminPlan'];
export type PlanInput = components['schemas']['AdminPlanInput'];
export type Feature = components['schemas']['AdminFeature'];
export type Kind = Plan['kind'];

// currencies are billing's: the pound and the dollar on sale, then the
// riyal and the dirham that older accounts are still billed in (ADR-0090).
export const currencies = ['EGP', 'USD', 'SAR', 'AED'] as const;
export const cycles = ['monthly', 'annual'] as const;
type Currency = (typeof currencies)[number];
type Cycle = (typeof cycles)[number];

export const kindLabel: Record<Kind, string> = { free: 'مجانية', pays: 'نسبة من المبيعات', subscription: 'اشتراك', agency: 'وكالة' };
export const featureKindLabel: Record<Feature['kind'], string> = { flag: 'تشغيل/إيقاف', count: 'عدد', monthly: 'في الشهر' };

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
  fees: Record<string, { rate: string; fixed: string; min: string; max: string }>; // by currency
  grants: Record<string, { value: string; unlimited: boolean }>; // by feature; empty value: not granted
}

export const priceKey = (c: string, cy: Cycle) => `${c}-${cy}`;

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
    d.fees[f.currency] = {
      rate: bpToPercent(f.rate_bps),
      fixed: f.fixed_minor ? toMajor(f.fixed_minor) : '',
      min: toMajor(f.min_minor),
      max: f.max_minor === undefined ? '' : toMajor(f.max_minor),
    };
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
      const fixed = f.fixed.trim() ? toMinor(f.fixed) : 0;
      const min = f.min.trim() ? toMinor(f.min) : 0;
      const max = f.max.trim() ? toMinor(f.max) : undefined;
      if (rate === undefined || fixed === undefined || min === undefined || (f.max.trim() && max === undefined)) return `رسوم ${c} غير صحيحة`;
      const fee = { currency: c, rate_bps: rate, fixed_minor: fixed, min_minor: min };
      fees.push(max === undefined ? fee : { ...fee, max_minor: max });
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

// currencyName is every currency's name.
export const currencyName: Record<Currency, string> = {
  EGP: 'الجنيه المصري',
  SAR: 'الريال السعودي',
  AED: 'الدرهم الإماراتي',
  USD: 'الدولار الأمريكي',
};

// paidKind says whether a kind sells at a price (and so has prices).
export const paidKind = (k: Kind) => k === 'subscription' || k === 'agency';

// saving is what an annual price saves on twelve monthly ones, in whole
// months ("يوفّر شهرين"); empty when it saves less than half a month.
export function saving(monthly: number | undefined, annual: number | undefined): string {
  if (!monthly || !annual || monthly <= 0) return '';
  const months = Math.round((monthly * 12 - annual) / monthly);
  if (months < 1) return '';
  if (months === 1) return 'يوفّر شهرًا';
  if (months === 2) return 'يوفّر شهرين';
  return months <= 10 ? `يوفّر ${String(months)} أشهر` : `يوفّر ${String(months)} شهرًا`;
}

const priceOf = (p: Plan, c: Currency, cy: Cycle) => p.prices.find((x) => x.currency === c && x.cycle === cy)?.amount_minor;

// headline is a plan card's price line and the line under it.
export function headline(p: Plan): { price: string; per: string; sub: string } {
  if (p.kind === 'pays') {
    const f = p.fees.find((x) => x.currency === 'EGP') ?? p.fees[0];
    if (!f) return { price: '—', per: '', sub: 'لم تُضبط النسبة' };
    const min = f.min_minor;
    const bounds =
      min === 0 && f.max_minor === undefined
        ? 'بلا حد أدنى ولا أقصى'
        : [min > 0 ? `حد أدنى ${money(min, f.currency)}` : '', f.max_minor !== undefined ? `حد أقصى ${money(f.max_minor, f.currency)}` : ''].filter(Boolean).join(' · ');
    return { price: `${bpToPercent(f.rate_bps)}%`, per: 'من كل طلب مسلّم', sub: bounds };
  }
  if (!paidKind(p.kind)) return { price: money(0, 'EGP'), per: '', sub: 'بلا اشتراك' };
  const m = priceOf(p, 'EGP', 'monthly');
  const y = priceOf(p, 'EGP', 'annual');
  if (m === undefined) return { price: '—', per: '', sub: 'لا سعر بالجنيه' };
  const annual = y === undefined ? '' : `${money(y, 'EGP')} سنويًا`;
  const extra = p.kind === 'agency' ? 'سعر واحد لكل متاجر الوكالة' : [annual, saving(m, y)].filter(Boolean).join(' · ');
  return { price: money(m, 'EGP'), per: '/ شهر', sub: extra };
}

// otherPrices are a card's monthly prices outside Egypt, for its title.
export function otherPrices(p: Plan): string {
  return currencies
    .filter((c) => c !== 'EGP')
    .flatMap((c) => {
      const v = priceOf(p, c, 'monthly');
      return v === undefined ? [] : [`${money(v, c)} شهريًا`];
    })
    .join(' · ');
}

// facts are a card's first limits (by the catalog's order) and its trial.
export function facts(p: Plan, features: Feature[], n = 3): string[] {
  const out: string[] = [];
  for (const f of [...features].sort((a, b) => a.sort - b.sort)) {
    if (out.length === n - 1) break;
    if (f.kind === 'flag') continue;
    const g = p.grants.find((x) => x.feature === f.key);
    if (!g) continue;
    out.push(`${f.name_ar}: ${g.unlimited ? 'بلا حد' : (g.value ?? 0).toLocaleString('en-US')}`);
  }
  out.push(p.trial_days > 0 ? `تجربة ${String(p.trial_days)} يومًا` : 'بلا تجربة');
  return out;
}

// subscribersOf are the stores on any version of a plan: all of them
// renew into its next version.
export const subscribersOf = (versions: Plan[]) => versions.reduce((n, v) => n + v.subscribers, 0);

// stores writes a count of subscribers in Arabic.
export function stores(n: number): string {
  if (n === 0) return 'لا مشتركين';
  if (n === 1) return 'مشترك واحد';
  if (n === 2) return 'مشتركان';
  const text = n.toLocaleString('en-US');
  return n <= 10 ? `${text} مشتركين` : `${text} مشتركًا`;
}
