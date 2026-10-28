import type { components } from '../api/client';

import { count } from './home';

export type Section = components['schemas']['Section'];
export type Settings = Record<string, unknown>;

// A JSON Schema as the section types are written (draft 2020-12, the
// subset they use).
export interface Schema {
  type?: string;
  $ref?: string;
  enum?: string[];
  default?: unknown;
  format?: string;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  minItems?: number;
  maxItems?: number;
  properties?: Record<string, Schema>;
  required?: string[];
  items?: Schema;
}

// The section types the editor offers, in the add dialog's order (the
// approved mockup). product_landing belongs to landing pages. A type the
// server adds later still shows, under its own name (ADR-0032).
export const sectionTypes: { type: string; label: string; hint: string; icon: string }[] = [
  { type: 'hero', label: 'صورة رئيسية', hint: 'عنوان كبير وزر، فوق صورة منتج', icon: 'image' },
  { type: 'product_grid', label: 'شبكة منتجات', hint: 'الأحدث، أو الأكثر مبيعًا، أو من تصنيف، أو تختارها', icon: 'grid' },
  { type: 'category_tiles', label: 'مربعات التصنيفات', hint: 'صورة واسم لكل تصنيف', icon: 'circles' },
  { type: 'promo_tiles', label: 'عروض', hint: 'مربعات عروض بعنوان ورابط', icon: 'tiles' },
  { type: 'countdown', label: 'عدّاد تنازلي', hint: 'لعرض ينتهي في وقت محدد', icon: 'clock' },
  { type: 'reviews', label: 'آراء العملاء', hint: 'اقتباسات من عملائك', icon: 'star' },
  { type: 'trust_badges', label: 'مميزات المتجر', hint: 'الدفع عند الاستلام، الاسترجاع، الشحن', icon: 'shield' },
  { type: 'faq', label: 'أسئلة شائعة', hint: 'سؤال وجواب يُفتح بالضغط', icon: 'question' },
  { type: 'whatsapp_cta', label: 'زر واتساب', hint: '«محتاج مساعدة؟» يفتح محادثة', icon: 'whatsapp' },
];

// The landing pages' own type, offered first on them.
const landingType = { type: 'product_landing', label: 'منتج واحد', hint: 'صفحة بيع لمنتج: صوره وسعره ومميزاته وزر الطلب', icon: 'bag' };

export const homeTypes = sectionTypes.map((t) => t.type);
export const landingTypes = [landingType.type, ...homeTypes];

export function typeInfo(type: string): { type: string; label: string; hint: string; icon: string } | undefined {
  return type === landingType.type ? landingType : sectionTypes.find((t) => t.type === type);
}

export function typeLabel(type: string): string {
  return typeInfo(type)?.label ?? type;
}

export function typeIcon(type: string): string {
  return typeInfo(type)?.icon ?? 'grid';
}

// Field names in Arabic; a field the server adds later shows its key.
const fieldLabels: Record<string, string> = {
  badge: 'شارة صغيرة',
  heading: 'العنوان',
  subheading: 'نص تحت العنوان',
  button_label: 'نص الزر',
  button_link: 'الزر يفتح',
  image_product_id: 'الصورة من منتج',
  align: 'محاذاة النص',
  title: 'العنوان',
  subtitle: 'نص تحت العنوان',
  source: 'المنتجات المعروضة',
  category_id: 'التصنيف',
  product_ids: 'اختر المنتجات',
  category_ids: 'التصنيفات (فارغ: كلها)',
  limit: 'العدد',
  layout: 'طريقة العرض',
  ends_at: 'ينتهي في',
  hide_when_ended: 'يختفي حين ينتهي',
  items: 'العناصر',
  question: 'السؤال',
  answer: 'الجواب',
  label: 'الشارة',
  link: 'الرابط',
  tone: 'اللون',
  name: 'الاسم',
  location: 'المدينة',
  rating: 'التقييم (1–5)',
  text: 'النص',
  icon: 'الرمز',
  product_id: 'المنتج',
  headline: 'العنوان الرئيسي',
  highlights: 'المميزات',
  show_description: 'اعرض وصف المنتج',
  cta_label: 'نص زر الطلب',
};

export function fieldLabel(key: string): string {
  return fieldLabels[key] ?? key;
}

