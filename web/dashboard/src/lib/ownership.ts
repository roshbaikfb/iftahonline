import { problemMessage } from './problem';

// Moving the store's ownership (C2).

// transferMessage says why a move was refused, in Arabic.
export function transferMessage(error: unknown): string {
  const p = error as { status?: number; code?: string; errors?: { field: string; code: string }[] } | undefined;
  if (p?.errors?.some((f) => f.field === 'password')) return 'كلمة المرور غير صحيحة.';
  if (p?.status === 401) return 'كود التحقق غير صحيح. اكتب الكود الذي يظهر الآن في التطبيق، أو كود استرداد.';
  if (p?.code === 'member_disabled') return 'حساب هذا العضو موقوف، فلا تُنقل إليه الملكية.';
  if (p?.code === 'member_not_found') return 'هذا العضو لم يعد في فريق المتجر.';
  return problemMessage(error);
}
