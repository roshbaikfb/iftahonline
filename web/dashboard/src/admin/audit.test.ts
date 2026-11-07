import { describe as group, expect, it } from 'vitest';

import { dayOf, describe, type Entry } from './audit';
import type { Feature } from './plans';

const me = '01M5ADMIN000000000000000AA';
const store = '01M5STORE0000000000000000A';
const page = { text: 'صفحة المتجر', to: `/stores/${store}` };
const features: Feature[] = [
  { key: 'orders_per_month', kind: 'monthly', name_ar: 'الطلبات في الشهر', name_en: 'Orders', sort: 10, enforced: true },
  { key: 'ad_free', kind: 'flag', name_ar: 'متجر بدون إعلانات', name_en: 'No ads', sort: 100, enforced: true },
];

function entry(action: string, over: Partial<Entry> = {}): Entry {
  return { id: 'E1', occurred_at: '2026-11-03T10:00:00Z', admin_id: me, action, target_type: 'store', target_id: store, ip_prefix: '10.0.0.0/24', metadata: {}, ...over };
}

group('describe', () => {
  it.each<[string, Partial<Entry>, { label: string; family: string; danger?: boolean; target?: { text: string; to?: string }; chips?: string[] }]>([
    ['store.suspended', { metadata: { reason: 'بلاغ', slug: 'mona' } }, { label: 'أوقف متجرًا', family: 'store', danger: true, target: { text: 'mona', to: `/stores/${store}` } }],
    ['store.impersonated', { metadata: { minutes: 30, write: false } }, { label: 'فتح جلسة دعم في متجر', family: 'store', target: { text: 'صفحة المتجر', to: `/stores/${store}` }, chips: ['30 دقيقة', 'للقراءة فقط'] }],
    ['store.member_disabled', { metadata: { reason: 'حساب مسروق', user_id: '01M5USER0000000000000000AA' } }, { label: 'أوقف حساب عضو متجر', family: 'store', danger: true, target: { text: 'صفحة المتجر', to: `/stores/${store}` }, chips: [] }],
    ['store.member_mfa_reset_requested', { metadata: { user_id: '01M5USER0000000000000000AA' } }, { label: 'طلب إلغاء التحقق بخطوتين لعضو متجر (بعد 24 ساعة)', family: 'store', danger: true, target: { text: 'صفحة المتجر', to: `/stores/${store}` }, chips: [] }],
    [
      'store.feature_overridden',
      { metadata: { feature: 'ad_free', value: 0, unlimited: false } },
      { label: 'غيّر ميزة لمتجر', family: 'store', target: page, chips: ['متجر بدون إعلانات: غير متاحة'] },
    ],
    ['store.feature_overridden', { metadata: { feature: 'orders_per_month', value: 3000, unlimited: false } }, { label: 'غيّر ميزة لمتجر', family: 'store', target: page, chips: ['الطلبات في الشهر: 3,000'] }],
    ['store.feature_overridden', { metadata: { feature: 'orders_per_month', value: 0, unlimited: true } }, { label: 'غيّر ميزة لمتجر', family: 'store', target: page, chips: ['الطلبات في الشهر: بلا حد'] }],
    [
      'plan.published',
      { target_type: 'plan', target_id: 'pro', metadata: { kind: 'subscription', prices: 6, grants: 12 } },
      { label: 'نشر إصدارًا جديدًا من باقة', family: 'billing', target: { text: 'pro', to: '/plans/pro' }, chips: ['اشتراك', '6 أسعار', '12 ميزة'] },
    ],
    ['plan.retired', { target_type: 'plan', target_id: 'gold' }, { label: 'أوقف بيع باقة', family: 'billing', danger: true, target: { text: 'gold', to: '/plans/gold' } }],
    [
      'billing.founding_saved',
      { target_type: 'founding_program', target_id: 'program', metadata: { enabled: true, seats: 100, discount_bps: 2000 } },
      { label: 'حفظ برنامج التجار المؤسسين', family: 'billing', target: { text: 'التجار المؤسسون', to: '/founding' }, chips: ['البرنامج مفعّل', '100 مقعد', 'خصم 20%'] },
    ],
    [
      'invoice.settled',
      { target_type: 'invoice', target_id: 'INV1', metadata: { reference: 'TRX-1', store } },
      { label: 'سجّل فاتورة مدفوعة بتحويل', family: 'billing', target: { text: 'فاتورة المتجر', to: '/billing/invoices/INV1' }, chips: ['المرجع: TRX-1'] },
    ],
    [
      'invoice.settled',
      { target_type: 'invoice', target_id: 'INV2', metadata: { reference: 'TRX-2', account: 'A1', number: 'INV-2026-000183' } },
      { label: 'سجّل فاتورة مدفوعة بتحويل', family: 'billing', target: { text: 'فاتورة INV-2026-000183', to: '/billing/invoices/INV2' }, chips: ['المرجع: TRX-2'] },
    ],
    [
      'admin.role_changed',
      { target_type: 'admin', target_id: 'A2', metadata: { reason: 'انتقل', name: 'سارة', from: 'operations', to: 'finance' } },
      { label: 'غيّر دور أدمن', family: 'security', target: { text: 'سارة', to: '/team' }, chips: ['من العمليات والدعم إلى المالية'] },
    ],
    [
      'admin.searched_owner',
      { target_type: 'search', target_id: '', metadata: { query: '+20101••••678' } },
      { label: 'بحث عن صاحب متجر برقمه أو إيميله', family: 'security', chips: ['«+20101••••678»'] },
    ],
    [
      'admin.invited',
      { target_type: 'admin', target_id: 'A3', metadata: { reason: 'انضمت', name: 'نور', role: 'content' } },
      { label: 'دعا أدمن جديدًا', family: 'security', target: { text: 'نور', to: '/team' }, chips: ['المحتوى'] },
    ],
    [
      'billing.invoices_exported',
      { target_type: 'invoice', target_id: '', metadata: { status: 'overdue', currency: '', q: 'النيل' } },
      { label: 'صدّر الفواتير (CSV)', family: 'billing', target: { text: 'الفواتير', to: '/billing' }, chips: ['متأخرة', 'بحث: النيل'] },
    ],
    [
      'site.guide_updated',
      { target_type: 'site_guide', target_id: 'G1', metadata: { slug: 'cod', status: 'published' } },
      { label: 'عدّل دليلًا', family: 'content', target: { text: 'cod', to: '/guides/G1' }, chips: ['منشور'] },
    ],
    ['admin.signed_in', { target_type: 'session', target_id: 'S1' }, { label: 'دخل الكونسول', family: 'security' }],
    // An action the console does not know yet still shows, as it is.
    ['store.renamed', { metadata: { old_name: 'أ' } }, { label: 'store.renamed', family: 'store', target: page, chips: ['old_name: أ'] }],
    ['billing.something', { target_type: 'thing', target_id: 'x' }, { label: 'billing.something', family: 'billing' }],
  ])('%s', (action, over, want) => {
    const got = describe(entry(action, over), me, features);
    expect(got.label).toBe(want.label);
    expect(got.family).toBe(want.family);
    expect(got.danger).toBe(want.danger ?? false);
    expect(got.target).toEqual(want.target);
    expect(got.chips).toEqual(want.chips ?? []);
  });

  it('puts the reason on its own line and names who did it', () => {
    expect(describe(entry('store.suspended', { metadata: { reason: 'بلاغ احتيال' } }), me).reason).toBe('بلاغ احتيال');
    expect(describe(entry('store.suspended'), me).who).toBe('أنت');
    expect(describe(entry('store.suspended', { admin_id: '01M5ADMIN000000000000000BB' }), me).who).toBe('أدمن آخر');
    expect(describe(entry('store.suspended', { admin_id: '01M5ADMIN000000000000000BB', admin_name: 'سارة' }), me).who).toBe('سارة');
    // The server names the store when it can.
    expect(describe(entry('store.suspended', { store_name: 'متجر الأناقة', metadata: { slug: 'alanaqa' } }), me).target).toEqual({ text: 'متجر الأناقة', to: `/stores/${store}` });
    expect(describe(entry('invoice.settled', { target_type: 'invoice', target_id: 'I1', store_name: 'متجر الأناقة', metadata: { store } }), me).target).toEqual({
      text: 'فاتورة «متجر الأناقة»',
      to: '/billing/invoices/I1',
    });
    const system = entry('admin.created');
    delete system.admin_id;
    expect(describe(system, me).who).toBe('النظام');
  });
});

group('dayOf', () => {
  const now = new Date(2026, 10, 3, 15, 0);
  it.each([
    [new Date(2026, 10, 3, 1, 0), 'اليوم'],
    [new Date(2026, 10, 2, 23, 59), 'أمس'],
    [new Date(2026, 9, 31, 12, 0), '31 أكتوبر'],
    [new Date(2025, 11, 31, 12, 0), '31 ديسمبر 2025'],
  ])('%s', (d, want) => {
    expect(dayOf(d.toISOString(), now)).toBe(want);
  });
});