// Choices of the enums, in Arabic (RTL: "start" is the right).
const enumLabels: Record<string, Record<string, string>> = {
  align: { start: 'يمين', center: 'وسط' },
  source: { newest: 'الأحدث', bestsellers: 'الأكثر مبيعًا', category: 'من تصنيف', manual: 'أختارها بنفسي' },
  layout: { grid: 'شبكة', carousel: 'شريط يتحرك' },
  tone: { amber: 'كهرماني', green: 'أخضر', blue: 'أزرق', primary: 'لون المتجر' },
  icon: { shipping: 'الشحن', cod: 'الدفع عند الاستلام', returns: 'الاسترجاع', secure: 'دفع آمن', support: 'الدعم', quality: 'الجودة' },
};

export function enumLabel(key: string, value: string): string {
  return enumLabels[key]?.[value] ?? value;
}

// The badge says it is optional, as the mockup's «شارة صغيرة (اختياري)».
export const optionalNote = (key: string) => key === 'badge';

// What the shop shows for a field left empty, as its placeholder.
export const placeholders: Record<string, string> = { button_label: 'تسوّق الآن', cta_label: 'اطلب الآن' };

// The extra line under a hero's image field (the approved mockup).
export const fieldNotes: Record<string, string> = {
  image_product_id: 'نستخدم الصورة الرئيسية للمنتج، فتبقى محدّثة.',
};

// phoneSummary is the shorter line of a closed section on phones (the
// approved mockup): the grid's title without its count, nothing for the
// WhatsApp button.
export function phoneSummary(s: Section): string {
  return s.type === 'whatsapp_cta' ? '' : (summary(s).split(' · ')[0] ?? '');
}

// summary is the line under a section's name in the list.
export function summary(s: Section): string {
  const v = (s.settings ?? {});
  const str = (k: string) => (typeof v[k] === 'string' ? v[k] : '');
  const items = Array.isArray(v['items']) ? v['items'].length : 0;
  switch (s.type) {
    case 'hero':
      return str('heading');
    case 'product_grid': {
      const n = typeof v['limit'] === 'number' ? v['limit'] : 8;
      return `${str('title') || gridTitle(str('source'))} · ${count(n, 'منتج واحد', 'منتجان', 'منتجات', 'منتجًا')}`;
    }
    case 'category_tiles': {
      const n = Array.isArray(v['category_ids']) ? v['category_ids'].length : 0;
      return n ? count(n, 'تصنيف واحد', 'تصنيفان', 'تصنيفات', 'تصنيفًا') : 'كل التصنيفات';
    }
    case 'reviews':
      return count(items, 'رأي واحد', 'رأيان', 'آراء', 'رأيًا');
    case 'faq':
      return count(items, 'سؤال واحد', 'سؤالان', 'أسئلة', 'سؤالًا');
    case 'trust_badges':
    case 'promo_tiles':
      return count(items, 'عنصر واحد', 'عنصران', 'عناصر', 'عنصرًا');
    case 'countdown':
      return str('title');
    case 'whatsapp_cta':
      return str('title') || defaults.whatsapp;
    case 'product_landing':
      return str('headline');
  }
  return '';
}

// The shop's texts for a setting left empty (the storefront's Arabic
// catalogue), so the list and the preview say what shoppers will see.
export const defaults = {
  button: 'تسوّق الآن',
  order: 'اطلب الآن',
  categories: 'تسوّق حسب التصنيف',
  reviews: 'آراء العملاء',
  faq: 'الأسئلة الشائعة',
  countdown: 'ينتهي العرض خلال',
  whatsapp: 'محتاج مساعدة في الطلب؟',
  whatsappText: 'كلّمنا على واتساب ونرد عليك بسرعة',
  badges: { shipping: 'شحن سريع', cod: 'الدفع عند الاستلام', returns: 'استرجاع سهل', secure: 'دفع آمن', support: 'دعم العملاء', quality: 'جودة مضمونة' } as Record<string, string>,
};

export function gridTitle(source: string): string {
  return source === 'bestsellers' ? 'الأكثر مبيعًا' : source === 'newest' || source === '' ? 'وصل حديثًا' : 'المنتجات';
}

// newID is a section ID the server accepts (lower-case letters, digits
// and "-"): stable from the moment it is added, so edits made while a
// draft is saving keep their place.
export function newID(): string {
  return `s${Math.random().toString(36).slice(2, 12)}`;
}

