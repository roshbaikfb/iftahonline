import type { components } from '../api/client';
import { featureKindLabel, kindLabel, type Feature } from './plans';

export type Entry = components['schemas']['AdminAuditEntry'];

// Family groups the actions by what they touch, for the icon and colour.
export type Family = 'store' | 'billing' | 'content' | 'security';

interface Action {
  label: string;
  family: Family;
  danger?: boolean;
}

// actions is every action the console records, in words.
const actions: Record<string, Action> = {
  'admin.created': { label: 'أضاف أدمن', family: 'security' },
  'admin.signed_in': { label: 'دخل الكونسول', family: 'security' },
  'admin.signed_out': { label: 'خرج من الكونسول', family: 'security' },
  'store.suspended': { label: 'أوقف متجرًا', family: 'store', danger: true },
  'store.reactivated': { label: 'أعاد تفعيل متجر', family: 'store' },
  'store.impersonated': { label: 'فتح جلسة دعم في متجر', family: 'store' },
  'store.feature_overridden': { label: 'غيّر ميزة لمتجر', family: 'store' },
  'store.feature_override_cleared': { label: 'أزال استثناء ميزة لمتجر', family: 'store' },
  'store.agency_joined': { label: 'ضمّ متجرًا لوكالة', family: 'store' },
  'store.agency_left': { label: 'أخرج متجرًا من وكالة', family: 'store' },
  'plan.published': { label: 'نشر إصدارًا جديدًا من باقة', family: 'billing' },
  'plan.retired': { label: 'أوقف بيع باقة', family: 'billing', danger: true },
  'feature.saved': { label: 'حفظ ميزة في الكتالوج', family: 'billing' },
  'agency.created': { label: 'أنشأ وكالة', family: 'billing' },
  'invoice.settled': { label: 'سجّل فاتورة مدفوعة بتحويل', family: 'billing' },
  'payment.refunded': { label: 'سجّل استرداد دفعة زائدة', family: 'billing' },
  'billing.founding_saved': { label: 'حفظ برنامج التجار المؤسسين', family: 'billing' },
  'ads.settings_saved': { label: 'حفظ إعدادات الإعلانات', family: 'content' },
  'ads.banner_created': { label: 'أضاف بانرًا إعلانيًا', family: 'content' },
  'ads.banner_active_set': { label: 'غيّر ظهور بانر إعلاني', family: 'content' },
  'site.settings_saved': { label: 'حفظ إعدادات الموقع', family: 'content' },
  'site.story_saved': { label: 'حفظ قصة المنصة', family: 'content' },
  'site.image_set': { label: 'غيّر صورة في الموقع', family: 'content' },
  'site.image_removed': { label: 'أزال صورة من الموقع', family: 'content' },
  'site.faq_created': { label: 'أضاف سؤالًا شائعًا', family: 'content' },
  'site.faq_updated': { label: 'عدّل سؤالًا شائعًا', family: 'content' },
  'site.faq_deleted': { label: 'حذف سؤالًا شائعًا', family: 'content', danger: true },
  'site.person_created': { label: 'أضاف شخصًا في «من نحن»', family: 'content' },
  'site.person_updated': { label: 'عدّل شخصًا في «من نحن»', family: 'content' },
  'site.person_deleted': { label: 'حذف شخصًا من «من نحن»', family: 'content', danger: true },
  'site.testimonial_created': { label: 'أضاف رأي عميل', family: 'content' },
  'site.testimonial_updated': { label: 'عدّل رأي عميل', family: 'content' },
  'site.testimonial_deleted': { label: 'حذف رأي عميل', family: 'content', danger: true },
  'site.guide_created': { label: 'أضاف دليلًا', family: 'content' },
  'site.guide_updated': { label: 'عدّل دليلًا', family: 'content' },
  'site.guide_deleted': { label: 'حذف دليلًا', family: 'content', danger: true },
};

const familyOf = (action: string): Family => {
  const prefix = action.split('.')[0];
  if (prefix === 'store') return 'store';
  if (prefix === 'site' || prefix === 'ads') return 'content';
  if (prefix === 'admin') return 'security';
  return 'billing';
};

const str = (v: unknown) => (typeof v === 'string' ? v : '');
const labelOf = (labels: Record<string, string>, v: unknown): string => (Object.hasOwn(labels, str(v)) ? (labels[str(v)] ?? str(v)) : str(v));
const num = (v: unknown) => (typeof v === 'number' ? v : undefined);

// Described is an entry as the log shows it.
export interface Described {
  label: string;
  family: Family;
  danger: boolean;
  target: { text: string; to?: string } | undefined;
  reason: string;
  chips: string[];
  who: string;
}

