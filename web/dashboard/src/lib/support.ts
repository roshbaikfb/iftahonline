// codeOf reads the console's one-time code from the URL's fragment, which
// browsers never send to a server (ADR-0055).
export function codeOf(hash: string): string {
  return new URLSearchParams(hash.replace(/^#/, '')).get('code') ?? '';
}
