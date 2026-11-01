import type { components } from '../api/client';

import { toMajor, toMinor } from './amounts';
import type { Product, ProductStatus } from './products';

type ProductInput = components['schemas']['ProductInput'];
type VariantInput = components['schemas']['VariantInput'];

// Limits of the catalog (ADR-0011), checked here so the merchant sees
// them before saving.
export const maxOptions = 3;
export const maxValues = 50;
export const maxVariants = 100;

// OptionDraft is an option being edited. uid follows it through renames
// and removals of others (never sent). colors is null for an option that
// is not a colour; otherwise one "#rrggbb" or "" (guess from the name)
// per value (ADR-0035).
export interface OptionDraft {
  uid: string;
  name: string;
  values: string[];
  colors: string[] | null;
}

// VariantDraft is one row of the variants grid. Amounts are kept as the
// merchant typed them. A variant with an id keeps its stock, which then
// changes through the inventory endpoint (onHandWas tells whether it did).
export interface VariantDraft {
  id?: string;
  values: string[];
  price: string;
  compare: string;
  // cost is what the variant costs the merchant (ADR-0068); '' is not
  // known. Shoppers never see it.
  cost?: string;
  sku: string;
  stock: string;
  tracked: boolean;
  onHandWas?: number;
  hidden: boolean;
}

export interface Draft {
  title: string;
  description: string;
  status: ProductStatus;
  options: OptionDraft[];
  // gridOptions are the options the variants rows were made from: they
  // lag behind options while those would make too many variants.
  gridOptions: OptionDraft[];
  variants: VariantDraft[];
  categoryIds: string[];
  sizeChartId: string;
}

let uids = 0;

// newOption is an empty option with its own uid.
export function newOption(): OptionDraft {
  uids += 1;
  return { uid: `o${String(uids)}`, name: '', values: [], colors: null };
}

export function emptyDraft(): Draft {
  return {
    title: '', description: '', status: 'draft', options: [], gridOptions: [], categoryIds: [], sizeChartId: '',
    variants: [{ values: [], price: '', compare: '', sku: '', stock: '0', tracked: true, hidden: false }],
  };
}

// draftOf reads a saved product into the editor.
export function draftOf(p: Product): Draft {
  const options = p.options.map((o) => ({ ...newOption(), name: o.name, values: [...o.values], colors: o.colors ? [...o.colors] : null }));
  return {
    title: p.title,
    description: p.description,
    status: p.status,
    options,
    gridOptions: options,
    variants: p.variants.map((v) => ({
      id: v.id,
      values: v.option_values,
      price: toMajor(v.price_minor),
      compare: v.compare_at_minor === undefined ? '' : toMajor(v.compare_at_minor),
      cost: v.cost_minor === undefined ? '' : toMajor(v.cost_minor),
      sku: v.sku ?? '',
      stock: String(v.stock.on_hand),
      tracked: v.stock.tracked,
      onHandWas: v.stock.on_hand,
      hidden: v.hidden ?? false,
    })),
    categoryIds: p.category_ids,
    sizeChartId: p.size_chart_id ?? '',
  };
}

// combinations is every choice of one value per option, in option order.
export function combinations(options: OptionDraft[]): string[][] {
  return options.reduce<string[][]>((acc, o) => acc.flatMap((head) => o.values.map((v) => [...head, v])), [[]]);
}

const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

