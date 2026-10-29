import type { components } from '../api/client';

import { toMajor, toMinor } from './amounts';
import { money } from './orders';

export type Coupon = components['schemas']['Coupon'];
export type CouponInput = components['schemas']['CouponInput'];
export type CouponKind = CouponInput['kind'];

// The coupon's code as the server keeps it: 3 to 32 of A-Z, 0-9, - and _.
export const codePattern = /^[A-Z0-9_-]{3,32}$/;
export const normalizeCode = (s: string) => s.trim().toUpperCase();

// randomCode is a fresh code, "MONA-7KQ2X" from the store's name when it
// is Latin, "SALE-…" otherwise.
export function randomCode(storeSlug: string): string {
  const prefix = (storeSlug.replace(/[^a-z0-9]/gi, '').slice(0, 6) || 'SALE').toUpperCase();
  const alphabet = 'ABCDEFGHJKMNPQRSTVWXYZ23456789';
  const bytes = crypto.getRandomValues(new Uint8Array(5));
  return `${prefix}-${Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')}`;
}

// The time-zone arithmetic for the dates a merchant types: in the store's
// zone, whatever the browser's.
function offsetMinutes(at: number, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).formatToParts(
    new Date(at),
  );
  const get = (t: Intl.DateTimeFormatPartTypes) => Number(parts.find((p) => p.type === t)?.value ?? 0);
  const asUTC = Date.UTC(get('year'), get('month') - 1, get('day'), get('hour'), get('minute'), get('second'));
  return Math.round((asUTC - at) / 60000);
}

// zonedToISO reads "2026-10-31T23:59" in timeZone as an instant.
export function zonedToISO(local: string, timeZone: string): string {
  const [d = '', t = '00:00'] = local.split('T');
  const [y = 0, mo = 1, da = 1] = d.split('-').map(Number);
  const [h = 0, mi = 0] = t.split(':').map(Number);
  const guess = Date.UTC(y, mo - 1, da, h, mi);
  const first = guess - offsetMinutes(guess, timeZone) * 60000;
  return new Date(guess - offsetMinutes(first, timeZone) * 60000).toISOString();
}

// isoToZoned is zonedToISO's inverse, for a datetime-local field.
export function isoToZoned(iso: string, timeZone: string): string {
  const at = Date.parse(iso);
  const local = new Date(at + offsetMinutes(at, timeZone) * 60000);
  return local.toISOString().slice(0, 16);
}

// Form is the editor's state: what the merchant types, as text.
export interface Form {
  code: string;
  kind: CouponKind;
  percent: string;
  amount: string; // major units
  min: string; // major units; empty is none
  maxUses: string; // empty is no bound
  perCustomer: string;
  startsAt: string; // datetime-local in the store's zone; empty is none
  endsAt: string;
  active: boolean;
}

export const emptyForm: Form = { code: '', kind: 'percent', percent: '10', amount: '', min: '', maxUses: '', perCustomer: '1', startsAt: '', endsAt: '', active: true };

export function toForm(c: Coupon, timeZone: string): Form {
  return {
    code: c.code,
    kind: c.kind,
    percent: c.percent === undefined ? '' : String(c.percent),
    amount: c.amount_minor === undefined ? '' : toMajor(c.amount_minor),
    min: c.min_subtotal_minor ? toMajor(c.min_subtotal_minor) : '',
    maxUses: c.max_uses === undefined ? '' : String(c.max_uses),
    perCustomer: c.max_per_customer === undefined ? '' : String(c.max_per_customer),
    startsAt: c.starts_at ? isoToZoned(c.starts_at, timeZone) : '',
    endsAt: c.ends_at ? isoToZoned(c.ends_at, timeZone) : '',
    active: c.active,
  };
}

const count = (s: string) => (/^\d+$/.test(s.trim()) ? Number(s.trim()) : undefined);

// toInput checks the form as the server would and builds the request;
// errors are by field, in the merchant's words.
export function toInput(f: Form, timeZone: string): { input?: CouponInput; errors: Record<string, string> } {
  const errors: Record<string, string> = {};
  const code = normalizeCode(f.code);
  if (!codePattern.test(code)) errors['code'] = 'من 3 إلى 32 حرفًا إنجليزيًا أو رقمًا أو - أو _.';
  const input: CouponInput = { code, kind: f.kind, active: f.active };
  if (f.kind === 'percent') {
    const p = count(f.percent);
    if (p === undefined || p < 1 || p > 100) errors['percent'] = 'من 1 إلى 100.';
    else input.percent = p;
  }
  if (f.kind === 'fixed') {
    const a = toMinor(f.amount);
    if (a === undefined || a < 1) errors['amount_minor'] = 'أكثر من صفر.';
    else input.amount_minor = a;
  }
  if (f.min.trim()) {
    const m = toMinor(f.min);
    if (m === undefined) errors['min_subtotal_minor'] = 'مبلغ صحيح أو اتركه فارغًا.';
    else input.min_subtotal_minor = m;
  }
  for (const [field, text, key] of [
    ['max_uses', f.maxUses, 'max_uses'],
    ['max_per_customer', f.perCustomer, 'max_per_customer'],
  ] as const) {
    if (!text.trim()) continue;
    const n = count(text);
    if (n === undefined || n < 1) errors[field] = 'عدد من 1 فأكثر، أو اتركه فارغًا.';
    else input[key] = n;
  }
  if (f.startsAt) input.starts_at = zonedToISO(f.startsAt, timeZone);
  if (f.endsAt) input.ends_at = zonedToISO(f.endsAt, timeZone);
  if (input.starts_at && input.ends_at && input.ends_at <= input.starts_at) errors['ends_at'] = 'بعد موعد البدء.';
  return Object.keys(errors).length > 0 ? { errors } : { input, errors };
}

