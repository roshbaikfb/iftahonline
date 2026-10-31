import { createContext, useContext } from 'react';

import { api, type components } from '../api/client';

export type Admin = components['schemas']['AdminProfile'];

export type AdminState = { kind: 'loading' } | { kind: 'signed-out' } | { kind: 'signed-in'; admin: Admin } | { kind: 'error'; message: string };

export interface AdminSession {
  state: AdminState;
  signedIn: (admin: Admin) => void;
  signOut: () => Promise<void>;
}

export const AdminSessionContext = createContext<AdminSession | null>(null);

// fetchAdmin reads the signed-in admin (GET /admin/v1/me). The session is
// the HttpOnly __Host-asid cookie of the console's own host (ADR-0005 §8).
export async function fetchAdmin(): Promise<AdminState> {
  try {
    const { data, response } = await api.GET('/admin/v1/me');
    if (data) return { kind: 'signed-in', admin: data };
    if (response.status === 401) return { kind: 'signed-out' };
    return { kind: 'error', message: `تعذر تحميل الحساب (${String(response.status)})` };
  } catch (err) {
    return { kind: 'error', message: err instanceof Error ? err.message : 'تعذر الاتصال' };
  }
}

export function useAdmin(): AdminSession {
  const s = useContext(AdminSessionContext);
  if (!s) throw new Error('useAdmin outside the console');
  return s;
}
