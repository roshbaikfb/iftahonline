import { useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { useAdmin } from './session';

// Login needs the password and a current authenticator code in one step:
// the console has no password-only state (ADR-0005 §8).
export function Login() {
  const { signedIn } = useAdmin();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/admin/v1/auth/login', {
      body: { email: field(form, 'email'), password: field(form, 'password'), code: field(form, 'code') },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    signedIn(data.admin);
  }

  return (
    <main className="login console-login">
      <form className="card" onSubmit={(e) => void submit(e)}>
        <h1>
          الكونسول <span className="admin-badge">أدمن</span>
        </h1>
        <label className="field-label" htmlFor="email">
          البريد الإلكتروني
        </label>
        <input id="email" name="email" type="email" autoComplete="username" required dir="ltr" />
        <label className="field-label" htmlFor="password">
          كلمة المرور
        </label>
        <input id="password" name="password" type="password" autoComplete="current-password" required />
        <label className="field-label" htmlFor="code">
          رمز تطبيق المصادقة
        </label>
        <input id="code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required dir="ltr" />
        {error && <p role="alert">{error}</p>}
        <button type="submit" className="button primary" disabled={busy}>
          دخول
        </button>
      </form>
    </main>
  );
}
