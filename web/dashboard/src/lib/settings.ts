import type { components } from '../api/client';
import { useSession } from '../session';

export type Role = components['schemas']['Role'];
type Domain = components['schemas']['Domain'];

// The roles as the merchant reads them (ADR-0006's matrix, in words): the
// owner's cannot be given or taken.
export const roles: { id: Role; label: string; can: string }[] = [
  { id: 'owner', label: 'المالك', can: 'كل شيء، ومنه ربط الدفع بالبطاقة وإدارة فريق العمل.' },
  { id: 'manager', label: 'مدير', can: 'يدير المتجر يوميًا: المنتجات والطلبات والشحن والتصميم والتحصيل. لا يدير فريق العمل ولا يربط الدفع بالبطاقة.' },
  { id: 'support', label: 'خدمة العملاء', can: 'يرد على العملاء ويؤكد الطلبات أو يلغيها، ويطّلع على المنتجات والشحن دون تعديل.' },
  { id: 'shipping', label: 'الشحن', can: 'يجهّز الطلبات ويشحنها ويسجّل التسليم والمرتجعات، ويضبط المخزون.' },
];

export const roleLabel = (r: Role) => roles.find((x) => x.id === r)?.label ?? r;

// useRole is the signed-in user's role in a store.
export function useRole(storeId: string): Role | undefined {
  const { state } = useSession();
  return state.kind === 'signed-in' ? state.me.memberships.find((m) => m.store_id === storeId)?.role : undefined;
}

export const timezones: { id: components['schemas']['StoreSettings']['timezone']; label: string }[] = [
  { id: 'Africa/Cairo', label: 'القاهرة (مصر)' },
  { id: 'Asia/Riyadh', label: 'الرياض (السعودية)' },
  { id: 'Asia/Dubai', label: 'دبي (الإمارات)' },
  { id: 'Asia/Kuwait', label: 'الكويت' },
  { id: 'Asia/Qatar', label: 'الدوحة (قطر)' },
  { id: 'Asia/Bahrain', label: 'المنامة (البحرين)' },
  { id: 'Asia/Muscat', label: 'مسقط (عُمان)' },
  { id: 'UTC', label: 'التوقيت العالمي (UTC)' },
];

export const currencies: Record<string, string> = { EGP: 'الجنيه المصري (ج.م)', SAR: 'الريال السعودي (ر.س)', AED: 'الدرهم الإماراتي (د.إ)' };

// domainStatus is a domain's chip.
export function domainStatus(d: Pick<Domain, 'status'>): { label: string; className: string } {
  switch (d.status) {
    case 'verified':
      return { label: 'يعمل', className: 'p-active' };
    case 'failed':
      return { label: 'فشل التحقق', className: 's-cancelled' };
  }
  return { label: 'بانتظار التحقق', className: 's-pending' };
}

export const failureReasons: Record<string, string> = {
  txt_record_not_found: 'لم نجد سجل TXT. تأكد أنك أضفته بالاسم والقيمة كما هنا، ثم اضغط «تحقّق الآن». قد يستغرق ظهوره ساعات.',
  dns_error: 'تعذّر الوصول إلى إعدادات DNS لهذا الدومين. تأكد أن الدومين مسجّل ويعمل، ثم حاول مرة أخرى.',
  domain_taken: 'هذا الدومين مربوط بمتجر آخر.',
  verification_timed_out: 'انتهت مدة التحقق التلقائي (حوالي 3 أيام) دون أن نجد السجل. أضفه ثم اضغط «تحقّق الآن».',
};

// The most custom domains a store can add (the server's limit).
export const maxCustomDomains = 5;
