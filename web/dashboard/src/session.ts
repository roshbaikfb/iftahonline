import { createContext, useContext } from 'react';

import { api, type components } from './api/client';

export type Me = components['schemas']['Me'];

export type SessionState =
  | { kind: 'loading' }
  | { kind: 'signed-out' }
  | { kind: 'signed-in'; me: Me }
  | { kind: 'error'; message: string };

export interface Session {
  state: SessionState;
  reload: () => Promise<void>;
  signOut: () => Promise<void>;
}

export const SessionContext = createContext<Session | null>(null);

// fetchSession reads who is signed in (GET /v1/me). The session itself is
// the HttpOnly __Host-sid cookie (ADR-0005); the dashboard never sees a
// token.
export async function fetchSession(): Promise<SessionState> {
  try {
    const { data, response } = await api.GET('/v1/me');
    if (data) return { kind: 'signed-in', me: data };
    if (response.status === 401) return { kind: 'signed-out' };
    return { kind: 'error', message: `تعذر تحميل الحساب (${String(response.status)})` };
  } catch (err) {
    return { kind: 'error', message: err instanceof Error ? err.message : 'تعذر الاتصال' };
  }
}

export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error('useSession outside SessionProvider');
  return s;
}

// field reads a text field of a submitted form.
export function field(form: FormData, name: string): string {
  const v = form.get(name);
  return typeof v === 'string' ? v : '';
}