// starter is a new section's settings: the schema's defaults and what a
// required list needs to start from.
export function starter(type: string, schema: Schema | undefined, now = new Date()): Settings {
  const out: Settings = {};
  for (const [k, p] of Object.entries(schema?.properties ?? {})) {
    if (p.default !== undefined) out[k] = p.default;
  }
  if (type === 'product_grid') out['source'] ??= 'newest';
  if (type === 'countdown') out['ends_at'] = new Date(now.getTime() + 7 * 86_400_000).toISOString();
  if (schema?.required?.includes('items')) out['items'] = [itemStarter(schema.properties?.['items']?.items)];
  return out;
}

export function itemStarter(schema: Schema | undefined): Settings {
  const out: Settings = {};
  for (const [k, p] of Object.entries(schema?.properties ?? {})) {
    if (p.default !== undefined) out[k] = p.default;
    else if (p.enum?.[0] !== undefined) out[k] = p.enum[0];
    else if (k === 'rating') out[k] = 5;
  }
  return out;
}

// kindOf names how a field is edited, from its schema and key.
export type FieldKind = 'text' | 'longtext' | 'link' | 'product' | 'category' | 'products' | 'categories' | 'lines' | 'enum' | 'segmented' | 'number' | 'bool' | 'datetime' | 'items';

export function kindOf(key: string, p: Schema): FieldKind {
  const ref = p.$ref ?? '';
  if (p.type === 'array') {
    if (p.items?.properties) return 'items';
    if (!(p.items?.$ref ?? '').endsWith('/id')) return 'lines';
    return key.startsWith('category') ? 'categories' : 'products';
  }
  if (ref.endsWith('/link')) return 'link';
  if (ref.endsWith('/id')) return key.startsWith('category') ? 'category' : 'product';
  if (p.enum) return key === 'align' ? 'segmented' : 'enum';
  if (p.type === 'integer') return 'number';
  if (p.type === 'boolean') return 'bool';
  if (p.format === 'date-time') return 'datetime';
  if (p.type === 'string' && !ref && (p.maxLength ?? 0) > 300) return 'longtext';
  return 'text';
}

export interface FieldError {
  field: string;
  code: string;
  message: string;
}

// fieldErrors names what the server refused, in Arabic, by field
// ("sections[2].settings.items[0].name"); the server's messages are
// English.
export function fieldErrors(list: FieldError[]): Record<string, string> {
  return Object.fromEntries(list.map((f) => [f.field, errorText(f)]));
}

function errorText(f: FieldError): string {
  const link = f.field.endsWith('link');
  switch (f.code) {
    case 'required':
    case 'minLength':
      return 'مطلوب';
    case 'dependentRequired':
      return link ? 'اختر أين يفتح الزر' : 'مطلوب';
    case 'maxLength':
    case 'length':
      return 'أطول من المسموح';
    case 'taken':
      return 'صفحة أخرى تستخدم هذا الرابط';
    case 'pattern':
      return link ? 'رابط غير مقبول: يبدأ بـ / أو https://' : 'فيه رموز غير مسموحة (مثل سطر جديد)';
    case 'minimum':
    case 'maximum':
    case 'type':
      return 'رقم خارج المسموح';
    case 'minItems':
      return 'أضف عنصرًا واحدًا على الأقل';
    case 'maxItems':
      return 'أكثر من المسموح';
    case 'too_many':
      return 'الحد 30 قسمًا في الصفحة';
    case 'too_large':
      return 'محتوى الأقسام أكبر من المسموح';
  }
  return f.field === 'slug' ? 'حروف وأرقام وشرطات (-) فقط، بلا مسافات' : 'قيمة غير مقبولة';
}

// sectionErrors is one section's errors, named from its settings
// ("items[0].name"; "" for the settings as a whole).
export function sectionErrors(all: Record<string, string>, index: number): Record<string, string> {
  const at = `sections[${String(index)}]`;
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(all)) {
    if (k.startsWith(`${at}.settings.`)) out[k.slice(at.length + 10)] = v;
    else if (k.startsWith(`${at}.settings[`)) out[k.slice(at.length + 9)] = v;
    else if (k === `${at}.settings` || k === at || k.startsWith(`${at}.`)) out[''] = v;
  }
  return out;
}
