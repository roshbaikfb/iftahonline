import type { components } from '../api/client';
import { roleLabel, type Role } from './settings';

export type AuditEntry = components['schemas']['AuditEntry'];

// What an entry says (D6, the approved mockup): the change as a verbal
// noun, so it reads right whoever made it; an optional quoted part (a
// code, a domain) kept left-to-right; and an optional link to the thing.
export interface Said {
  text: string;
  ltr?: string;
  after?: string;
  link?: { to: string; label: string };
}

const statusWords: Record<string, string> = {
  pending: 'إعادة الطلب إلى «قيد الانتظار»',
  confirmed: 'تأكيد الطلب',
  ready_to_ship: 'تجهيز الطلب للشحن',
  shipped: 'شحن الطلب',
  delivered: 'تسليم الطلب',
  cancelled: 'إلغاء الطلب',
  failed_delivery: 'تسجيل فشل توصيل الطلب',
  returned: 'تسجيل إرجاع الطلب',
};

const paymentWords: Record<string, string> = {
  unpaid: 'تسجيل الطلب غير مدفوع',
  awaiting: 'انتظار دفع الطلب',
  paid: 'تسجيل دفع الطلب',
  partially_refunded: 'استرداد جزء من مبلغ الطلب',
  refunded: 'استرداد مبلغ الطلب',
};

// Fixed sentences, by action.
const plain: Record<string, string> = {
  'store.created': 'إنشاء المتجر',
  'store.updated': 'تعديل بيانات المتجر',
  'store.country_changed': 'تغيير دولة المتجر',
  'store.billing_held': 'إيقاف المتجر لعدم الدفع',
  'store.billing_hold_released': 'إعادة تشغيل المتجر بعد الدفع',
  'onboarding.template_chosen': 'اختيار شكل المتجر في معالج البداية',
  'onboarding.completed': 'إنهاء معالج البداية',
  'product.created': 'إضافة منتج',
  'product.updated': 'تعديل منتج',
  'product.deleted': 'حذف منتج',
  'image.created': 'إضافة صورة منتج',
  'image.updated': 'تعديل صورة منتج',
  'image.deleted': 'حذف صورة منتج',
  'inventory.updated': 'تعديل المخزون',
  'category.created': 'إضافة تصنيف',
  'category.updated': 'تعديل تصنيف',
  'category.deleted': 'حذف تصنيف',
  'size_chart.created': 'إضافة جدول مقاسات',
  'size_chart.updated': 'تعديل جدول مقاسات',
  'size_chart.deleted': 'حذف جدول مقاسات',
  'import.created': 'بدء استيراد منتجات',
  'import.finished': 'انتهاء استيراد منتجات',
  'order.placed': 'إدخال طلب',
  'order.released': 'الإفراج عن طلب موقوف للمراجعة',
  'order.exported': 'تصدير الطلبات إلى ملف CSV',
  'customer.blocked': 'حظر عميل',
  'customer.unblocked': 'رفع الحظر عن عميل',
  'shipment.requested': 'طلب شحنة من شركة الشحن',
  'shipment.cancel_requested': 'طلب إلغاء شحنة',
  'shipping_zone.created': 'إضافة منطقة شحن',
  'shipping_zone.updated': 'تعديل منطقة شحن',
  'shipping_zone.deleted': 'حذف منطقة شحن',
  'shipping_settings.updated': 'تعديل إعدادات الشحن',
  'cod_statement.imported': 'استيراد كشف تحصيل',
  'payment_settings.updated': 'تعديل إعدادات الدفع',
  'theme.updated': 'تعديل تصميم المتجر',
  'theme.published': 'نشر تصميم المتجر',
  'home.published': 'نشر الصفحة الرئيسية',
  'page.created': 'إضافة صفحة',
  'page.updated': 'تعديل صفحة',
  'page.deleted': 'حذف صفحة',
  'logo.uploaded': 'رفع شعار المتجر',
  'logo.deleted': 'حذف شعار المتجر',
  'pixel.deleted': 'حذف بكسل إعلانات',
  'marketing_settings.updated': 'تعديل إعدادات التسويق',
  'product_feed.key_renewed': 'تجديد رابط كتالوج المنتجات',
  'product_feed.disabled': 'إيقاف كتالوج المنتجات',
  'messaging_settings.updated': 'تعديل إعدادات رسائل العملاء',
  'message_text.updated': 'تعديل نص رسالة للعملاء',
  'message_text.reset': 'إرجاع نص رسالة إلى الأصل',
  'risk.rules_updated': 'تعديل قواعد الطلبات المشكوك فيها',
  'analytics.settings_updated': 'تعديل إعدادات الربح الصافي',
  'invitation.revoked': 'إلغاء دعوة لفريق العمل',
  'member.joined': 'انضمام عضو إلى فريق العمل',
  'member.removed': 'إخراج عضو من فريق العمل',
  'domain.verification_requested': 'طلب التحقق من دومين',
  'impersonation.started': 'بدء جلسة دعم من فريق المنصة',
};

