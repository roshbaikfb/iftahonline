import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { accountMessage, deviceName, groupKey, phoneRegion } from '../lib/account';
import { ago } from '../lib/home';
import { field, useSession } from '../session';

type SessionInfo = components['schemas']['SessionInfo'];
type TOTPSetup = components['schemas']['TOTPSetup'];

// Account is «حسابي» (D4, the approved mockup): the signed-in user's own
// name, password, phone, two-step verification and signed-in devices,
// the same for every store they work on. A support session sees it but
// changes nothing.
export function Account() {
  const { storeId = '' } = useParams();
  const { state } = useSession();
  const support = state.kind === 'signed-in' && state.me.impersonation !== undefined;
  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>حسابي</h1>
          <p className="muted">بياناتك وأمان دخولك. لكل المتاجر التي تعمل عليها.</p>
        </div>
      </div>
      {support && <p className="notice">هذه جلسة دعم: تُعرض بيانات الحساب ولا تتغير منها.</p>}
      <div className="account">
        <Profile />
        <Password />
        <Phone storeId={storeId} />
        <TwoStep support={support} />
        <Devices />
      </div>
    </Layout>
  );
}

// useAction runs one form's request: busy while it runs, then its error
// or its success line.
function useAction() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState('');
  async function run(fn: () => Promise<string | null>) {
    setBusy(true);
    setError('');
    setDone('');
    try {
      const ok = await fn();
      if (ok !== null) setDone(ok);
    } catch (err) {
      setError(accountMessage(err));
    } finally {
      setBusy(false);
    }
  }
  // fail ends a request with the API's refusal, said in Arabic.
  function fail(err: unknown): never {
    throw new Error(accountMessage(err));
  }
  return { busy, error, done, run, fail };
}

function Outcome({ error, done }: { error: string; done: string }) {
  return (
    <>
      {error && <p role="alert">{error}</p>}
      {done && (
        <p role="status" className="ok-note">
          {done}
        </p>
      )}
    </>
  );
}

function Profile() {
  const { state, reload } = useSession();
  const user = state.kind === 'signed-in' ? state.me.user : undefined;
  const a = useAction();
  if (!user) return null;

  function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const name = field(new FormData(e.currentTarget), 'name').trim();
    void a.run(async () => {
      const { error } = await api.PATCH('/v1/me', { body: { name } });
      if (error) a.fail(error);
      await reload();
      return 'حُفظ الاسم.';
    });
  }

  return (
    <section className="card account-card" aria-labelledby="acc-profile">
      <h2 id="acc-profile">بياناتك</h2>
      <form onSubmit={save}>
        <label>
          الاسم
          <input name="name" defaultValue={user.name} required maxLength={100} autoComplete="name" />
        </label>
        <div className="account-field">
          <span>البريد الإلكتروني</span>
          <div>
            <bdi dir="ltr">{user.email}</bdi>{' '}
            <span className={user.email_verified ? 'status s-delivered' : 'status s-pending'}>{user.email_verified ? 'مؤكَّد' : 'غير مؤكَّد'}</span>
          </div>
        </div>
        <Outcome error={a.error} done={a.done} />
        <div className="actions">
          <button type="submit" disabled={a.busy}>
            احفظ الاسم
          </button>
        </div>
      </form>
    </section>
  );
}

function Password() {
  const { state } = useSession();
  const a = useAction();

  function change(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const data = new FormData(form);
    void a.run(async () => {
      const { error } = await api.POST('/v1/me/password', {
        body: { current_password: field(data, 'current'), new_password: field(data, 'next') },
      });
      if (error) a.fail(error);
      form.reset();
      window.dispatchEvent(new Event(sessionsChanged));
      return 'تغيّرت كلمة المرور، وخرجنا من كل الأجهزة الأخرى.';
    });
  }

  return (
    <section className="card account-card" aria-labelledby="acc-password">
      <h2 id="acc-password">كلمة المرور</h2>
      <form onSubmit={change}>
        {state.kind === 'signed-in' && <input type="email" name="username" value={state.me.user.email} autoComplete="username" readOnly hidden />}
        <div className="account-pair">
          <label>
            الحالية
            <input name="current" type="password" required maxLength={512} autoComplete="current-password" dir="ltr" />
          </label>
          <label>
            الجديدة
            <input name="next" type="password" required minLength={10} maxLength={128} autoComplete="new-password" dir="ltr" />
            <small className="muted">10 أحرف على الأقل.</small>
          </label>
        </div>
        <p className="muted small">بعد التغيير نخرجك من كل الأجهزة الأخرى.</p>
        <Outcome error={a.error} done={a.done} />
        <div className="actions">
          <button type="submit" disabled={a.busy}>
            غيّر كلمة المرور
          </button>
        </div>
      </form>
    </section>
  );
}

