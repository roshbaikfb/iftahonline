import { useEffect, useRef, useState } from 'react';
import { Navigate } from 'react-router';

import { api } from '../api/client';
import { codeOf } from '../lib/support';
import { useSession } from '../session';

// Impersonate opens a platform admin's support session: it trades the
// console's one-time code for the session cookie, once, and goes to the
// store. The code is wiped from the address bar first.
export function Impersonate() {
  const { state, reload } = useSession();
  const [code] = useState(() => codeOf(window.location.hash));
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);
  const started = useRef(false);

  useEffect(() => {
    window.history.replaceState(null, '', window.location.pathname);
    if (started.current || !code) return;
    started.current = true;
    void api.POST('/v1/auth/impersonation', { body: { code } }).then(async ({ error: err }) => {
      if (err) {
        setError('انتهى رابط جلسة الدعم أو استُخدم من قبل. افتح جلسة جديدة من الكونسول.');
        return;
      }
      await reload();
      setDone(true);
    });
  }, [code, reload]);

  if (done && state.kind === 'signed-in') {
    const first = state.me.memberships[0]?.store_id;
    return <Navigate to={first ? `/stores/${first}/home` : '/'} replace />;
  }
  const message = code ? error : 'رابط جلسة الدعم ناقص.';
  return <main className="login">{message ? <p role="alert">{message}</p> : <p role="status">جارٍ فتح جلسة الدعم…</p>}</main>;
}
