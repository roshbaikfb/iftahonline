import type { components } from '../api/client';

export type Member = components['schemas']['AdminTeamMember'];
export type Invite = components['schemas']['AdminInvite'];
export type Role = components['schemas']['AdminRole'];

// roles are ADR-0077's five, the most powerful first, with what each may do.
export const roles: { value: Role; label: string; can: string }[] = [
  { value: 'owner', label: 'المالك', can: 'كل شيء، ومعه إدارة الفريق وسجل الأدمن.' },
  { value: 'operations', label: 'العمليات والدعم', can: 'المتاجر: إيقاف وتفعيل، جلسة دعم، استثناءات الميزات. يرى فواتير المتجر دون تسويتها.' },
  { value: 'finance', label: 'المالية', can: 'الفواتير والمدفوعات، التسوية بتحويل، الاسترداد، تصدير CSV، الباقات، المؤسسون، الوكالات.' },
  { value: 'content', label: 'المحتوى', can: 'الموقع التعريفي، الأدلة، الإعلانات.' },
  { value: 'viewer', label: 'مشاهدة فقط', can: 'يفتح الصفحات دون أي إجراء. لا يرى أرقام التجار.' },
];

export const roleLabel = (r: Role) => roles.find((x) => x.value === r)?.label ?? r;

// inviteLink is the page an invitee opens. The token sits in the fragment,
// so it never reaches a server log (ADR-0078).
export const inviteLink = (token: string, origin = globalThis.location.origin) => `${origin}/console/invite#${token}`;

// expiresIn words how long an invite still works, or that it no longer does.
export function expiresIn(iso: string, now: Date): string {
  const hours = Math.floor((new Date(iso).getTime() - now.getTime()) / 3_600_000);
  if (hours < 0) return 'انتهت صلاحيتها: أنشئ رابطًا جديدًا';
  if (hours < 1) return 'تنتهي خلال أقل من ساعة';
  if (hours < 24) return `تنتهي بعد ${String(hours)} ساعة`;
  const days = Math.floor(hours / 24);
  return days === 1 ? 'تنتهي بعد يوم' : days === 2 ? 'تنتهي بعد يومين' : `تنتهي بعد ${String(days)} أيام`;
}

// teamFirst sorts the signed-in admin first, then the active by name.
export function teamFirst(members: Member[], me: string): Member[] {
  return [...members].sort((a, b) => Number(b.id === me) - Number(a.id === me) || Number(b.active) - Number(a.active) || a.name.localeCompare(b.name, 'ar'));
}
