import type { components } from '../api/client';

export type AdBanner = components['schemas']['AdminAdBanner'];
export type AdSettings = components['schemas']['AdminAdSettings'];
export type Placement = components['schemas']['AdminAdPlacement'];
export type Country = NonNullable<AdBanner['country']>;

// placements are the four places in the console's order (ADR-0062), with
// the box each reserves on a store's page.
export const placements: { key: Placement; name: string; size: string }[] = [
  { key: 'home_top', name: 'أعلى الرئيسية', size: '320×100' },
  { key: 'grid', name: 'بين المنتجات', size: '300×250' },
  { key: 'product_bottom', name: 'صفحة المنتج (أسفل الوصف)', size: '300×250' },
  { key: 'category_top', name: 'صفحة التصنيف', size: '320×100' },
];

export const placementName = (p: Placement) => placements.find((x) => x.key === p)?.name ?? p;

export const countries: { key: Country; name: string }[] = [
  { key: 'EG', name: 'مصر' },
  { key: 'SA', name: 'السعودية' },
  { key: 'AE', name: 'الإمارات' },
];

export const countryName = (c?: Country) => (c ? (countries.find((x) => x.key === c)?.name ?? c) : 'كل الدول');

export type BannerState = 'live' | 'scheduled' | 'ended' | 'stopped';

export const bannerStateLabel: Record<BannerState, string> = {
  live: 'نشط',
  scheduled: 'مجدول',
  ended: 'انتهى',
  stopped: 'متوقف',
};

// bannerState is whether a banner shows at now.
export function bannerState(b: AdBanner, now: Date): BannerState {
  if (!b.active) return 'stopped';
  if (b.ends_at && new Date(b.ends_at) <= now) return 'ended';
  if (new Date(b.starts_at) > now) return 'scheduled';
  return 'live';
}

// liveCount is how many banners each placement shows at now.
export function liveCount(banners: AdBanner[], now: Date): Record<Placement, number> {
  const out: Record<Placement, number> = { home_top: 0, grid: 0, product_bottom: 0, category_top: 0 };
  for (const b of banners) if (bannerState(b, now) === 'live') out[b.placement]++;
  return out;
}

const day = (iso: string) => new Date(iso).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long' });

// period is a banner's run: "من 1 نوفمبر" or "1 نوفمبر – 30 نوفمبر".
export function period(b: AdBanner): string {
  return b.ends_at ? `${day(b.starts_at)} – ${day(b.ends_at)}` : `من ${day(b.starts_at)}`;
}

// startOfDay is a date input's day (local midnight) as an instant.
export function startOfDay(date: string): string {
  return new Date(`${date}T00:00:00`).toISOString();
}
