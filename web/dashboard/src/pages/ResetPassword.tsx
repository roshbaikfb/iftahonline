import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';

import { api } from '../api/client';
import { accountMessage, linkToken } from '../lib/account';
import { field, useSession } from '../session';

type Load = { kind: 'loading' } | { kind: 'gone' } | { kind: 'failed'; message: string } | { kind: 'ready'; email: string };

// ResetPassword is where a reset email's link lands (D3, the approved
// mockup): /reset-password#<token>. It shows the account's email, takes
// the new password twice, then signs in (the second factor first when the
// account has one). Every other device is signed out.
export function ResetPassword() {
  const token = linkToken(useLocation().hash);
  const navigate = useNavigate();
  const { reload } = useSession();
  const [load, setLoad] = useState<Load>(() => (token ? { kind: 'loading' } : { kind: 'gone' }));
  const [challenge, setChallenge] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!token) return;
    let live = true;
    void api.POST('/v1/auth/password/reset/check', { body: { token } }).then(({ data, error: err, response }) => {
      if (!live) return;
      if (data) setLoad({ kind: 'ready', email: data.email });
      else if (response.status === 404) setLoad({ kind: 'gone' });
      else setLoad({ kind: 'failed', message: accountMessage(err) });
    });
    return () => {
      live = false;
    };
  }, [token]);

  async function signedIn() {
    await reload();
    void navigate('/', { replace: true });
  }

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setError('');
    if (challenge === null) {
      const password = field(form, 'password');
      if (password !== field(form, 'confirm')) {
        setError('كلمتا المرور غير متطابقتين.');
        return;
      }
      setBusy(true);
      try {
        const { data, error: err, response } = await api.POST('/v1/auth/password/reset', { body: { token, password, client: 'web' } });
        if (response.status === 404) setLoad({ kind: 'gone' });
        else if (err) setError(accountMessage(err));
        else if ('challenge_id' in data) setChallenge(data.challenge_id);
        else await signedIn();
      } catch (err) {
        setError(accountMessage(err));
      } finally {
        setBusy(false);
      }
      return;
    }
    setBusy(true);
    try {
      const { error: err } = await api.POST('/v1/auth/mfa', { body: { challenge_id: challenge, code: field(form, 'code'), client: 'web' } });
      if (err) setError(accountMessage(err));
      else await signedIn();
    } catch (err) {
      setError(accountMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <div className="card">
        {load.kind === 'loading' && <p role="status">جارٍ قراءة الرابط…</p>}
        {load.kind === 'gone' && (
          <>
            <h1>كلمة مرور جديدة</h1>
            <p role="alert">
              انتهت صلاحية هذا الرابط أو استُخدم. <Link to="/forgot-password">اطلب رابطًا جديدًا</Link>
            </p>
          </>
        )}
        {load.kind === 'failed' && <p role="alert">{load.message}</p>}
        {load.kind === 'ready' && challenge === null && (
          <>
            <h1>كلمة مرور جديدة</h1>
            <p className="muted">
              لحساب <bdi dir="ltr">{load.email}</bdi>
            </p>
            <form onSubmit={(e) => void submit(e)}>
              <input type="email" name="username" value={load.email} autoComplete="username" readOnly hidden />
              <label>
                كلمة المرور الجديدة
                <input name="password" type="password" required minLength={10} maxLength={128} autoComplete="new-password" dir="ltr" />
                <small className="muted">10 أحرف على الأقل، ولا تشبه بريدك.</small>
              </label>
              <label>
                أعد كتابتها
                <input name="confirm" type="password" required minLength={10} maxLength={128} autoComplete="new-password" dir="ltr" />
              </label>
              {error && <p role="alert">{error}</p>}
              <button type="submit" disabled={busy}>
                {busy ? 'جارٍ الحفظ…' : 'احفظ وادخل'}
              </button>
            </form>
            <p className="muted small">نخرجك من كل الأجهزة الأخرى بعد التغيير، ونرسل لك بريدًا يخبرك بذلك.</p>
          </>
        )}
        {load.kind === 'ready' && challenge !== null && (
          <>
            <h1>رمز التحقق</h1>
            <p className="muted">حُفظت كلمة المرور الجديدة. اكتب رمز تطبيق المصادقة أو رمزًا احتياطيًا لتدخل.</p>
            <form onSubmit={(e) => void submit(e)}>
              <label>
                رمز التحقق
                <input name="code" inputMode="numeric" autoComplete="one-time-code" required dir="ltr" />
              </label>
              {error && <p role="alert">{error}</p>}
              <button type="submit" disabled={busy}>
                تأكيد
              </button>
            </form>
          </>
        )}
      </div>
    </main>
  );
}
