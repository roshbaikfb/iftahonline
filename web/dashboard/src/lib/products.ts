import type { components } from '../api/client';

import { count } from './home';
import { money } from './orders';

export type Product = components['schemas']['Product'];
export type ProductStatus = Product['status'];
export type ProductCounts = components['schemas']['ProductCounts'];
export type Category = components['schemas']['Category'];

// The products list's tabs (the approved mockup) and the chip of each
// status.
export const productTabs: { status: ProductStatus | ''; label: string; key: keyof ProductCounts }[] = [
  { status: '', label: 'الكل', key: 'all' },
  { status: 'active', label: 'منشورة', key: 'active' },
  { status: 'draft', label: 'مسودات', key: 'draft' },
  { status: 'archived', label: 'مؤرشفة', key: 'archived' },
];

export const productStatusLabel: Record<ProductStatus, string> = { active: 'منشور', draft: 'مسودة', archived: 'مؤرشف' };

export type StockFilter = '' | 'in_stock' | 'low' | 'out';

export const stockFilters: { id: StockFilter; label: string }[] = [
  { id: '', label: 'الكل' },
  { id: 'in_stock', label: 'متوفر' },
  { id: 'low', label: 'قارب على النفاد' },
  { id: 'out', label: 'نفد' },
];

// Plurals of the option names merchants use most; any other option is
// counted as "name: n".
const optionPlurals: Record<string, [string, string, string, string]> = {
  اللون: ['لون واحد', 'لونان', 'ألوان', 'لونًا'],
  المقاس: ['مقاس واحد', 'مقاسان', 'مقاسات', 'مقاسًا'],
  الحجم: ['حجم واحد', 'حجمان', 'أحجام', 'حجمًا'],
};

// variantsLine sums a product's options up for its list row: "3 ألوان ·
// 4 مقاسات · 12 نوعًا"; a product without options has none.
export function variantsLine(p: Pick<Product, 'options' | 'variants'>): string {
  if (p.options.length === 0) return 'بلا خيارات';
  const parts = p.options.map((o) => {
    const forms = optionPlurals[o.name.trim()];
    return forms ? count(o.values.length, ...forms) : `${o.name}: ${String(o.values.length)}`;
  });
  if (p.options.length > 1) parts.push(count(p.variants.length, 'نوع واحد', 'نوعان', 'أنواع', 'نوعًا'));
  return parts.join(' · ');
}

export interface StockView {
  units?: number; // free units of tracked variants; none when nothing is tracked
  variants: number; // variants they are spread over
  state: 'plenty' | 'low' | 'out' | 'untracked';
  lowVariants: number; // variants with 1 to threshold free
}

// stockView is a product's stock as the list shows it: free units, and
// whether it sold out or runs low (threshold from the store's theme).
export function stockView(p: Pick<Product, 'variants'>, threshold: number): StockView {
  const tracked = p.variants.filter((v) => v.stock.tracked);
  const untracked = tracked.length < p.variants.length;
  const units = tracked.reduce((n, v) => n + v.stock.available, 0);
  const lowVariants = tracked.filter((v) => v.stock.available > 0 && v.stock.available <= threshold).length;
  let state: StockView['state'] = 'plenty';
  if (tracked.length === 0) state = 'untracked';
  else if (!untracked && units === 0) state = 'out';
  else if (lowVariants > 0) state = 'low';
  const view: StockView = { variants: p.variants.length, state, lowVariants };
  if (tracked.length > 0) view.units = units;
  return view;
}

// priceLine is a product's price or price range; a single price on sale
// also gives the price before.
export function priceLine(p: Pick<Product, 'variants'>): { price: string; before?: string } {
  const first = p.variants[0];
  if (!first) return { price: '' };
  const prices = p.variants.map((v) => v.price_minor);
  const lo = Math.min(...prices);
  const hi = Math.max(...prices);
  if (lo !== hi) return { price: `${money(lo, first.currency).replace(/ \S+$/, '')} – ${money(hi, first.currency)}` };
  const compare = p.variants.find((v) => v.compare_at_minor !== undefined)?.compare_at_minor;
  return compare === undefined ? { price: money(lo, first.currency) } : { price: money(lo, first.currency), before: money(compare, first.currency).replace(/ \S+$/, '') };
}

// thumb is the product's first ready picture, small.
export function thumb(p: Pick<Product, 'images'>): string | undefined {
  return p.images.find((i) => i.status === 'ready' && i.urls)?.urls?.thumb.webp;
}

// selectedProducts counts selected products the way Arabic says it.
export function selectedProducts(n: number): string {
  return `حدّدت ${count(n, 'منتجًا واحدًا', 'منتجين', 'منتجات', 'منتجًا')}`;
}

// productsTotal is the count under the title ("24 منتجًا").
export function productsTotal(n: number): string {
  return n === 0 ? 'لا منتجات بعد' : count(n, 'منتج واحد', 'منتجان', 'منتجات', 'منتجًا');
}
