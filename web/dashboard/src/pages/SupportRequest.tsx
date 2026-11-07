import { useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router';

import { api, type components } from '../api/client';
import { accountMessage, linkToken } from '../lib/account';
import { hoursUntil, keptLine, requestTitle, whenAr } from '../lib/supportRequests';
import { useSession } from '../session';

type SupportLink = components['schemas']['SupportLink'];

type Load =
  | { kind: 'loading' }
  | { kind: 'gone' }
  | { kind: 'failed'; message: string }
  | { kind: 'open'; link: SupportLink }
  | { kind: 'cancelled'; what: SupportLink['kind'] };

// SupportRequest is where a support request's email link lands (C1, the
// approved mockup): /support-request#<token>. Without signing in, it shows
// what the platform's support asked for and when it takes effect, and
// cancels it on one button; an old link says the request is gone.
export function SupportRequest() {
  const token = linkToken(useLocation().hash);
  const { state } = useSession();
  const signedIn = state.kind === 'signed-in';
  const [load, setLoad] = useState<Load>(() => (token ? { kind: 'loading' } : { kind: 'gone' }));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!token) return;
    let live = true;
    void api.POST('/v1/support-requests/check', { body: { token } }).then(({ data, error: err, response }) => {
      if (!live) return;
      if (data) setLoad({ kind: 'open', link: data });
      else if (response.status === 404) setLoad({ kind: 'gone' });
      else setLoad({ kind: 'failed', message: accountMessage(err) });
    });
    return () => {
      live = false;
    };
  }, [token]);

  async function cancel(what: SupportLink['kind']) {
    setBusy(true);
    setError('');
    const { error: err, response } = await api.POST('/v1/support-requests/cancel', { body: { token } });
    setBusy(false);
    if (!err) setLoad({ kind: 'cancelled', what });
    else if (response.status === 404) setLoad({ kind: 'gone' });
    else setError(accountMessage(err));
  }

  const home = '/';
  return (
    <main className="login">
      <div className="card verify-card support-card">
        <strong className="brand-word">افتح أونلاين</strong>
        {load.kind === 'loading' && <p role="status">جارٍ قراءة الطلب…</p>}
        {load.kind === 'open' && (
          <>
            <h1>{requestTitle(load.link.kind)}</h1>
            <dl className="support-facts">
              <dt>الحساب</dt>
              <dd>
                <bdi dir="ltr">{load.link.email}</bdi>
              </dd>
              <dt>طُلب</dt>
              <dd>{whenAr(load.link.created_at)}</dd>
              <dt>يُنفَّذ</dt>
              <dd>
                {whenAr(load.link.effective_at)} ({hoursUntil(load.link.effective_at, new Date())})
              </dd>
            </dl>
            {error && <p role="alert">{error}</p>}
            <button type="button" className="support-cancel" disabled={busy} onClick={() => void cancel(load.link.kind)}>
              ألغِ الطلب، لم أطلبه
            </button>
            <p className="muted small">الإلغاء يبلّغ فريق الدعم أيضًا.</p>
          </>
        )}
        {load.kind === 'cancelled' && (
          <>
            <p role="status" className="support-ok">
              ألغينا الطلب. {keptLine(load.what)}
            </p>
            <p>إن لم تكن أنت من تواصل مع الدعم، فقد يعرف أحدٌ بياناتك: غيّر كلمة المرور من «حسابي».</p>
            <Link className="button secondary" to={signedIn ? '/account' : home}>
              {signedIn ? 'إلى حسابي' : 'ادخل إلى حسابك'}
            </Link>
          </>
        )}
        {load.kind === 'gone' && (
          <>
            <h1>الطلب لم يعد قائمًا</h1>
            <p className="muted">هذا الطلب لم يعد قائمًا: أُلغي، أو نُفِّذ في موعده.</p>
            <Link className="button secondary" to={home}>
              {signedIn ? 'إلى لوحة التحكم' : 'إلى تسجيل الدخول'}
            </Link>
          </>
        )}
        {load.kind === 'failed' && <p role="alert">{load.message}</p>}
      </div>
    </main>
  );
}
