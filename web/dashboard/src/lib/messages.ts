import type { components } from '../api/client';

import { dateTime } from './orders';
import type { Role } from './settings';

export type CustomerMessage = components['schemas']['CustomerMessage'];
export type MessageStatus = components['schemas']['MessageStatus'];
export type MessageText = components['schemas']['MessageText'];
export type MessagingSettings = components['schemas']['MessagingSettings'];
export type MessageUsage = components['schemas']['MessageUsage'];
export type Notice = components['schemas']['Notification'];
export type OrderConfirmation = components['schemas']['OrderConfirmation'];

// Who may read the messages and notices, and who may change the settings
// (authz: messaging.read, messaging.manage).
export const canReadMessages = (r: Role | undefined) => r === 'owner' || r === 'manager' || r === 'support';
export const canManageMessages = (r: Role | undefined) => r === 'owner' || r === 'manager';

export const messageStatusLabel: Record<MessageStatus, string> = {
  queued: 'بانتظار الإرسال',
  sent: 'أُرسلت',
  delivered: 'وصلت',
  read: 'قُرئت',
  failed: 'لم تصل',
  skipped_quota: 'لم تُرسل: نفد الرصيد',
};

// The log's tabs, in the order a message moves through them.
export const messageTabs: { status: MessageStatus | ''; label: string }[] = [
  { status: '', label: 'الكل' },
  { status: 'queued', label: 'بانتظار الإرسال' },
  { status: 'sent', label: 'أُرسلت' },
  { status: 'delivered', label: 'وصلت' },
  { status: 'read', label: 'قُرئت' },
  { status: 'failed', label: 'لم تصل' },
  { status: 'skipped_quota', label: 'نفد الرصيد' },
];

export const messageKindLabel: Record<CustomerMessage['kind'], string> = {
  order_confirmation: 'تأكيد طلب',
  order_answer_ack: 'شكر على الرد',
};

export const channelLabel = (c: CustomerMessage['channel']) => (c === 'whatsapp' ? 'واتساب' : c === 'sms' ? 'SMS' : '—');

// failureLabel says why a message did not reach the customer.
export function failureLabel(code: string | undefined): string {
  switch (code) {
    case undefined:
    case '':
      return '';
    case 'undeliverable':
      return 'الرقم لا يستقبل الرسائل';
    case 'rejected':
      return 'رفضتها شركة الرسائل';
    case 'unavailable':
      return 'تعذّر الإرسال الآن';
    case 'no_channel':
      return 'لا توجد قناة إرسال';
    default:
      return `رمز ${code}`;
  }
}

// orderCode is the short reference merchants see: the ID's last eight
// characters (the orders module's Code).
export const orderCode = (id: string) => id.slice(-8);

// The SMS placeholders, in the order the editor offers them.
export const smsVars: { key: string; ar: string; en: string }[] = [
  { key: 'name', ar: 'اسم العميل', en: 'Customer name' },
  { key: 'store', ar: 'اسم المتجر', en: 'Store name' },
  { key: 'ref', ar: 'رقم الطلب', en: 'Order number' },
  { key: 'total', ar: 'الإجمالي', en: 'Total' },
  { key: 'url', ar: 'رابط التأكيد', en: 'Confirm link' },
];

// smsPreview fills the placeholders with an example order, as the
// customer would read it.
export function smsPreview(body: string, locale: 'ar' | 'en', storeName: string): string {
  const sample: Record<string, string> =
    locale === 'ar'
      ? { name: 'سارة', store: storeName || 'متجرك', ref: 'YM2ECZRR', total: '500 ج.م', url: 'mona.iftahonline.com/o/k7Q2xR' }
      : { name: 'Sara', store: storeName || 'Your store', ref: 'YM2ECZRR', total: 'EGP 500', url: 'mona.iftahonline.com/o/k7Q2xR' };
  return body.replace(/\{\{(name|store|ref|total|url)\}\}/g, (_, k: string) => sample[k] ?? '');
}

// smsParts is how many SMS a text costs the customer's operator to
// carry: 70 characters in one (67 each when split) for Arabic, 160
// (153) for plain Latin.
export function smsParts(text: string): number {
  let unicode = false;
  for (let i = 0; i < text.length && !unicode; i++) unicode = text.charCodeAt(i) > 0x7f;
  const [one, each] = unicode ? [70, 67] : [160, 153];
  return text.length <= one ? 1 : Math.ceil(text.length / each);
}

