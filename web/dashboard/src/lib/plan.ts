import { useEffect, useState } from 'react';

import { api, type components } from '../api/client';

export type PlanFeatures = components['schemas']['PlanFeatures'];
export type Included = PlanFeatures['included'][number];
export type Full = PlanFeatures['full'][number];

// featureName names a plan feature in a sentence ("باقتك لا تشمل …").
export const featureName: Record<string, string> = {
  card_payments: 'الدفع بالبطاقة',
  coupons: 'الكوبونات',
  pixels: 'بكسلات الإعلانات',
  feeds: 'كتالوج المنتجات للإعلانات',
  ad_free: 'المتجر بدون إعلانات',
  risk_rules: 'حماية الدفع عند الاستلام',
  profit: 'لوحة الربح الصافي',
  products: 'المنتجات',
  staff: 'أعضاء الفريق',
  custom_domains: 'الدومينات الخاصة',
  orders_per_month: 'الطلبات في الشهر',
  messages: 'رسائل واتساب وSMS',
};

// PlanLimitCode is the problem code of a request the store's plan does
// not allow (403).
export const PlanLimitCode = 'plan_limit';

// isPlanLimit reports whether an API error is the plan's refusal.
export function isPlanLimit(error: unknown): boolean {
  return (error as { code?: string } | undefined)?.code === PlanLimitCode;
}

// planLimitMessage says the plan's refusal in Arabic. The server writes
// it in English with the feature's key ("…allows 1 staff…", "…does not
// include coupons…"); the key names the feature here.
export function planLimitMessage(error: unknown): string {
  const detail = (error as { detail?: string } | undefined)?.detail ?? '';
  const limit = /allows (\d+) ([a-z_]+);/.exec(detail);
  if (limit) {
    const name = featureName[limit[2] ?? ''] ?? 'هذه الميزة';
    return `وصلت إلى حد باقتك في ${name} (${limit[1] ?? ''}). رقِّ باقتك من «الفوترة والباقة» لتضيف المزيد.`;
  }
  const flag = /does not include ([a-z_]+);/.exec(detail);
  const name = flag ? (featureName[flag[1] ?? ''] ?? 'هذه الميزة') : 'هذه الميزة';
  return `باقتك الحالية لا تشمل ${name}. رقِّ باقتك من «الفوترة والباقة» لتستخدمها.`;
}

// usePlanFeatures reads what the store's plan includes, to lock a form
// before the server refuses it; null until read (or if it cannot be).
export function usePlanFeatures(storeId: string): PlanFeatures | null {
  const [pf, setPf] = useState<PlanFeatures | null>(null);
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/plan-features', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setPf(data);
    });
    return () => {
      live = false;
    };
  }, [storeId]);
  return pf;
}

// lockedFor reports whether the plan rules out the feature: a flag it does
// not include, or a counted feature with no room left. Unknown (null)
// plans lock nothing: the server still refuses.
export function lockedFor(pf: PlanFeatures | null, feature: Included | Full): boolean {
  if (!pf) return false;
  if ((pf.full as string[]).includes(feature)) return true;
  const flags: string[] = ['card_payments', 'coupons', 'pixels', 'feeds', 'ad_free', 'risk_rules', 'profit'];
  return flags.includes(feature) && !(pf.included as string[]).includes(feature);
}