// serverErrors puts the API's refusals on the fields they concern.
export function serverErrors(problem: { code?: string; errors?: { field: string }[] }): Record<string, string> {
  if (problem.code === 'coupon_code_taken') return { code: 'كوبون آخر يحمل هذا الكود.' };
  if (problem.code === 'coupon_code_in_use') return { code: 'طلبات استخدمت هذا الكوبون، فلا يتغير كوده: أنشئ كوبونًا جديدًا.' };
  const words: Record<string, string> = {
    code: 'من 3 إلى 32 حرفًا إنجليزيًا أو رقمًا أو - أو _.',
    percent: 'من 1 إلى 100.',
    amount_minor: 'أكثر من صفر.',
    min_subtotal_minor: 'مبلغ صحيح أو اتركه فارغًا.',
    max_uses: 'عدد من 1 فأكثر، أو اتركه فارغًا.',
    max_per_customer: 'عدد من 1 فأكثر، أو اتركه فارغًا.',
    ends_at: 'بعد موعد البدء.',
  };
  return Object.fromEntries((problem.errors ?? []).map((e) => [e.field, words[e.field] ?? 'قيمة غير صالحة.']));
}

const day = (iso: string, timeZone: string) => new Date(iso).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', timeZone });

// discountLabel is the list's "الخصم": "10% من المنتجات", "50 ج.م".
export function discountLabel(c: Pick<CouponInput, 'kind' | 'percent' | 'amount_minor'>, currency: string): string {
  if (c.kind === 'percent') return `${String(c.percent ?? 0)}% من المنتجات`;
  if (c.kind === 'fixed') return money(c.amount_minor ?? 0, currency);
  return 'شحن مجاني';
}

// conditionsLabel is the list's "الشروط".
export function conditionsLabel(c: CouponInput, currency: string): string {
  const parts: string[] = [];
  if (c.min_subtotal_minor) parts.push(`من ${money(c.min_subtotal_minor, currency)}`);
  if (c.max_per_customer === 1) parts.push('مرة لكل عميل');
  else if (c.max_per_customer) parts.push(`${String(c.max_per_customer)} مرات لكل عميل`);
  return parts.length > 0 ? parts.join(' · ') : '—';
}

// validityLabel is the list's "الصلاحية".
export function validityLabel(c: CouponInput, timeZone: string, now = Date.now()): string {
  if (c.ends_at && Date.parse(c.ends_at) < now) return `انتهى ${day(c.ends_at, timeZone)}`;
  if (c.starts_at && c.ends_at) return `${day(c.starts_at, timeZone)} – ${day(c.ends_at, timeZone)}`;
  if (c.ends_at) return `حتى ${day(c.ends_at, timeZone)}`;
  if (c.starts_at) return `من ${day(c.starts_at, timeZone)}`;
  return 'دائم';
}

// couponState is the list's status pill.
export function couponState(c: Coupon, timeZone: string, now = Date.now()): { label: string; cls: string } {
  if (!c.active) return { label: 'متوقف', cls: 'c-off' };
  if (c.ends_at && Date.parse(c.ends_at) < now) return { label: 'منتهٍ', cls: 'c-ended' };
  if (c.max_uses !== undefined && c.uses >= c.max_uses) return { label: 'استُنفد', cls: 'c-ended' };
  if (c.starts_at && Date.parse(c.starts_at) > now) return { label: `يبدأ ${day(c.starts_at, timeZone)}`, cls: 'c-soon' };
  return { label: 'مفعّل', cls: 'c-on' };
}

// summaryLines is the editor's "الملخص", as the merchant reads the terms.
export function summaryLines(f: Form, currency: string, timeZone: string): string[] {
  const lines: string[] = [];
  const at = (local: string) => day(zonedToISO(local, timeZone), timeZone);
  if (f.kind === 'percent') lines.push(`خصم ${f.percent || '0'}% على المنتجات`);
  if (f.kind === 'fixed') lines.push(`خصم ${money(toMinor(f.amount) ?? 0, currency)} على المنتجات`);
  if (f.kind === 'free_shipping') lines.push('شحن مجاني');
  const min = toMinor(f.min);
  lines.push(min ? `للطلبات التي منتجاتها ${money(min, currency)} أو أكثر` : 'لكل الطلبات');
  const uses = count(f.maxUses);
  lines.push(uses ? `${String(uses)} استخدامًا كحد أقصى` : 'بلا حد لعدد الاستخدامات');
  const per = count(f.perCustomer);
  if (per === 1) lines.push('مرة واحدة لكل عميل');
  else if (per) lines.push(`${String(per)} مرات لكل عميل`);
  if (f.startsAt && f.endsAt) lines.push(`من ${at(f.startsAt)} حتى ${at(f.endsAt)}`);
  else if (f.endsAt) lines.push(`حتى ${at(f.endsAt)}`);
  else if (f.startsAt) lines.push(`يبدأ ${at(f.startsAt)}`);
  else lines.push('بلا تاريخ انتهاء');
  return lines;
}

// effectOn is what the coupon takes off an order whose items cost
// subtotal (the shopper's preview).
export function effectOn(f: Form, subtotal: number, currency: string): string {
  if (f.kind === 'free_shipping') return 'شحن مجاني';
  const off = f.kind === 'percent' ? Math.round((subtotal * (count(f.percent) ?? 0)) / 100) : Math.min(subtotal, toMinor(f.amount) ?? 0);
  return `−${money(off, currency)}`;
}
