import { useEffect, useState, type SubmitEvent } from 'react';
import { useNavigate } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { useAdmin } from './session';
import { roleLabel, roles } from './team';

type View = components['schemas']['AdminInviteView'];

const dayAr = (iso: string) => new Date(iso).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long' });

// AcceptInvite is the invitee's page (ADR-0078, the approved mockup): the
// link's token is in the fragment, read here and posted, never sent in a
// URL. Three steps: a password, the authenticator, a code to confirm;
// then the new admin is signed in. A bad link says only that.
export function AcceptInvite() {
  const { signedIn } = useAdmin();
  const navigate = useNavigate();
  const [token] = useState(() => globalThis.location.hash.slice(1));
  const [view, setView] = useState<View | null>(null);
  const [state, setState] = useState<'loading' | 'ready' | 'invalid'>('loading');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void api.POST('/admin/v1/invites/lookup', { body: { token } }).then((r) => {
      if (!live) return;
      if (r.data) {
        setView(r.data);
        setState('ready');
      } else if (r.response.status === 404 || r.response.status === 400) setState('invalid');
      else {
        setError(problemMessage(r.error));
        setState('invalid');
      }
    });
    return () => {
      live = false;
    };
  }, [token]);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const password = field(form, 'password');
    if (password !== field(form, 'again')) {
      setError('كلمتا السر غير متطابقتين.');
      return;
    }
    setBusy(true);
    setError('');
    const r = await api.POST('/admin/v1/invites/accept', { body: { token, password, code: field(form, 'code').replaceAll(' ', '') } });
    setBusy(false);
    if (r.error) {
      setError(r.response.status === 404 ? 'انتهت صلاحية هذه الدعوة أو استُخدمت. اطلب رابطًا جديدًا من المالك.' : problemMessage(r.error));
      return;
    }
    signedIn(r.data.admin);
    void navigate('/', { replace: true });
  }

  const brand = (
    <div className="invite-brand">
      <strong>الكونسول</strong> <span className="admin-badge">أدمن</span>
    </div>
  );
  if (state === 'loading') {
    return (
      <main className="invite-page">
        {brand}
        <p role="status">جارٍ التحميل…</p>
      </main>
    );
  }
  if (state === 'invalid' || !view) {
    return (
      <main className="invite-page">
        {brand}
        <h1>انتهت صلاحية هذه الدعوة</h1>
        <p className="muted">الرابط مستخدم أو منتهٍ أو غير صحيح. اطلب رابطًا جديدًا من مالك الكونسول.</p>
        {error && <p role="alert">{error}</p>}
      </main>
    );
  }
  const reset = view.purpose === 'reset';
  const can = roles.find((r) => r.value === view.role)?.can ?? '';
  const qr = `data:image/svg+xml;utf8,${encodeURIComponent(view.totp_qr_svg)}`;
  return (
    <main className="invite-page">
      {brand}
      <div>
        <h1>{reset ? `أهلًا ${view.name}، أعد ضبط حسابك` : `أهلًا ${view.name}، دعاك ${view.invited_by_name} إلى الكونسول`}</h1>
        <p className="muted">
          دورك: <strong>{roleLabel(view.role)}</strong> ({can}) {reset ? 'ضع كلمة سر جديدة واربط تطبيق المصادقة من جديد.' : 'جهّز حسابك في ثلاث خطوات.'}{' '}
          الرابط صالح حتى {dayAr(view.expires_at)}.
        </p>
      </div>
      <form className="invite-steps" onSubmit={(e) => void submit(e)}>
        <section className="card" aria-labelledby="h-step1">
          <h2 id="h-step1">
            <span className="step-no">1</span>كلمة السر
          </h2>
          <div className="two">
            <label>
              كلمة السر
              <input name="password" type="password" autoComplete="new-password" required minLength={14} maxLength={128} />
            </label>
            <label>
              أعدها
              <input name="again" type="password" autoComplete="new-password" required minLength={14} maxLength={128} />
            </label>
          </div>
          <p className="muted small">14 حرفًا على الأقل. جملة طويلة تتذكرها أسهل وأقوى من كلمة معقدة.</p>
        </section>
        <section className="card" aria-labelledby="h-step2">
          <h2 id="h-step2">
            <span className="step-no">2</span>تطبيق المصادقة
          </h2>
          <div className="invite-qr">
            <img src={qr} alt="رمز QR لإضافة الحساب إلى تطبيق المصادقة" width={168} height={168} />
            <div>
              <p>امسح الرمز بتطبيق مصادقة (Google Authenticator أو Microsoft Authenticator أو 1Password). سيظهر فيه «iftahonline admin».</p>
              <p className="muted small">لا تستطيع المسح؟ اكتب هذا المفتاح في التطبيق:</p>
              <code dir="ltr">{view.totp_secret.replace(/(.{4})/g, '$1 ').trim()}</code>
            </div>
          </div>
        </section>
        <section className="card" aria-labelledby="h-step3">
          <h2 id="h-step3">
            <span className="step-no">3</span>أكّد بالرمز
          </h2>
          <label className="invite-code">
            الرمز المكوّن من 6 أرقام في التطبيق
            <input name="code" inputMode="numeric" autoComplete="one-time-code" required maxLength={7} dir="ltr" />
          </label>
        </section>
        {error && <p role="alert">{error}</p>}
        <button type="submit" className="button primary" disabled={busy}>
          {reset ? 'احفظ وادخل' : 'فعّل حسابي وادخل'}
        </button>
      </form>
    </main>
  );
}
