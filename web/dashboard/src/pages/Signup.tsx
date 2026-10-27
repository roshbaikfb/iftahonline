import { useEffect, useState, type SubmitEvent } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { WizardFrame } from '../components/WizardFrame';
import { countries, slugProblem, type Country } from '../lib/onboarding';
import { problemMessage } from '../lib/problem';
import { useSession } from '../session';

interface Account {
  name: string;
  email: string;
  password: string;
}

type SlugState = { kind: 'idle' } | { kind: 'checking' } | { kind: 'free' } | { kind: 'bad'; message: string };

// fieldMessages says what went wrong with sign-up in Arabic, per field
// and code; anything else falls back to the server's words.
const fieldMessages: Record<string, string> = {
  'email:taken': 'يوجد حساب بهذا البريد. سجّل الدخول بدلًا من ذلك.',
  'email:invalid': 'اكتب بريدًا إلكترونيًا صحيحًا.',
  'store.slug:taken': slugProblem.taken,
  'store.slug:reserved': slugProblem.reserved,
  'store.slug:invalid': slugProblem.invalid,
};

function signupError(err: unknown): { message: string; accountStep: boolean } {
  const fields = (err as { errors?: { field: string; code: string; message: string }[] } | undefined)?.errors;
  if (fields && fields.length > 0) {
    const f = fields[0];
    if (f) {
      const known = fieldMessages[`${f.field}:${f.code}`];
      const accountStep = !f.field.startsWith('store.');
      if (known) return { message: known, accountStep };
      if (f.field === 'password') return { message: 'كلمة المرور: 10 أحرف على الأقل، ولا تشبه بريدك.', accountStep: true };
    }
  }
  return { message: problemMessage(err), accountStep: false };
}