// target names what an entry acted on, and where the console shows it.
function target(e: Entry): Described['target'] {
  const m = e.metadata;
  switch (e.target_type) {
    case 'store':
      return { text: e.store_name ?? (str(m['slug']) || 'صفحة المتجر'), to: `/stores/${e.target_id}` };
    case 'plan':
      return { text: e.target_id, to: `/plans/${e.target_id}` };
    case 'feature':
      return { text: e.target_id, to: '/plans?tab=features' };
    case 'agency':
      return { text: str(m['name']) || 'وكالة', to: `/agencies/${e.target_id}` };
    case 'invoice':
      return str(m['store']) ? { text: e.store_name ? `فاتورة «${e.store_name}»` : 'فاتورة المتجر', to: `/stores/${str(m['store'])}` } : { text: 'فاتورة' };
    case 'site_guide':
      return { text: str(m['slug']) || 'دليل', to: `/guides/${e.target_id}` };
    case 'site_faq':
      return { text: str(m['question']) || 'سؤال', to: '/site' };
    case 'site_person':
    case 'site_testimonial':
      return { text: str(m['name']) || 'الموقع', to: '/site' };
    case 'site':
    case 'site_image':
      return { text: 'الموقع التعريفي', to: '/site' };
    case 'ad_banner':
      return { text: str(m['title']) || 'بانر', to: '/ads' };
    case 'ads':
      return { text: 'الإعلانات', to: '/ads' };
    case 'founding_program':
      return { text: 'التجار المؤسسون', to: '/founding' };
    default:
      return undefined; // a session, an admin: nothing to open
  }
}

// chips are an entry's details beside its reason, in words; a key the
// console does not know shows as it is.
function chips(e: Entry, features: Feature[]): string[] {
  const out: string[] = [];
  const feature = features.find((f) => f.key === str(e.metadata['feature']));
  for (const [k, v] of Object.entries(e.metadata)) {
    switch (k) {
      case 'reason':
      case 'slug':
      case 'name':
      case 'name_en':
      case 'store':
      case 'agency':
      case 'title':
      case 'question':
      case 'link_url':
      case 'publisher_id':
      case 'video_id':
        break; // the reason has its line; the rest names the target or is an ID
      case 'feature':
        if (e.action !== 'store.feature_overridden') out.push(feature?.name_ar ?? str(v));
        break;
      case 'value':
        if (e.metadata['unlimited'] === true) break;
        if (feature?.kind === 'flag') out.push(`${feature.name_ar}: ${num(v) ? 'متاحة' : 'غير متاحة'}`);
        else out.push(`${feature?.name_ar ?? str(e.metadata['feature'])}: ${(num(v) ?? 0).toLocaleString('en-US')}`);
        break;
      case 'unlimited':
        if (v === true) out.push(`${feature?.name_ar ?? str(e.metadata['feature'])}: بلا حد`);
        break;
      case 'kind':
        if (e.action === 'feature.saved') out.push(labelOf(featureKindLabel, v));
        else if (e.target_type === 'plan') out.push(labelOf(kindLabel, v));
        break;
      case 'prices':
        out.push(`${String(num(v) ?? 0)} أسعار`);
        break;
      case 'grants':
        out.push(`${String(num(v) ?? 0)} ميزة`);
        break;
      case 'minutes':
        out.push(`${String(num(v) ?? 0)} دقيقة`);
        break;
      case 'write':
        out.push(v === true ? 'مسموح بالتعديل' : 'للقراءة فقط');
        break;
      case 'reference':
        out.push(`المرجع: ${str(v)}`);
        break;
      case 'enabled':
        out.push(v === true ? 'البرنامج مفعّل' : 'البرنامج متوقف');
        break;
      case 'seats':
        out.push(`${String(num(v) ?? 0)} مقعد`);
        break;
      case 'discount_bps':
        out.push(`خصم ${String((num(v) ?? 0) / 100)}%`);
        break;
      case 'adsense_enabled':
        out.push(v === true ? 'AdSense مفعّل' : 'AdSense متوقف');
        break;
      case 'active':
        out.push(v === true ? 'ظاهر' : 'متوقف');
        break;
      case 'placement':
        out.push(`المكان: ${str(v)}`);
        break;
      case 'currency':
        out.push(str(v));
        break;
      case 'plan_code':
        out.push(`باقة ${str(v)}`);
        break;
      case 'status':
        out.push(v === 'published' ? 'منشور' : v === 'draft' ? 'مسودة' : str(v));
        break;
      default:
        out.push(`${k}: ${typeof v === 'string' ? v : JSON.stringify(v)}`);
    }
  }
  return out;
}

// describe turns an entry into the log's line: what was done, to what,
// why, the details, and who (the reader is «أنت», another admin by
// name).
export function describe(e: Entry, me: string, features: Feature[] = []): Described {
  const a = actions[e.action];
  return {
    label: a?.label ?? e.action,
    family: a?.family ?? familyOf(e.action),
    danger: a?.danger ?? false,
    target: target(e),
    reason: str(e.metadata['reason']),
    chips: chips(e, features),
    who: !e.admin_id ? 'النظام' : e.admin_id === me ? 'أنت' : (e.admin_name ?? 'أدمن آخر'),
  };
}

// dayOf is the heading an entry falls under: today, yesterday, or its
// date.
export function dayOf(iso: string, now: Date): string {
  const d = new Date(iso);
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((start(now) - start(d)) / 86_400_000);
  if (diff === 0) return 'اليوم';
  if (diff === 1) return 'أمس';
  return d.toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', ...(d.getFullYear() === now.getFullYear() ? {} : { year: 'numeric' }) });
}
