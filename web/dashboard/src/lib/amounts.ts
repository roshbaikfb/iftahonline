// Amounts are typed by merchants in major units ("45" or "45.50", Arabic
// digits too) and sent as integer minor units (CLAUDE.md rule 7). Two
// decimals for every currency the platform sells in.
const digits = 2;

const arabicDigits = /[٠-٩]/g;

// toMinor reads a typed amount; undefined when it is not one. Empty is
// undefined too: the caller decides whether the field is optional.
export function toMinor(text: string): number | undefined {
  const t = text
    .trim()
    .replace(arabicDigits, (d) => String(d.charCodeAt(0) - 0x0660))
    .replace('٫', '.')
    .replace(/,/g, '');
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return undefined;
  const [whole = '0', frac = ''] = t.split('.');
  const minor = Number(whole) * 10 ** digits + Number(frac.padEnd(digits, '0'));
  return Number.isSafeInteger(minor) ? minor : undefined;
}

// toMajor writes minor units the way toMinor reads them: "45", "45.5".
export function toMajor(minor: number): string {
  const v = minor / 10 ** digits;
  return Number.isInteger(v) ? String(v) : v.toFixed(digits).replace(/0$/, '');
}

// bpToPercent and percentToBp convert basis points (150) to the percent
// merchants type (1.5).
export function bpToPercent(bp: number): string {
  return String(bp / 100);
}

export function percentToBp(text: string): number | undefined {
  const t = text
    .trim()
    .replace(arabicDigits, (d) => String(d.charCodeAt(0) - 0x0660))
    .replace('٫', '.');
  if (!/^\d+(\.\d{1,2})?$/.test(t)) return undefined;
  return Math.round(Number(t) * 100);
}
