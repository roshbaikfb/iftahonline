import type { components } from '../api/client';

export type Onboarding = components['schemas']['Onboarding'];
export type Activity = components['schemas']['OnboardingActivity'];
export type Look = components['schemas']['OnboardingLook'];

// The wizard's six steps (ADR-0034, the approved mockup).
export const wizardSteps = ['الحساب', 'المتجر', 'النشاط', 'الشكل', 'الشحن', 'جاهز'] as const;

export interface Country {
  id: 'eg' | 'sa' | 'ae';
  name: string;
  currencyName: string;
  currency: 'EGP' | 'SAR' | 'AED';
  timezone: string;
}

// The countries a store can sell in; the country sets the currency, which
// does not change after sign-up.
export const countries: Country[] = [
  { id: 'eg', name: 'مصر', currencyName: 'جنيه مصري', currency: 'EGP', timezone: 'Africa/Cairo' },
  { id: 'sa', name: 'السعودية', currencyName: 'ريال سعودي', currency: 'SAR', timezone: 'Asia/Riyadh' },
  { id: 'ae', name: 'الإمارات', currencyName: 'درهم إماراتي', currency: 'AED', timezone: 'Asia/Dubai' },
];

export interface ActivityCard {
  id: Activity;
  label: string;
  color: string; // the suggested main colour
  icon: string; // an SVG path, 24×24, stroked
}

export const activities: ActivityCard[] = [
  { id: 'clothing', label: 'ملابس', color: '#1e3a5f', icon: 'M8 3l-5 3 2 4 2-1v12h10V9l2 1 2-4-5-3c-1 2-2.5 3-4 3S9 5 8 3z' },
  { id: 'electronics', label: 'إلكترونيات', color: '#1d4ed8', icon: 'M7 2h10a1 1 0 011 1v18a1 1 0 01-1 1H7a1 1 0 01-1-1V3a1 1 0 011-1zM11 18h2' },
  {
    id: 'beauty',
    label: 'تجميل',
    color: '#be185d',
    icon: 'M12 3l1.8 4.7L18.5 9l-4.7 1.8L12 15.5l-1.8-4.7L5.5 9l4.7-1.3zM18 15l.9 2.1L21 18l-2.1.9L18 21l-.9-2.1L15 18l2.1-.9z',
  },
  { id: 'perfume', label: 'عطور', color: '#92400e', icon: 'M10 2h4v3h-4zM9 5h6v2l2 2v11a2 2 0 01-2 2H9a2 2 0 01-2-2V9l2-2z' },
  {
    id: 'kids',
    label: 'أطفال',
    color: '#0e7490',
    icon: 'M12 21a7 7 0 100-14 7 7 0 000 14zM7 8.5A3 3 0 115.5 4M17 8.5A3 3 0 1018.5 4M10 13h.01M14 13h.01M10 16.5c1.2 1 2.8 1 4 0',
  },
  { id: 'food', label: 'أغذية', color: '#15803d', icon: 'M12 7c-3-3-8-1-8 4 0 5 4 10 8 10s8-5 8-10c0-5-5-7-8-4zM12 7c0-2 1-4 3-5' },
  { id: 'home', label: 'منزل', color: '#7c2d12', icon: 'M3 11l9-7 9 7M5 10v10h14V10M10 20v-6h4v6' },
  { id: 'other', label: 'أخرى', color: '#334155', icon: 'M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z' },
];

export interface LookCard {
  id: Look;
  name: string;
  hint: string;
}

export const looks: LookCard[] = [
  { id: 'classic', name: 'كلاسيكي', hint: 'شعار في المنتصف، صورة عريضة، شبكة منتظمة' },
  { id: 'modern', name: 'عصري', hint: 'عنوان بجوار صورة، منتجات كبيرة' },
  { id: 'soft', name: 'ناعم', hint: 'زوايا مستديرة، تصنيفات دائرية، ألوان هادئة' },
];

// palette is the colours offered for an activity: its suggestion first,
// then a few others, without repeats.
export function palette(activity: Activity): string[] {
  const first = activities.find((a) => a.id === activity)?.color ?? '#0f2e52';
  return [first, '#0f2e52', '#15803d', '#b91c1c', '#6d28d9'].filter((c, i, all) => all.indexOf(c) === i).slice(0, 5);
}

// tint mixes a #rrggbb colour into white (share 0.13 is a light wash), as
// the soft look's page does.
export function tint(color: string, share: number): string {
  let out = '#';
  for (let i = 1; i < 7; i += 2) {
    const v = Number.parseInt(color.slice(i, i + 2), 16);
    if (Number.isNaN(v)) return '#ffffff';
    out += Math.round(255 - share * (255 - v))
      .toString(16)
      .padStart(2, '0');
  }
  return out;
}

// slugProblem explains why a store address cannot be used.
export const slugProblem: Record<'invalid' | 'reserved' | 'taken', string> = {
  invalid: 'من 3 إلى 63 حرفًا إنجليزيًا صغيرًا أو رقمًا أو شرطة، لا تبدأ ولا تنتهي بشرطة.',
  reserved: 'هذا الرابط محجوز، اختر غيره.',
  taken: 'هذا الرابط مستخدم، جرّب غيره.',
};
