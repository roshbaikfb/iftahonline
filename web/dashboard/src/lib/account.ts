import { problemMessage } from './problem';

interface FieldError {
  field: string;
  code: string;
  message: string;
}

const passwordInvalid = 'كلمة المرور فيها رموز غير مقبولة.';
const passwordCodes: Partial<Record<string, string>> = {
  too_short: 'كلمة المرور قصيرة: 10 أحرف على الأقل.',
  too_long: 'كلمة المرور طويلة: 128 حرفًا على الأكثر.',
  too_weak: 'كلمة المرور سهلة التخمين أو تشبه بريدك. اختر غيرها.',
  invalid: passwordInvalid,
};

// accountMessage is an account screen's error in Arabic (D3, D4): a
// refused password says why, too many tries says to wait, a used or
// expired link says so; anything else is problemMessage.
export function accountMessage(error: unknown): string {
  const p = error as { code?: string; errors?: FieldError[] } | undefined;
  if (p && typeof p === 'object') {
    const pw = p.errors?.find((f) => f.field === 'password');
    if (pw) return passwordCodes[pw.code] ?? passwordInvalid;
    const field = (name: string) => p.errors?.some((f) => f.field === name) ?? false;
    if (field('current_password')) return 'كلمة المرور الحالية غير صحيحة.';
    if (field('email')) return 'اكتب بريدًا إلكترونيًا صحيحًا.';
    if (field('name')) return 'اكتب اسمك (100 حرف على الأكثر).';
    if (field('phone')) return 'اكتب رقم موبايل صحيحًا، أو اكتبه كاملًا بمفتاح الدولة مثل +966501234567.';
    const known: Partial<Record<string, string>> = {
      rate_limited: 'طلبات كثيرة. انتظر دقيقة ثم أعد المحاولة.',
      link_not_found: 'انتهت صلاحية هذا الرابط أو استُخدم.',
      unauthenticated: 'الكود غير صحيح أو انتهت صلاحيته.',
      unavailable: 'إرسال الأكواد غير متاح الآن. جرّب بعد قليل.',
      phone_taken: 'هذا الرقم مسجّل لحساب آخر.',
      mfa_enabled: 'التحقق بخطوتين مفعّل بالفعل.',
      mfa_disabled: 'التحقق بخطوتين غير مفعّل.',
      forbidden: 'لا تتغير بيانات الحساب من جلسة الدعم.',
    };
    const said = p.code ? known[p.code] : undefined;
    if (said) return said;
  }
  return problemMessage(error);
}

// linkToken is the one-time link's secret: the URL fragment, so it never
// reaches a server log.
export function linkToken(hash: string): string {
  try {
    return decodeURIComponent(hash.replace(/^#/, ''));
  } catch {
    return '';
  }
}

// Phone regions the API reads national numbers in.
const regions = ['EG', 'SA', 'AE', 'KW', 'QA', 'BH', 'OM'] as const;
export type Region = (typeof regions)[number];
const zones: Partial<Record<string, Region>> = {
  'Africa/Cairo': 'EG',
  'Asia/Riyadh': 'SA',
  'Asia/Dubai': 'AE',
  'Asia/Kuwait': 'KW',
  'Asia/Qatar': 'QA',
  'Asia/Bahrain': 'BH',
  'Asia/Muscat': 'OM',
};

// phoneRegion is the country a number without "+" is read in: the store's
// when it has one we know, else the browser's time zone, else Egypt.
export function phoneRegion(country?: string, zone = Intl.DateTimeFormat().resolvedOptions().timeZone): Region {
  const known = regions.find((r) => r === country);
  return known ?? zones[zone] ?? 'EG';
}

// maskPhone shows a typed number with its middle hidden ("010••••5678").
export function maskPhone(raw: string): string {
  const d = raw.replace(/[^\d+]/g, '');
  if (d.length < 8) return d;
  return `${d.slice(0, 3)}${'•'.repeat(d.length - 7)}${d.slice(-4)}`;
}

// The User-Agent marks a session's device is named by, first match wins.
const systems: [string, string][] = [
  ['Android', 'Android'],
  ['iPhone', 'iPhone'],
  ['iPad', 'iPhone'],
  ['Windows', 'Windows'],
  ['Macintosh', 'Mac'],
  ['Mac OS X', 'Mac'],
  ['Linux', 'Linux'],
];
const browsers: [string, string][] = [
  ['Edg/', 'Edge'],
  ['OPR/', 'Opera'],
  ['Firefox/', 'Firefox'],
  ['Chrome/', 'Chrome'],
  ['Safari/', 'Safari'],
];
const apps = ['Dart/', 'okhttp', 'iftahonline'];

// deviceName names a session's device from its User-Agent the way people
// say it ("Chrome على Windows", "تطبيق التاجر على Android").
export function deviceName(ua: string): string {
  const pick = (marks: [string, string][]) => marks.find(([mark]) => ua.includes(mark))?.[1] ?? '';
  const os = pick(systems);
  if (apps.some((mark) => ua.includes(mark))) return os ? `تطبيق التاجر على ${os}` : 'تطبيق التاجر';
  const browser = pick(browsers);
  if (browser && os) return `${browser} على ${os}`;
  return browser || os || 'جهاز غير معروف';
}

// groupKey writes a TOTP seed in fours, easier to type ("JBSW Y3DP …").
export function groupKey(secret: string): string {
  return secret.replace(/(.{4})(?=.)/g, '$1 ');
}
