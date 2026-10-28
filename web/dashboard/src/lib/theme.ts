import type { components } from '../api/client';

export type ThemeSettings = components['schemas']['ThemeSettings'];
export type Look = NonNullable<ThemeSettings['look']>;
export type Font = ThemeSettings['font'];
export type Radius = ThemeSettings['radius'];

// The looks of the approved mockup (ADR-0034's three layouts).
export const looks: { id: Look; label: string; hint: string }[] = [
  { id: 'classic', label: 'كلاسيكي', hint: 'الشعار في المنتصف' },
  { id: 'modern', label: 'عصري', hint: 'بطاقات أكبر' },
  { id: 'soft', label: 'ناعم', hint: 'تصنيفات دائرية' },
];

// The theme's fonts, as the storefront names them, and the CSS family
// the preview uses (shop-fonts.css; Iftah Sans is the dashboard's own).
export const fonts: { id: Font; label: string; family: string }[] = [
  { id: 'cairo', label: 'Cairo — كايرو', family: "'Cairo', sans-serif" },
  { id: 'tajawal', label: 'Tajawal — تجوال', family: "'Tajawal', sans-serif" },
  { id: 'ibm-plex-sans-arabic', label: 'IBM Plex Sans Arabic', family: "'Iftah Sans Arabic', sans-serif" },
  { id: 'noto-kufi-arabic', label: 'Noto Kufi Arabic', family: "'Noto Kufi Arabic', sans-serif" },
  { id: 'system', label: 'خط الجهاز', family: 'system-ui, sans-serif' },
];

export const radii: { id: Radius; label: string; css: string }[] = [
  { id: 'none', label: 'حادة', css: '0' },
  { id: 'small', label: 'صغيرة', css: '4px' },
  { id: 'medium', label: 'متوسطة', css: '8px' },
  { id: 'large', label: 'كبيرة', css: '16px' },
];

type ColorKey = keyof ThemeSettings['colors'];

export const colorRows: { key: ColorKey; label: string; hint?: string }[] = [
  { key: 'primary', label: 'اللون الأساسي', hint: 'الأزرار والروابط والأسعار' },
  { key: 'accent', label: 'لون التمييز', hint: 'الخصومات والشارات' },
  { key: 'background', label: 'الخلفية' },
  { key: 'text', label: 'النص' },
];

// Ready palettes («ألوان جاهزة»): each passes the storefront's contrast
// rule for text on background.
export const presets: { label: string; colors: ThemeSettings['colors'] }[] = [
  { label: 'كحلي', colors: { primary: '#0f2e52', accent: '#15803d', background: '#ffffff', text: '#0f172a' } },
  { label: 'فيروزي', colors: { primary: '#0f766e', accent: '#f59e0b', background: '#ffffff', text: '#0f172a' } },
  { label: 'نبيتي', colors: { primary: '#7f1d1d', accent: '#d97706', background: '#fffbf5', text: '#1c1917' } },
  { label: 'وردي', colors: { primary: '#be185d', accent: '#7c3aed', background: '#fff7fb', text: '#1f2937' } },
  { label: 'أسود', colors: { primary: '#111827', accent: '#dc2626', background: '#ffffff', text: '#111827' } },
];

export const hexColor = /^#[0-9a-fA-F]{6}$/;

function channel(v: number): number {
  const c = v / 255;
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function luminance(hex: string): number {
  const n = parseInt(hex.slice(1), 16);
  return 0.2126 * channel((n >> 16) & 255) + 0.7152 * channel((n >> 8) & 255) + 0.0722 * channel(n & 255);
}

// contrast is the WCAG contrast ratio of two "#rrggbb" colours (1 to 21),
// the rule the storefront enforces for text on the background (4.5).
export function contrast(a: string, b: string): number {
  if (!hexColor.test(a) || !hexColor.test(b)) return 0;
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

export const minContrast = 4.5;

// sameSettings compares two themes as the server stores them (colours
// are lower case there).
export function sameSettings(a: ThemeSettings, b: ThemeSettings): boolean {
  const norm = (t: ThemeSettings) =>
    JSON.stringify({
      ...t,
      colors: Object.fromEntries(Object.entries(t.colors).map(([k, v]) => [k, v.toLowerCase()])),
      announcement: t.announcement ?? '',
      about: t.about ?? '',
      contact: { whatsapp: t.contact?.whatsapp ?? '', email: t.contact?.email ?? '' },
      look: t.look ?? '',
      low_stock: t.low_stock ?? 5,
    });
  return norm(a) === norm(b);
}