export function partsLabel(n: number): string {
  if (n === 1) return 'رسالة واحدة';
  if (n === 2) return 'رسالتين';
  return `${String(n)} رسائل`;
}

// noticeText is a notice in the merchant's words; the order's number
// links to it.
export function noticeText(n: Notice): { before: string; after: string } {
  switch (n.kind) {
    case 'quota_nearly':
      if (n.feature)
        return { before: `استهلكت 80% من طلبات باقتك هذا الشهر (${String(n.used ?? 0)} من ${String(n.limit ?? 0)}).`, after: '' };
      return { before: `استهلكت 80% من رسائل هذا الشهر (${String(n.used ?? 0)} من ${String(n.limit ?? 0)}).`, after: '' };
    case 'quota_reached':
      if (n.feature) return { before: 'وصلت إلى حد طلبات باقتك هذا الشهر. الطلبات ما زالت تصلك؛ رقِّ باقتك لتبقى ضمن الحد.', after: '' };
      return { before: 'نفد رصيد رسائل هذا الشهر: لن تُرسل رسائل التأكيد حتى يتجدد.', after: '' };
    case 'order_confirmed':
      return { before: 'أكّد العميل الطلب', after: '.' };
    case 'order_cancelled':
      return { before: 'ألغى العميل الطلب', after: '.' };
    case 'order_auto_cancelled':
      return { before: 'أُلغي الطلب', after: ' تلقائيًا: لم يرد العميل في الوقت.' };
    case 'confirmation_unreached':
      return { before: 'لم تصل رسالة التأكيد لعميل الطلب', after: ': اتصل به بنفسك.' };
  }
}

// noticeTone picks the notice's icon: done, refused, or needs a look.
export function noticeTone(kind: Notice['kind']): 'ok' | 'no' | 'warn' {
  if (kind === 'order_confirmed') return 'ok';
  if (kind === 'order_cancelled' || kind === 'order_auto_cancelled') return 'no';
  return 'warn';
}

// confirmationLabel is the confirmation card's status pill.
export const confirmationLabel: Record<OrderConfirmation['status'], { label: string; cls: string }> = {
  pending: { label: 'بانتظار رده', cls: 's-pending' },
  confirmed: { label: 'أكّد الطلب', cls: 's-delivered' },
  cancelled: { label: 'ألغى الطلب', cls: 's-cancelled' },
  expired: { label: 'أُلغي لعدم الرد', cls: 's-cancelled' },
  skipped: { label: 'لم تُرسل: نفد الرصيد', cls: 's-pending' },
  unreached: { label: 'لم تصل الرسالة', cls: 's-pending' },
  stale: { label: 'تصرّفت قبل رده', cls: 'status' },
};

// ago says how long ago a notice came, in the words merchants use; past
// a day, the date.
export function ago(iso: string, now = Date.now()): string {
  const min = Math.floor((now - new Date(iso).getTime()) / 60000);
  if (min < 1) return 'الآن';
  const say = (n: number, one: string, two: string, few: string, many: string) =>
    n === 1 ? `منذ ${one}` : n === 2 ? `منذ ${two}` : `منذ ${String(n)} ${n <= 10 ? few : many}`;
  if (min < 60) return say(min, 'دقيقة', 'دقيقتين', 'دقائق', 'دقيقة');
  const h = Math.floor(min / 60);
  if (h < 24) return say(h, 'ساعة', 'ساعتين', 'ساعات', 'ساعة');
  return dateTime(iso);
}

// The bell's last read of a store's notices. Every screen mounts its own
// bell, so the read is kept here: a screen opened within a minute of the
// last read shows it without asking again (the unread count is a query).
export interface NoticeRead {
  at: number;
  items: Notice[];
  unread: number;
  next: string;
}

export const noticeTTL = 60_000;
const noticeReads = new Map<string, NoticeRead>();

export function recentNotices(storeId: string, now = Date.now()): NoticeRead | undefined {
  const r = noticeReads.get(storeId);
  return r && now - r.at < noticeTTL ? r : undefined;
}

export function keepNotices(storeId: string, r: NoticeRead): void {
  noticeReads.set(storeId, r);
}

// forgetNotices drops what was kept (tests; signing out).
export function forgetNotices(): void {
  noticeReads.clear();
}
