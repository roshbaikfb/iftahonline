import { vi } from 'vitest';

export interface Call {
  method: string;
  path: string;
  search: URLSearchParams;
  body: unknown;
}

type Handler = (call: Call) => { status: number; body?: unknown };

// parseBody reads a JSON body, or keeps any other (a CSV upload) as text.
function parseBody(text: string): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

// mockAPI routes fetch by "METHOD /path" (the path may end in "*") and
// records every call, so a test can assert what the page sent.
export function mockAPI(routes: Record<string, Handler>): Call[] {
  const calls: Call[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
    const req = input instanceof Request ? input : new Request(input);
    const url = new URL(req.url);
    const text = await req.text();
    const call: Call = { method: req.method, path: url.pathname, search: url.searchParams, body: parseBody(text) };
    calls.push(call);
    const key = Object.keys(routes).find((k) => {
      const [m, p = ''] = k.split(' ');
      return m === req.method && (p.endsWith('*') ? url.pathname.startsWith(p.slice(0, -1)) : url.pathname === p);
    });
    const res = key ? routes[key]?.(call) : { status: 404, body: { title: 'Not Found', status: 404 } };
    const status = res?.status ?? 500;
    return new Response(status === 204 ? null : JSON.stringify(res?.body ?? {}), {
      status,
      headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' },
    });
  });
  return calls;
}