const str = (v: unknown) => (typeof v === 'string' ? v : '');
const role = (v: unknown) => roleLabel(str(v) as Role);

// describe says an entry in Arabic; an action this dashboard does not know
// yet is «تغيير في المتجر».
export function describe(e: AuditEntry, storeId: string): Said {
  const at = (path: string) => `/stores/${storeId}/${path}`;
  const m = e.metadata;
  const [kind = '', verb = ''] = e.action.split('.');
  if (kind === 'order') {
    const order = { to: at(`orders/${e.target_id}`), label: `#${e.target_id.slice(-8)}` };
    const deposit = verb.startsWith('deposit_') ? paymentWords[verb.slice(8)] : undefined;
    const words =
      statusWords[verb] ??
      (verb.startsWith('payment_') ? paymentWords[verb.slice(8)] : undefined) ??
      (deposit ? `${deposit} (رسوم التوصيل المقدمة)` : undefined) ??
      plain[e.action];
    if (words) return e.action === 'order.exported' ? { text: words } : { text: words, link: order };
  }
  switch (e.action) {
    case 'product.created':
    case 'product.updated':
      return { text: plain[e.action] ?? '', link: { to: at(`products/${e.target_id}`), label: 'عرض المنتج' } };
    case 'coupon.created':
    case 'coupon.updated':
    case 'coupon.deleted':
      return {
        text: e.action === 'coupon.created' ? 'إضافة الكوبون' : e.action === 'coupon.updated' ? 'تعديل الكوبون' : 'حذف الكوبون',
        ltr: str(m['code']),
      };
    case 'domain.added':
      return { text: 'إضافة الدومين', ltr: str(m['host']) };
    case 'domain.removed':
      return { text: 'حذف الدومين', ltr: str(m['host']) };
    case 'domain.verified':
      return { text: 'التحقق من الدومين', ltr: str(m['host']) };
    case 'domain.primary_set':
      return { text: 'جعل الدومين أساسيًا', ltr: str(m['host']) };
    case 'payment_connection.created':
      return { text: 'ربط بوابة الدفع', ltr: gatewayName(str(m['gateway'])) };
    case 'payment_connection.disconnected':
      return { text: 'فصل بوابة الدفع', ltr: gatewayName(str(m['gateway'])) };
    case 'payment.refund_requested':
      return { text: 'طلب استرداد مبلغ لعميل' };
    case 'carrier_connection.created':
      return { text: 'ربط شركة الشحن', ltr: str(m['carrier']) };
    case 'carrier_connection.disconnected':
      return { text: 'فصل شركة الشحن', ltr: str(m['carrier']) };
    case 'member.invited':
      return { text: `دعوة عضو جديد بدور ${role(m['role'])}` };
    case 'member.role_changed':
      return { text: `تغيير دور عضو من ${role(m['from'])} إلى ${role(m['to'])}` };
    case 'store.status_changed':
      return { text: m['status'] === 'active' ? 'فتح المتجر للزوار' : 'إغلاق المتجر مؤقتًا' };
    case 'pixel.saved':
      return { text: 'حفظ بكسل إعلانات', ltr: str(m['pixel_id']) };
    case 'impersonation.request':
      return { text: m['refused'] === true ? 'طلب مرفوض من جلسة الدعم' : 'تغيير من جلسة الدعم' };
  }
  const known = plain[e.action];
  return { text: known ?? 'تغيير في المتجر' };
}

const gateways: Record<string, string> = { paymob: 'Paymob', stripe: 'Stripe', fake: 'Fake' };
const gatewayName = (g: string) => gateways[g] ?? g;

// who names the member behind an entry: the name on the team today, a
// former member, the platform's support team, or the store itself (an
// automatic step, such as an unanswered confirmation).
export function who(e: AuditEntry, names: ReadonlyMap<string, string>): string {
  if (e.metadata['impersonated_by'] !== undefined || e.action.startsWith('impersonation.')) return 'فريق دعم المنصة';
  if (!e.actor_user_id) return 'المتجر تلقائيًا';
  return names.get(e.actor_user_id) ?? 'عضو سابق';
}

// when says the time the way the mockup does: «اليوم 2:14 م», «أمس 9:40 م»,
// else the date («3 نوفمبر»).
export function when(iso: string, now: Date): string {
  const t = new Date(iso);
  const time = t.toLocaleTimeString('ar-EG-u-nu-latn', { hour: 'numeric', minute: '2-digit' });
  if (t.toDateString() === now.toDateString()) return `اليوم ${time}`;
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (t.toDateString() === y.toDateString()) return `أمس ${time}`;
  return t.toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', ...(t.getFullYear() === now.getFullYear() ? {} : { year: 'numeric' }) });
}
