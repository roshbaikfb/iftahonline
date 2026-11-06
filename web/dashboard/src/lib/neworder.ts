import type { components } from '../api/client';
import { problemMessage } from './problem';

type Product = components['schemas']['Product'];

// A variant the merchant can put on a phone order (D5, the approved
// mockup): its product's name with its options, its price, and how many
// are free (null: stock not tracked, never runs out).
export interface Pick {
  variantId: string;
  name: string;
  priceMinor: number;
  currency: string;
  available: number | null;
}

// A line of the order being entered.
export interface Line extends Pick {
  qty: number;
}

// picks lists the variants of the found products that can be sold: active
// products, variants not hidden.
export function picks(products: Product[]): Pick[] {
  return products
    .filter((p) => p.status === 'active')
    .flatMap((p) =>
      p.variants
        .filter((v) => v.hidden !== true)
        .map((v) => ({
          variantId: v.id,
          name: v.option_values.length > 0 ? `${p.title} · ${v.option_values.join(' / ')}` : p.title,
          priceMinor: v.price_minor,
          currency: v.currency,
          available: v.stock.tracked ? Math.max(0, v.stock.available) : null,
        })),
    );
}

// soldOut is a tracked variant with none free.
export const soldOut = (p: Pick) => p.available !== null && p.available <= 0;

// addLine puts a pick on the order, or one more of it when it is there.
export function addLine(lines: Line[], p: Pick): Line[] {
  if (lines.some((l) => l.variantId === p.variantId)) {
    return lines.map((l) => (l.variantId === p.variantId ? { ...l, qty: Math.min(maxQty, l.qty + 1) } : l));
  }
  return [...lines, { ...p, qty: 1 }];
}

// An order line's quantity bounds (the API's).
export const maxQty = 100;
export const maxLines = 50;

// subtotal is the lines' total in minor units.
export const subtotal = (lines: Line[]) => lines.reduce((sum, l) => sum + l.priceMinor * l.qty, 0);

interface FieldError {
  field: string;
  code: string;
}

// orderMessage says why the order was refused, in Arabic.
export function orderMessage(error: unknown): string {
  const p = error as { code?: string; errors?: FieldError[] } | undefined;
  if (p && typeof p === 'object') {
    if (p.code === 'customer_blocked') return 'هذا العميل محظور في متجرك، فلا يُنشأ له طلب.';
    if (p.code === 'insufficient_stock') return 'الكمية المطلوبة أكبر من المتاح في المخزون.';
    const said = (p.errors ?? []).map(fieldMessage);
    if (said.length > 0) return [...new Set(said)].join(' ');
  }
  return problemMessage(error);
}

function fieldMessage(f: FieldError): string {
  if (f.field === 'phone') return 'اكتب رقم موبايل صحيحًا.';
  if (f.field === 'governorate') return f.code === 'not_delivered' ? 'لا توصيل لهذه المحافظة في إعدادات الشحن.' : 'اختر المحافظة.';
  if (f.field === 'coupon_code') return f.code === 'not_found' ? 'هذا الكوبون غير موجود.' : 'هذا الكوبون لا ينطبق على هذا الطلب.';
  if (f.field.startsWith('lines')) return f.code === 'unavailable' ? 'منتج في الطلب لم يعد متاحًا للبيع.' : 'راجع المنتجات وكمياتها.';
  if (f.field === 'name') return 'اكتب اسم العميل.';
  if (f.field === 'city') return 'اكتب المدينة أو المنطقة.';
  if (f.field === 'address') return 'اكتب العنوان.';
  return 'راجع بيانات الطلب.';
}