// regenerate rebuilds the variants grid after the options went from prev
// to next: one row per combination. A row keeps its ID, prices and stock
// while its values are still there; an option added since takes its
// first value (so adding «اللون» to a product sold by size keeps every
// size, and its stock, as the first colour). New rows start from the
// first row's price. A removed value takes its rows (and their stock).
export function regenerate(prev: OptionDraft[], next: OptionDraft[], rows: VariantDraft[]): VariantDraft[] {
  const was = prev.filter((o) => o.values.length > 0);
  const usable = next.filter((o) => o.values.length > 0);
  const known = rows.map((r) => new Map(was.map((o, i) => [o.uid, r.values[i] ?? ''])));
  const used = new Set<number>();
  const model = rows[0];
  return combinations(usable).map((values) => {
    const at = rows.findIndex(
      (_, r) =>
        !used.has(r) &&
        usable.every((o, i) => {
          const had = known[r]?.get(o.uid);
          const v = values[i] ?? '';
          return had === undefined ? same(v, o.values[0] ?? '') : same(had, v);
        }),
    );
    const kept = rows[at];
    if (kept) {
      used.add(at);
      return { ...kept, values };
    }
    return { values, price: model?.price ?? '', compare: model?.compare ?? '', sku: '', stock: '0', tracked: true, hidden: false };
  });
}

// variantCount is how many rows the options would make.
export function variantCount(options: OptionDraft[]): number {
  return options.filter((o) => o.values.length > 0).reduce((n, o) => n * o.values.length, 1);
}

export interface Built {
  body: ProductInput;
  // stock to set on existing variants after saving, by variant ID
  stock: { id: string; onHand: number; tracked: boolean }[];
  errors: Record<string, string>; // field → message, for the merchant
}

const digits = /[٠-٩]/g;

function toCount(text: string): number | undefined {
  const t = text.trim().replace(digits, (d) => String(d.charCodeAt(0) - 0x0660));
  return /^\d{1,7}$/.test(t) && Number(t) <= 1_000_000 ? Number(t) : undefined;
}

// build checks the draft and turns it into the request body, reporting
// every field the merchant must fix at once.
export function build(d: Draft): Built {
  const errors: Record<string, string> = {};
  if (!d.title.trim()) errors['title'] = 'اكتب اسم المنتج';
  if (d.variants.length > maxVariants) errors['variants'] = `المنتج يصل إلى ${String(maxVariants)} نوعًا. قلّل القيم.`;
  d.options.forEach((o, i) => {
    if (!o.name.trim()) errors[`options.${String(i)}`] = 'اكتب اسم الخيار';
    else if (o.values.length === 0) errors[`options.${String(i)}`] = 'أضف قيمة واحدة على الأقل';
  });
  const variants: VariantInput[] = [];
  const stock: Built['stock'] = [];
  d.variants.forEach((v, i) => {
    const at = `variants.${String(i)}`;
    const price = toMinor(v.price);
    if (price === undefined) errors[`${at}.price`] = 'اكتب السعر';
    const compare = v.compare.trim() === '' ? undefined : toMinor(v.compare);
    if (v.compare.trim() !== '' && (compare === undefined || price === undefined || compare <= price)) {
      errors[`${at}.compare`] = 'السعر قبل الخصم أكبر من السعر';
    }
    const costText = (v.cost ?? '').trim();
    const cost = costText === '' ? undefined : toMinor(costText);
    if (costText !== '' && cost === undefined) errors[`${at}.cost`] = 'اكتب التكلفة رقمًا';
    const onHand = toCount(v.stock);
    if (v.tracked && onHand === undefined) errors[`${at}.stock`] = 'اكتب عددًا صحيحًا';
    // tracked and on_hand only count for a new variant; the server ignores
    // them on an existing one.
    const out: VariantInput = { price_minor: price ?? 0, option_values: v.values, hidden: v.hidden, tracked: v.tracked, on_hand: onHand ?? 0 };
    if (compare !== undefined) out.compare_at_minor = compare;
    if (cost !== undefined) out.cost_minor = cost;
    if (v.sku.trim()) out.sku = v.sku.trim();
    if (v.id) {
      out.id = v.id;
      if (onHand !== undefined && onHand !== v.onHandWas) stock.push({ id: v.id, onHand, tracked: v.tracked });
    }
    variants.push(out);
  });
  const body: ProductInput = {
    title: d.title.trim(),
    description: d.description.trim(),
    status: d.status,
    options: d.options.map((o) => (o.colors ? { name: o.name.trim(), values: o.values, colors: o.colors } : { name: o.name.trim(), values: o.values })),
    variants,
    category_ids: d.categoryIds,
  };
  if (d.sizeChartId) body.size_chart_id = d.sizeChartId;
  return { body, stock, errors };
}

