import type { components } from '../api/client';

type Kind = components['schemas']['SupportLink']['kind'];

// Platform support's delayed requests (C1, ADR-0095), shared by the
// console's team card and the dashboard's link page.

// requestTitle names what a request does.
export const requestTitle = (k: Kind) => (k === 'mfa_reset' ? 'طلب إلغاء التحقق بخطوتين' : 'طلب نقل ملكية المتجر');

// keptLine says what stays as it was once a request is cancelled.
export const keptLine = (k: Kind) => (k === 'mfa_reset' ? 'التحقق بخطوتين باقٍ على حسابك كما هو.' : 'ملكية المتجر باقية كما هي.');

// hoursUntil words the time until a request takes effect: «بعد 23 ساعة».
export function hoursUntil(iso: string, now: Date): string {
  const h = Math.floor((new Date(iso).getTime() - now.getTime()) / 3_600_000);
  if (h < 1) return 'بعد أقل من ساعة';
  if (h === 1) return 'بعد ساعة';
  if (h === 2) return 'بعد ساعتين';
  return h <= 10 ? `بعد ${String(h)} ساعات` : `بعد ${String(h)} ساعة`;
}

// whenAr is a moment as the page writes it: «السبت 7 نوفمبر 2:30 م».
export const whenAr = (iso: string) =>
  new Date(iso).toLocaleString('ar-EG-u-nu-latn', { weekday: 'long', day: 'numeric', month: 'long', hour: 'numeric', minute: '2-digit' });
