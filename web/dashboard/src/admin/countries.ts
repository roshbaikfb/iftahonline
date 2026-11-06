import type { components } from '../api/client';

export type PaymentRule = components['schemas']['AdminPaymentRule'];
export type Gateway = PaymentRule['billing_gateway'];
export type Tax = components['schemas']['AdminTax'];
export type FXRate = components['schemas']['AdminFXRate'];

// OTHERS is the row every country without its own follows (ADR-0089).
export const OTHERS = 'ZZ';

// The countries a store may be in today: addresses, phones and shipping
// cover Egypt and these two (ADR-0088).
export const storeCountries = ['EG', 'SA', 'AE'] as const;

const names = new Intl.DisplayNames(['ar'], { type: 'region' });

// shortNames are the names the console uses for the store countries.
const shortNames: Record<string, string> = { EG: 'مصر', SA: 'السعودية', AE: 'الإمارات' };

// countryName is a country's Arabic name, "باقي البلاد" for ZZ, and the
// code itself when the browser does not know it.
export function countryName(code: string): string {
  if (code === OTHERS) return 'باقي البلاد';
  const short = shortNames[code];
  if (short) return short;
  try {
    return names.of(code) ?? code;
  } catch {
    return code;
  }
}

// byCountry puts a country's own rows first, by name, and the others' last.
export function byCountry<T extends { country: string }>(rows: readonly T[]): T[] {
  return [...rows].sort((a, b) => {
    if (a.country === OTHERS) return 1;
    if (b.country === OTHERS) return -1;
    return countryName(a.country).localeCompare(countryName(b.country), 'ar');
  });
}

export const gatewayName: Record<Gateway, string> = {
  paymob: 'Paymob',
  stripe: 'Stripe',
};

// billingCurrency is what a gateway bills a country's merchants in
// (ADR-0090): the pound in Egypt, the dollar elsewhere.
export const billingCurrency = (country: string) => (country === 'EG' ? 'جنيه' : 'دولار');

// percent writes basis points as a percentage: 1400 → "14".
export const percent = (bps: number) => String(bps / 100);

// toBps reads a percentage ("14" or "2.5") as basis points, NaN for
// anything else.
export function toBps(text: string): number {
  const t = text.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return Number.NaN;
  return Math.round(Number(t) * 100);
}

// rate writes a rate in millionths: 3750000 → "3.75".
export const rate = (micros: number) => String(micros / 1_000_000);

// toMicros reads a rate ("48.5", up to six decimals) as millionths, NaN
// for anything else or zero.
export function toMicros(text: string): number {
  const t = text.trim();
  if (!/^\d+(\.\d{1,6})?$/.test(t)) return Number.NaN;
  const [whole = '0', frac = ''] = t.split('.');
  const v = Number(whole) * 1_000_000 + Number(frac.padEnd(6, '0'));
  return v > 0 ? v : Number.NaN;
}