// Colour options and colour names, as the storefront reads them
// (storefront/internal/web/colors.go): an option named like a colour
// starts as a colour option, and a value's swatch is guessed from its
// name until the merchant picks one.
const colourOptionNames = new Set(['اللون', 'لون', 'الالوان', 'الوان', 'color', 'colour', 'colors']);

const named: Record<string, string> = {
  اسود: '#111827', black: '#111827', ابيض: '#f8fafc', white: '#f8fafc', احمر: '#dc2626', red: '#dc2626',
  ازرق: '#2563eb', blue: '#2563eb', كحلي: '#1e3a5f', navy: '#1e3a5f', اخضر: '#16a34a', green: '#16a34a',
  زيتي: '#4d5d2a', olive: '#4d5d2a', رمادي: '#9ca3af', gray: '#9ca3af', grey: '#9ca3af', بني: '#7c4a2d', brown: '#7c4a2d',
  بيج: '#e8d8bd', beige: '#e8d8bd', وردي: '#f472b6', بمبي: '#f472b6', pink: '#f472b6', بنفسجي: '#7c3aed', purple: '#7c3aed',
  اصفر: '#facc15', yellow: '#facc15', برتقالي: '#f97316', orange: '#f97316', ذهبي: '#d4af37', gold: '#d4af37',
  فضي: '#c0c0c0', silver: '#c0c0c0', سماوي: '#7dd3fc', لبني: '#7dd3fc', sky: '#7dd3fc', نبيتي: '#7f1d1d', عنابي: '#7f1d1d',
  maroon: '#7f1d1d', burgundy: '#7f1d1d', كريمي: '#fdf6e3', cream: '#fdf6e3', موف: '#b784a7', mauve: '#b784a7',
  فيروزي: '#14b8a6', turquoise: '#14b8a6', teal: '#14b8a6',
};

// colourKey matches names however they are written: "أبيض" and "ابيض".
function colourKey(s: string): string {
  return s
    .trim()
    .toLowerCase()
    .replace(/[ً-ْـ]/g, '')
    .replace(/[أإآ]/g, 'ا')
    .replace(/ى/g, 'ي')
    .replace(/ة/g, 'ه');
}

export function isColourName(optionName: string): boolean {
  return colourOptionNames.has(colourKey(optionName));
}

// guessColour is the swatch the shop shows for a value without a set
// colour, or "" when the name is not a known colour.
export function guessColour(value: string): string {
  return named[colourKey(value)] ?? '';
}

// swatchOf is the colour a value shows: the one set, or the name's guess
// ("" when neither).
export function swatchOf(o: OptionDraft, i: number): string {
  const set = o.colors?.[i];
  return set !== undefined && set !== '' ? set : guessColour(o.values[i] ?? '');
}

export const palette = ['#111827', '#f8fafc', '#1e3a5f', '#2563eb', '#7f1d1d', '#e8d8bd', '#9ca3af', '#4d5d2a', '#dc2626', '#f472b6', '#7c4a2d', '#facc15'];

export const hexPattern = /^#[0-9a-f]{6}$/;

// normalizeHex reads "#1F2A44" or "1f2a44" as "#1f2a44"; "" when it is
// not a colour.
export function normalizeHex(text: string): string {
  const t = text.trim().toLowerCase();
  const h = t.startsWith('#') ? t : `#${t}`;
  return hexPattern.test(h) ? h : '';
}

// colourIndex is the option the variants are grouped by: the first
// colour option, or -1.
export function colourIndex(options: OptionDraft[]): number {
  return options.findIndex((o) => o.colors !== null && o.values.length > 0);
}
