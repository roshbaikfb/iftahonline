import type { components } from '../api/client';

export type OrderStats = components['schemas']['OrderStats'];
export type StatsRange = OrderStats['range'];

// The home page's periods (the approved mockup).
export const ranges: { id: StatsRange; label: string; versus: string }[] = [
  { id: 'today', label: 'اليوم', versus: 'عن أمس' },
  { id: '7d', label: '7 أيام', versus: 'عن الأيام السبعة السابقة' },
  { id: '30d', label: '30 يوم', versus: 'عن الثلاثين يومًا السابقة' },
];

// greeting says good morning before noon, good evening after.
export function greeting(now: Date): string {
  return now.getHours() < 12 ? 'صباح الخير' : 'مساء الخير';
}

// count names n things the Arabic way: one, two, 3–10, 11 and more.
export function count(n: number, one: string, two: string, few: string, many: string): string {
  if (n === 1) return one;
  if (n === 2) return two;
  if (n >= 3 && n <= 10) return `${String(n)} ${few}`;
  return `${String(n)} ${many}`;
}

// ago says how long ago iso was, as the mockup does ("منذ 5 دقائق",
// "أمس"); older than yesterday, the date.
export function ago(iso: string, now: Date): string {
  const then = new Date(iso);
  const minutes = Math.floor((now.getTime() - then.getTime()) / 60_000);
  if (minutes < 1) return 'الآن';
  if (minutes < 60) return `منذ ${count(minutes, 'دقيقة', 'دقيقتين', 'دقائق', 'دقيقة')}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24 && then.getDate() === now.getDate()) return `منذ ${count(hours, 'ساعة', 'ساعتين', 'ساعات', 'ساعة')}`;
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (then.toDateString() === yesterday.toDateString()) return 'أمس';
  return then.toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long' });
}

// change is the sales change against the previous period, as a signed
// whole percent ("+18%"); none when there was nothing to compare with.
export function change(now: number, before: number): string | null {
  if (before <= 0) return null;
  const pct = Math.round(((now - before) / before) * 100);
  return `${pct > 0 ? '+' : pct < 0 ? '−' : ''}${String(Math.abs(pct))}%`;
}

// dayLabel is a chart day ("25 سبتمبر").
export function dayLabel(date: string): string {
  return new Date(`${date}T12:00:00`).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long' });
}