// Signup is the wizard's first two steps (ADR-0034, the approved mockup):
// the account, then the store's name, address and country. The account
// and store are created together at the end of the second step; the
// session then opens and the wizard goes on in the store.
export function Signup() {
  const { reload } = useSession();
  const [step, setStep] = useState(0);
  const [account, setAccount] = useState<Account>({ name: '', email: '', password: '' });
  const [storeName, setStoreName] = useState('');
  const [slug, setSlug] = useState('');
  const [slugState, setSlugState] = useState<SlugState>({ kind: 'idle' });
  const [country, setCountry] = useState<Country['id']>('eg');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  // The address is checked as it is typed, once typing pauses.
  useEffect(() => {
    if (slug === '') return;
    let live = true;
    const t = setTimeout(() => {
      void api.GET('/v1/auth/slug-availability', { params: { query: { slug } } }).then(({ data }) => {
        if (!live) return;
        if (!data) setSlugState({ kind: 'idle' });
        else if (data.available) setSlugState({ kind: 'free' });
        else setSlugState({ kind: 'bad', message: slugProblem[data.reason ?? 'invalid'] });
      });
    }, 400);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [slug]);

  function next(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setError('');
    setStep(1);
  }

  async function create(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const c = countries.find((x) => x.id === country) ?? countries[0];
    if (!c) return;
    setBusy(true);
    setError('');
    try {
      const { error: err } = await api.POST('/v1/auth/signup', {
        body: {
          ...account,
          client: 'web',
          store: { name: storeName.trim(), slug, currency: c.currency, locale: 'ar', timezone: c.timezone },
        },
      });
      if (err) {
        const { message, accountStep } = signupError(err);
        setError(message);
        if (accountStep) setStep(0);
        return;
      }
      await reload();
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (step === 0) {
    return (
      <WizardFrame step={0}>
        <form className="wiz-form" onSubmit={next}>
          <div className="wiz-head">
            <h1>أنشئ متجرك في دقائق</h1>
            <p>مجانًا، بدون بطاقة. نبدأ بحسابك.</p>
          </div>
          <div className="wiz-fields">
            <label>
              اسمك
              <input
                name="name"
                autoComplete="name"
                required
                maxLength={100}
                placeholder="مثال: منى أحمد"
                value={account.name}
                onChange={(e) => {
                  setAccount({ ...account, name: e.target.value });
                }}
              />
            </label>
            <label>
              البريد الإلكتروني
              <input
                name="email"
                type="email"
                dir="ltr"
                autoComplete="email"
                required
                maxLength={254}
                placeholder="you@example.com"
                value={account.email}
                onChange={(e) => {
                  setAccount({ ...account, email: e.target.value });
                }}
              />
            </label>
            <label>
              كلمة المرور
              <input
                name="password"
                type="password"
                dir="ltr"
                autoComplete="new-password"
                required
                minLength={10}
                maxLength={128}
                value={account.password}
                onChange={(e) => {
                  setAccount({ ...account, password: e.target.value });
                }}
              />
              <small>10 أحرف على الأقل.</small>
            </label>
          </div>
          {error && <p role="alert">{error}</p>}
          <p className="wiz-alt">
            لديك حساب؟ <Link to="/">تسجيل الدخول</Link>
          </p>
          <div className="wiz-nav">
            <span className="grow" />
            <button type="submit">متابعة</button>
          </div>
        </form>
      </WizardFrame>
    );
  }

  return (
    <WizardFrame step={1}>
      <form className="wiz-form" onSubmit={(e) => void create(e)}>
        <div className="wiz-head">
          <h1>متجرك</h1>
          <p>الاسم والرابط يمكن تغييرهما لاحقًا من الإعدادات.</p>
        </div>
        <div className="wiz-fields">
          <label>
            اسم المتجر
            <input
              name="store"
              required
              maxLength={120}
              placeholder="مثال: بيت منى"
              value={storeName}
              onChange={(e) => {
                setStoreName(e.target.value);
              }}
            />
          </label>
          <label>
            رابط المتجر
            <span className="slug" dir="ltr">
              <input
                name="slug"
                required
                maxLength={63}
                autoComplete="off"
                placeholder="beit-mona"
                aria-describedby="slug-state"
                value={slug}
                onChange={(e) => {
                  const v = e.target.value.toLowerCase().replace(/\s+/g, '-');
                  setSlug(v);
                  setSlugState(v === '' ? { kind: 'idle' } : { kind: 'checking' });
                }}
              />
              <span>.iftahonline.com</span>
            </span>
            <small id="slug-state" className={slugState.kind === 'free' ? 'ok' : slugState.kind === 'bad' ? 'bad' : undefined} aria-live="polite">
              {slugState.kind === 'free' && (
                <>
                  <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
                    <path d="M5 12l5 5L20 7" />
                  </svg>
                  الرابط متاح
                </>
              )}
              {slugState.kind === 'checking' && 'جارٍ التحقق…'}
              {slugState.kind === 'bad' && slugState.message}
            </small>
          </label>
          <fieldset className="wiz-choices">
            <legend>الدولة</legend>
            <div className="cols-3">
              {countries.map((c) => (
                <button
                  key={c.id}
                  type="button"
                  className="choice"
                  aria-pressed={c.id === country}
                  onClick={() => {
                    setCountry(c.id);
                  }}
                >
                  <strong>{c.name}</strong>
                  <small>{c.currencyName}</small>
                </button>
              ))}
            </div>
            <small>تحدد عملة المتجر ومحافظات الشحن، ولا تتغير بعد إنشاء المتجر.</small>
          </fieldset>
        </div>
        {error && <p role="alert">{error}</p>}
        <div className="wiz-nav">
          <button
            type="button"
            className="secondary"
            onClick={() => {
              setError('');
              setStep(0);
            }}
          >
            رجوع
          </button>
          <span className="grow" />
          <button type="submit" disabled={busy || slugState.kind === 'bad' || slugState.kind === 'checking'}>
            {busy ? 'جارٍ إنشاء المتجر…' : 'إنشاء المتجر'}
          </button>
        </div>
      </form>
    </WizardFrame>
  );
}
