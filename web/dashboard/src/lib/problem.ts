import type { components } from '../api/client';
import { isPlanLimit, planLimitMessage } from './plan';

type Problem = components['schemas']['Problem'];

// problemMessage turns an API error (RFC 9457 problem) or a network
// failure into one line for the merchant.
export function problemMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  // The plan's refusal, in Arabic with the way out (the server's is English).
  if (isPlanLimit(error)) return planLimitMessage(error);
  const p = error as Partial<Problem> | undefined;
  if (p && typeof p === 'object') {
    const fields = (p as { errors?: { field: string; message: string }[] }).errors;
    if (fields && fields.length > 0) return fields.map((f) => `${f.field}: ${f.message}`).join(' · ');
    return p.detail ?? p.title ?? 'حدث خطأ غير متوقع';
  }
  return 'حدث خطأ غير متوقع';
}
