import { useEffect, useState, type SubmitEvent } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { BuildInfo } from '../components/BuildInfo';
import { accountMessage, maskPhone, phoneRegion } from '../lib/account';
import { problemMessage } from '../lib/problem';
import { field, useSession } from '../session';

// A new code may be asked for this long after the last one (the API
// sends at most one a minute per number).
const resendAfter = 60;

type Step = { kind: 'password' } | { kind: 'phone' } | { kind: 'code'; challenge: string; phone: string; sentAt: number } | { kind: 'mfa'; challenge: string };

// Login is email and password, or a code sent to the account's confirmed
// phone (D4, the approved mockup); then a TOTP or recovery code when the
// account has two-step verification (POST /v1/auth/mfa).
export function Login() {
  const { reload } = useSession();
  const [step, setStep] = useState<Step>({ kind: 'password' });
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  // The resend countdown ticks while the code step is open.
  useEffect(() => {
    if (step.kind !== 'code') return undefined;
    const t = window.setInterval(() => {
      setNow(Date.now());
    }, 1000);
    return () => {
      window.clearInterval(t);
    };
  }, [step.kind]);

  async function sendCode(phone: string) {
    const { data, error: err } = await api.POST('/v1/auth/otp/start', { body: { phone, region: phoneRegion() } });
    if (!data) {
      setError(accountMessage(err));
      return;
    }
    const at = Date.now();
    setNow(at);
    setStep({ kind: 'code', challenge: data.challenge_id, phone, sentAt: at });
  }

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    try {
      switch (step.kind) {
        case 'password': {
          const {
            data,
            error: err,
            response,
          } = await api.POST('/v1/auth/login', {
            body: { email: field(form, 'email'), password: field(form, 'password'), client: 'web' },
          });
          if (response.status === 202 && data && 'challenge_id' in data) setStep({ kind: 'mfa', challenge: data.challenge_id });
          else if (err) setError(response.status === 401 ? 'البريد أو كلمة المرور غير صحيحة' : problemMessage(err));
          else await reload();
          break;
        }
        case 'phone':
          await sendCode(field(form, 'phone').trim());
          break;
        case 'code': {
          const {
            data,
            error: err,
            response,
          } = await api.POST('/v1/auth/otp/verify', {
            body: { challenge_id: step.challenge, code: field(form, 'code').trim(), client: 'web' },
          });
          if (response.status === 202 && data && 'challenge_id' in data) setStep({ kind: 'mfa', challenge: data.challenge_id });
          else if (err) setError(response.status === 401 ? 'الكود غير صحيح أو انتهت صلاحيته.' : accountMessage(err));
          else await reload();
          break;
        }
        case 'mfa': {
          const { error: err } = await api.POST('/v1/auth/mfa', {
            body: { challenge_id: step.challenge, code: field(form, 'code'), client: 'web' },
          });
          if (err) setError(problemMessage(err));
          else await reload();
          break;
        }
      }
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function resend(phone: string) {
    setBusy(true);
    setError('');
    try {
      await sendCode(phone);
    } catch (err) {
      setError(accountMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const choose = (kind: 'password' | 'phone') => {
    setError('');
    setStep({ kind });
  };
  const byPhone = step.kind === 'phone' || step.kind === 'code';
  const wait = step.kind === 'code' ? Math.max(0, resendAfter - Math.floor((now - step.sentAt) / 1000)) : 0;

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
        <h1>{step.kind === 'mfa' ? 'رمز التحقق' : step.kind === 'code' ? 'اكتب الكود' : 'تسجيل الدخول'}</h1>
        {(step.kind === 'password' || step.kind === 'phone') && (
          <div className="segmented login-tabs" role="tablist" aria-label="طريقة الدخول">
            <button
              type="button"
              role="tab"
              aria-selected={!byPhone}
              onClick={() => {
                choose('password');
              }}
            >
              البريد وكلمة المرور
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={byPhone}
              onClick={() => {
                choose('phone');
              }}
            >
              كود على الموبايل
            </button>
          </div>
        )}
        {step.kind === 'code' && (
          <p className="muted">
            إذا كان للرقم <bdi dir="ltr">{maskPhone(step.phone)}</bdi> حساب، فقد أرسلنا إليه كودًا من 6 أرقام.
          </p>
        )}
        <form key={step.kind} onSubmit={(e) => void submit(e)}>
          {step.kind === 'password' && (
            <>
              <label>
                البريد الإلكتروني
                <input name="email" type="email" autoComplete="username" required dir="ltr" />
              </label>
              <div className="field">
                <div className="label-row">
                  <label htmlFor="login-password">كلمة المرور</label>
                  <Link to="/forgot-password">نسيت كلمة المرور؟</Link>
                </div>
                <input id="login-password" name="password" type="password" autoComplete="current-password" required dir="ltr" />
              </div>
            </>
          )}
          {step.kind === 'phone' && (
            <label>
              رقم الموبايل
              <input name="phone" type="tel" autoComplete="tel" required maxLength={32} dir="ltr" />
              <small className="muted">الرقم المؤكَّد في «حسابي». نرسل الكود بواتساب، أو برسالة SMS إن تعذّر.</small>
            </label>
          )}
          {(step.kind === 'code' || step.kind === 'mfa') && (
            <label>
              {step.kind === 'code' ? 'الكود' : 'رمز التحقق'}
              <input name="code" inputMode="numeric" autoComplete="one-time-code" required maxLength={32} dir="ltr" />
            </label>
          )}
          {error && <p role="alert">{error}</p>}
          <button type="submit" disabled={busy}>
            {step.kind === 'password' ? 'تسجيل الدخول' : step.kind === 'phone' ? 'أرسل الكود' : step.kind === 'code' ? 'دخول' : 'تأكيد'}
          </button>
        </form>
        {step.kind === 'code' && (
          <>
            {wait > 0 ? (
              <p className="muted small">
                لم يصلك؟ يمكنك طلب كود جديد بعد <strong dir="ltr">{`0:${String(wait).padStart(2, '0')}`}</strong>
              </p>
            ) : (
              <button type="button" className="link" disabled={busy} onClick={() => void resend(step.phone)}>
                أرسل كودًا جديدًا
              </button>
            )}
            <p className="muted small">إن كان التحقق بخطوتين مفعّلًا، نطلب بعدها كود تطبيق التحقق كالمعتاد.</p>
          </>
        )}
        {step.kind === 'password' && (
          <p className="login-alt">
            ليس لديك متجر؟ <Link to="/signup">أنشئ متجرك</Link>
          </p>
        )}
        <BuildInfo />
      </div>
    </main>
  );
}
