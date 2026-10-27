import { useState, type SubmitEvent } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { BuildInfo } from '../components/BuildInfo';
import { problemMessage } from '../lib/problem';
import { field, useSession } from '../session';

// Login is email and password, then a TOTP or recovery code when the
// account has two-step verification (POST /v1/auth/mfa).
export function Login() {
  const { reload } = useSession();
  const [challenge, setChallenge] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    try {
      if (challenge === null) {
        const { data, error: err, response } = await api.POST('/v1/auth/login', {
          body: { email: field(form, 'email'), password: field(form, 'password'), client: 'web' },
        });
        if (response.status === 202 && data && 'challenge_id' in data) {
          setChallenge(data.challenge_id);
        } else if (err) {
          setError(response.status === 401 ? 'البريد أو كلمة المرور غير صحيحة' : problemMessage(err));
        } else {
          await reload();
        }
      } else {
        const { error: err } = await api.POST('/v1/auth/mfa', {
          body: { challenge_id: challenge, code: field(form, 'code'), client: 'web' },
        });
        if (err) setError(problemMessage(err));
        else await reload();
      }
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <div className="card">
        <div className="side-brand">
          <span className="mark" aria-hidden="true">
            ا
          </span>
          <div>
            <strong>افتح أونلاين</strong>
            <small>لوحة التاجر</small>
          </div>
        </div>
        <h1>{challenge === null ? 'تسجيل الدخول' : 'رمز التحقق'}</h1>
        <form onSubmit={(e) => void submit(e)}>
          {challenge === null ? (
            <>
              <label>
                البريد الإلكتروني
                <input name="email" type="email" autoComplete="username" required dir="ltr" />
              </label>
              <label>
                كلمة المرور
                <input name="password" type="password" autoComplete="current-password" required dir="ltr" />
              </label>
            </>
          ) : (
            <label>
              رمز التحقق
              <input name="code" inputMode="numeric" autoComplete="one-time-code" required dir="ltr" />
            </label>
          )}
          {error && <p role="alert">{error}</p>}
          <button type="submit" disabled={busy}>
            {challenge === null ? 'تسجيل الدخول' : 'تأكيد'}
          </button>
        </form>
        {challenge === null && (
          <p className="login-alt">
            ليس لديك متجر؟ <Link to="/signup">أنشئ متجرك</Link>
          </p>
        )}
        <BuildInfo />
      </div>
    </main>
  );
}
