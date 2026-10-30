import { useCallback, useEffect, useState, type ReactNode } from 'react';

import { api } from './api/client';
import { forgetNotices } from './lib/messages';
import { fetchSession, SessionContext, type SessionState } from './session';

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ kind: 'loading' });

  useEffect(() => {
    let live = true;
    void fetchSession().then((s) => {
      if (live) setState(s);
    });
    return () => {
      live = false;
    };
  }, []);

  const reload = useCallback(async () => {
    setState(await fetchSession());
  }, []);

  const signOut = useCallback(async () => {
    await api.POST('/v1/auth/logout');
    forgetNotices(); // another account may sign in next
    setState({ kind: 'signed-out' });
  }, []);

  return <SessionContext.Provider value={{ state, reload, signOut }}>{children}</SessionContext.Provider>;
}
