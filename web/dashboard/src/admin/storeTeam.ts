import { api, type components } from '../api/client';
import { roleLabel as merchantRole } from '../lib/settings';
import { hoursUntil } from '../lib/supportRequests';
import { dateTimeAr } from './lib';
import { roleLabel as adminRole } from './team';

export type StoreMember = components['schemas']['AdminStoreMember'];
export type StoreInvite = components['schemas']['AdminStoreInvite'];
export type SupportRequest = components['schemas']['AdminSupportRequest'];
type Team = components['schemas']['AdminStoreTeam'];

// fetchTeam reads a store's team, its invitations and support's requests.
export const fetchTeam = (storeId: string) => api.GET('/admin/v1/stores/{store_id}/team', { params: { path: { store_id: storeId } } });

// memberRole names a member's role on the console's card (the approved
// mockup): the owner is «صاحب المتجر», the others as the merchant reads
// them.
export const memberRole = (r: StoreMember['role']) => (r === 'owner' ? 'صاحب المتجر' : merchantRole(r));

// kindWords names what a request does, for a member's name after it.
export const kindWords: Record<SupportRequest['kind'], string> = {
  mfa_reset: 'إلغاء التحقق بخطوتين',
  ownership_transfer: 'نقل ملكية المتجر',
};

// openRequests are the requests still waiting, the soonest first.
export const openRequests = (t: Team) => t.requests.filter((r) => r.state === 'open').sort((a, b) => a.effective_at.localeCompare(b.effective_at));

// cancelledByLink are the requests a merchant cancelled from the emailed
// link lately: support should know before asking again.
export const cancelledByLink = (t: Team) => t.requests.filter((r) => r.state === 'cancelled' && r.cancelled_by === 'link');

// waiting is the open request of a kind on a member, if any.
export const waiting = (t: Team, user: string, kind: SupportRequest['kind']) =>
  t.requests.find((r) => r.state === 'open' && r.kind === kind && r.user_id === user);

// requester names who asked: «سارة (العمليات)», or «أدمن سابق».
export function requester(r: SupportRequest): string {
  if (!r.requested_by_name) return 'أدمن سابق';
  return r.requested_by_role ? `${r.requested_by_name} (${adminRole(r.requested_by_role)})` : r.requested_by_name;
}

// requestLine is a waiting request's banner text (the approved mockup).
export function requestLine(r: SupportRequest, now: Date): string {
  return `يُنفَّذ ${dateTimeAr(r.effective_at)} (${hoursUntil(r.effective_at, now)}) إن لم يُلغَ. طلبه ${requester(r)} · ${r.reason}.`;
}

// seats words a count of seats in Arabic: «مقعد واحد», «مقعدان», «4 مقاعد»,
// «12 مقعدًا».
function seats(n: number): string {
  if (n === 1) return 'مقعد واحد';
  if (n === 2) return 'مقعدان';
  return n >= 3 && n <= 10 ? `${String(n)} مقاعد` : `${String(n)} مقعدًا`;
}

// seatsLine is the card's count of seats against the plan's: members and
// pending invitations both take one.
export function seatsLine(t: Team, limit: number | null): string {
  const used = seats(t.members.length + t.invites.length);
  return limit === null ? `${used}، والباقة بلا حد.` : `${used} من ${String(limit)} في الباقة.`;
}

// lastSeen is when a member last used the dashboard.
export const lastSeen = (m: StoreMember) => (m.last_seen_at ? dateTimeAr(m.last_seen_at) : 'لم يدخل بعد');

// contactLine is a member's email and masked phone, as the role may see them.
export const contactLine = (m: StoreMember) => [m.email, m.phone].filter((x): x is string => !!x);
