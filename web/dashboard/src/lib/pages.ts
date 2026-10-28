import type { components } from '../api/client';

type Page = components['schemas']['Page'];

export type PolicySlug = 'shipping' | 'refund' | 'privacy' | 'terms';

// The policies a store can publish, in the shop's order, with the title
// the shop shows when the merchant leaves it empty.
export const policies: { slug: PolicySlug; title: string; hint: string }[] = [
  { slug: 'shipping', title: 'سياسة الشحن', hint: 'المدن التي تشحن لها، ومدة التوصيل وتكلفته.' },
  { slug: 'refund', title: 'سياسة الاسترجاع', hint: 'متى وكيف يُرجع العميل منتجًا ويسترد ماله.' },
  { slug: 'privacy', title: 'سياسة الخصوصية', hint: 'ما تجمعه من بيانات عملائك وكيف تستخدمها.' },
  { slug: 'terms', title: 'الشروط والأحكام', hint: 'شروط الشراء من متجرك.' },
];

export const isPolicy = (s: string): s is PolicySlug => policies.some((p) => p.slug === s);

// pageStatus is a page's chip: published, a draft, or (a policy) not
// written yet.
export function pageStatus(p: Pick<Page, 'status' | 'version'>): { label: string; className: string } {
  if (p.version === 0) return { label: 'لم تُكتب بعد', className: 'p-archived' };
  return p.status === 'published' ? { label: 'منشورة', className: 'p-active' } : { label: 'مسودة', className: 'p-draft' };
}

// The most landing pages a store can have (the server's limit).
export const maxLandingPages = 50;
