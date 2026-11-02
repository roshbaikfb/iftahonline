import type { components } from '../api/client';

export type FoundingProgram = components['schemas']['AdminFoundingProgram'];
export type FoundingMember = components['schemas']['AdminFoundingMember'];

// percent writes basis points as a percentage: 2000 → "20%".
export const percent = (bps: number) => `${String(bps / 100)}%`;

// toBps reads a percentage typed in the form ("20" or "12.5") as basis
// points.
export const toBps = (text: string) => Math.round(Number(text) * 100);

export const cycleLabel: Record<FoundingMember['cycle'], string> = { monthly: 'شهري', annual: 'سنوي' };

// memberState is a member's status line: in, or why it left.
export function memberState(m: FoundingMember): string {
  if (!m.lost_reason) return 'عضو';
  return m.lost_reason === 'cancelled' ? 'خرج: ألغى الاشتراك' : 'خرج: لم يدفع التجديد 30 يومًا';
}

// seatsLeft are the seats a new merchant can still take (lost seats stay
// taken).
export const seatsLeft = (p: FoundingProgram, members: FoundingMember[]) => Math.max(0, p.seats - members.length);
