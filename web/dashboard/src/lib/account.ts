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
    if (p.errors?.some((f) => f.field === 'email')) return 'اكتب بريدًا إلكترونيًا صحيحًا.';
    if (p.code === 'rate_limited') return 'طلبات كثيرة. انتظر دقيقة ثم أعد المحاولة.';
    if (p.code === 'link_not_found') return 'انتهت صلاحية هذا الرابط أو استُخدم.';
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
