import { useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router';

import { api } from '../api/client';
import { accountMessage, linkToken } from '../lib/account';
import { useSession } from '../session';

type Load = { kind: 'loading' } | { kind: 'gone' } | { kind: 'failed'; message: string } | { kind: 'done'; email: string };

// VerifyEmail is where a confirmation email's link lands (D3, the approved
// mockup): /verify-email#<token>. It works signed in or not (the link may
// open in another browser); a signed-in session is refreshed so the banner
// goes away.
export function VerifyEmail() {
  const token = linkToken(useLocation().hash);
  const { state, reload } = useSession();
  const signedIn = state.kind === 'signed-in';
  const [load, setLoad] = useState<Load>(() => (token ? { kind: 'loading' } : { kind: 'gone' }));

  useEffect(() => {
    if (!token) return;
    let live = true;
    void api.POST('/v1/auth/email/verify', { body: { token } }).then(async ({ data, error: err, response }) => {
      if (!live) return;
      if (data) {
        setLoad({ kind: 'done', email: data.email });
        if (signedIn) await reload();
      } else if (response.status === 404) setLoad({ kind: 'gone' });
      else setLoad({ kind: 'failed', message: accountMessage(err) });
    });
    return () => {
      live = false;
    };
    // The link is used once: verify on the token alone, not on session changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  return (
    <main className="login">
      <div className="card verify-card">
        {load.kind === 'loading' && <p role="status">جارٍ تأكيد بريدك…</p>}
        {load.kind === 'done' && (
          <>
            <span className="verify-ok" aria-hidden="true">
              <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <path d="M5 13l4 4L19 7" />
              </svg>
            </span>
            <h1>تم تأكيد بريدك</h1>
            <p className="muted">
              <bdi dir="ltr">{load.email}</bdi> مؤكَّد الآن.
            </p>
            <Link className="button primary" to="/">
              {signedIn ? 'إلى لوحة التحكم' : 'إلى تسجيل الدخول'}
            </Link>
          </>
        )}
        {load.kind === 'gone' && (
          <>
            <h1>انتهت صلاحية الرابط</h1>
            <p className="muted">روابط التأكيد تعمل يومين. اطلب رابطًا جديدًا من الشريط أعلى اللوحة.</p>
            <Link className="button secondary" to="/">
              {signedIn ? 'إلى لوحة التحكم' : 'إلى تسجيل الدخول'}
            </Link>
          </>
        )}
        {load.kind === 'failed' && <p role="alert">{load.message}</p>}
      </div>
    </main>
  );
}
