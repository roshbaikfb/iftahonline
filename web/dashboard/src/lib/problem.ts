import type { components } from '../api/client';

type Problem = components['schemas']['Problem'];

// problemMessage turns an API error (RFC 9457 problem) or a network
// failure into one line for the merchant.
export function problemMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  const p = error as Partial<Problem> | undefined;
  if (p && typeof p === 'object') {
    const fields = (p as { errors?: { field: string; message: string }[] }).errors;
    if (fields && fields.length > 0) return fields.map((f) => `${f.field}: ${f.message}`).join(' · ');
    return p.detail ?? p.title ?? 'حدث خطأ غير متوقع';
  }
  return 'حدث خطأ غير متوقع';
}