function Phone({ storeId }: { storeId: string }) {
  const { state, reload } = useSession();
  const phone = state.kind === 'signed-in' ? state.me.user.phone : undefined;
  const [country, setCountry] = useState<string | undefined>();
  const [challenge, setChallenge] = useState<string | null>(null);
  const a = useAction();

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setCountry(data.country);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  function send(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const number = field(new FormData(e.currentTarget), 'phone').trim();
    void a.run(async () => {
      const { data, error } = await api.POST('/v1/me/phone', { body: { phone: number, region: phoneRegion(country) } });
      if (error) a.fail(error);
      if (data) setChallenge(data.challenge_id);
      return 'أرسلنا كودًا إلى الرقم.';
    });
  }

  function verify(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const code = field(new FormData(e.currentTarget), 'code').trim();
    if (challenge === null) return;
    void a.run(async () => {
      const { error } = await api.POST('/v1/me/phone/verify', { body: { challenge_id: challenge, code } });
      if (error) a.fail(error);
      setChallenge(null);
      await reload();
      return 'تأكّد الرقم.';
    });
  }

  return (
    <section className="card account-card" aria-labelledby="acc-phone">
      <h2 id="acc-phone">رقم الموبايل</h2>
      <p className="muted">للدخول بكود على الموبايل، ولتنبيهاتك على واتساب.</p>
      {phone && (
        <p>
          الرقم المؤكَّد: <bdi dir="ltr">{phone}</bdi>
        </p>
      )}
      <form className="account-inline" onSubmit={send}>
        <label>
          {phone ? 'رقم جديد' : 'الرقم'}
          <input name="phone" type="tel" required maxLength={32} autoComplete="tel" dir="ltr" />
        </label>
        <button type="submit" className="secondary" disabled={a.busy}>
          أرسل كود التأكيد
        </button>
      </form>
      {challenge !== null && (
        <form className="account-inline" onSubmit={verify}>
          <label>
            الكود الذي وصلك
            <input name="code" inputMode="numeric" autoComplete="one-time-code" required maxLength={12} dir="ltr" />
          </label>
          <button type="submit" disabled={a.busy}>
            أكّد الرقم
          </button>
        </form>
      )}
      <Outcome error={a.error} done={a.done} />
    </section>
  );
}

