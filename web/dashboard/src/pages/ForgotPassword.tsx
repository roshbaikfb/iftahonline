import { useState, type SubmitEvent } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { accountMessage } from '../lib/account';
import { field } from '../session';

// ForgotPassword asks for a reset link (D3, the approved mockup). The
// answer is the same whether the email has an account or not.
export function ForgotPassword() {
  const [sent, setSent] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const email = field(new FormData(e.currentTarget), 'email').trim();
    setBusy(true);
    setError('');
    setSent(false);
    try {
      const { error: err } = await api.POST('/v1/auth/password/forgot', { body: { email } });
      if (err) setError(accountMessage(err));
      else setSent(true);
    } catch (err) {
      setError(accountMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login">
      <div className="card">
        <h1>نسيت كلمة المرور</h1>
        <p className="muted">اكتب بريدك، ونرسل لك رابطًا لتعيين كلمة مرور جديدة. يعمل الرابط ساعة واحدة.</p>
        <form onSubmit={(e) => void submit(e)}>
          <label>
            البريد الإلكتروني
            <input name="email" type="email" autoComplete="username" required maxLength={254} dir="ltr" />
          </label>
          {error && <p role="alert">{error}</p>}
          <button type="submit" disabled={busy}>
            {busy ? 'جارٍ الإرسال…' : 'أرسل الرابط'}
          </button>
        </form>
        {sent && (
          <p role="status" className="sent-note">
            إذا كان لهذا البريد حساب، فقد أرسلنا إليه رابطًا. تفقّد صندوق الوارد والرسائل غير المرغوبة.
          </p>
        )}
        <p className="login-alt">
          <Link to="/">رجوع إلى الدخول</Link>
        </p>
      </div>
    </main>
  );
}