function TwoStep({ support }: { support: boolean }) {
  const { state, reload } = useSession();
  const enabled = state.kind === 'signed-in' && state.me.user.mfa_enabled;
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [stopping, setStopping] = useState(false);
  const a = useAction();

  // Not enabled: a fresh seed to scan (nothing is on until it is confirmed).
  useEffect(() => {
    if (enabled || support) return;
    let live = true;
    void api.POST('/v1/me/mfa/totp').then(({ data }) => {
      if (live && data) setSetup(data);
    });
    return () => {
      live = false;
    };
  }, [enabled, support]);

  function confirm(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const code = field(new FormData(e.currentTarget), 'code').trim();
    void a.run(async () => {
      const { data, error } = await api.POST('/v1/me/mfa/totp/confirm', { body: { code } });
      if (error) a.fail(error);
      setCodes(data?.codes ?? []);
      setSetup(null);
      await reload();
      return 'فُعّل التحقق بخطوتين.';
    });
  }

  function stop(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    void a.run(async () => {
      const { error, response } = await api.POST('/v1/me/mfa/totp/disable', { body: { password: field(data, 'password'), code: field(data, 'code').trim() } });
      if (response.status === 401) throw new Error('كلمة المرور أو الكود غير صحيح.');
      if (error) a.fail(error);
      setStopping(false);
      setCodes(null);
      await reload();
      return 'أُوقف التحقق بخطوتين.';
    });
  }

  const text = codes?.join('\n') ?? '';
  return (
    <section className="card account-card" aria-labelledby="acc-2fa">
      <div className="account-title">
        <h2 id="acc-2fa">التحقق بخطوتين</h2>
        <span className={enabled ? 'status s-delivered' : 'status'}>{enabled ? 'مفعّل' : 'غير مفعّل'}</span>
      </div>
      <p className="muted">يطلب كودًا من تطبيق على موبايلك (Google Authenticator أو غيره) مع كلمة المرور. ننصح به لصاحب المتجر.</p>

      {!enabled && setup && (
        <div className="totp-setup">
          <img src={`data:image/svg+xml;utf8,${encodeURIComponent(setup.qr_svg)}`} alt="رمز QR لإضافة الحساب إلى تطبيق التحقق" width={148} height={148} />
          <form onSubmit={confirm}>
            <p>
              1. امسح الرمز بالتطبيق، أو اكتب المفتاح: <bdi dir="ltr">{groupKey(setup.secret)}</bdi>
            </p>
            <label>
              2. اكتب الكود الذي يظهر في التطبيق
              <input name="code" inputMode="numeric" autoComplete="one-time-code" required maxLength={12} dir="ltr" />
            </label>
            <div className="actions">
              <button type="submit" disabled={a.busy}>
                فعّل التحقق بخطوتين
              </button>
            </div>
          </form>
        </div>
      )}

      {codes && codes.length > 0 && (
        <div className="recovery">
          <strong>أكواد الاسترداد (تظهر مرة واحدة)</strong>
          <p className="muted">احفظها في مكان آمن. كل كود يدخلك مرة واحدة إن فقدت موبايلك.</p>
          <div className="recovery-codes" dir="ltr">
            {codes.map((c) => (
              <code key={c}>{c}</code>
            ))}
          </div>
          <div className="actions">
            <button type="button" className="secondary" onClick={() => void navigator.clipboard.writeText(text)}>
              انسخها
            </button>
            <a className="button" href={`data:text/plain;charset=utf-8,${encodeURIComponent(text + '\n')}`} download="iftahonline-recovery-codes.txt">
              نزّلها ملفًا
            </a>
          </div>
        </div>
      )}

      {enabled && !stopping && (
        <div className="actions">
          <button
            type="button"
            className="danger"
            onClick={() => {
              setStopping(true);
            }}
          >
            أوقف التحقق بخطوتين
          </button>
        </div>
      )}
      {enabled && stopping && (
        <form onSubmit={stop}>
          <div className="account-pair">
            <label>
              كلمة المرور
              <input name="password" type="password" required maxLength={512} autoComplete="current-password" dir="ltr" />
            </label>
            <label>
              كود من التطبيق أو كود استرداد
              <input name="code" required maxLength={32} autoComplete="one-time-code" dir="ltr" />
            </label>
          </div>
          <div className="actions">
            <button type="submit" className="danger" disabled={a.busy}>
              أوقف التحقق بخطوتين
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setStopping(false);
              }}
            >
              إلغاء
            </button>
          </div>
        </form>
      )}
      <Outcome error={a.error} done={a.done} />
    </section>
  );
}

const sessionsChanged = 'iftah:sessions-changed';

function Devices() {
  const [items, setItems] = useState<SessionInfo[] | null>(null);
  const [error, setError] = useState('');
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/me/sessions').then(({ data, error: err }) => {
      if (!live) return;
      if (data) setItems(data.items);
      else setError(accountMessage(err));
    });
    const again = () => {
      setVersion((v) => v + 1);
    };
    window.addEventListener(sessionsChanged, again);
    return () => {
      live = false;
      window.removeEventListener(sessionsChanged, again);
    };
  }, [version]);

  async function revoke(id: string) {
    setError('');
    const { error: err } = await api.DELETE('/v1/me/sessions/{session_id}', { params: { path: { session_id: id } } });
    if (err) setError(accountMessage(err));
    else setVersion((v) => v + 1);
  }

  const now = new Date();
  return (
    <section className="card account-card" aria-labelledby="acc-devices">
      <h2 id="acc-devices">الأجهزة المسجّل دخولها</h2>
      {items === null && !error && <p role="status">جارٍ التحميل…</p>}
      {items && (
        <ul className="devices">
          {items.map((s) => (
            <li key={s.id}>
              <div>
                <strong>{deviceName(s.user_agent)}</strong>
                <div className="muted small">
                  آخر نشاط {ago(s.last_seen_at, now)} · دخل {ago(s.created_at, now)}
                </div>
              </div>
              {s.current ? (
                <span className="status s-delivered">هذا الجهاز</span>
              ) : (
                <button type="button" className="secondary" onClick={() => void revoke(s.id)}>
                  اخرج من هذا الجهاز
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
