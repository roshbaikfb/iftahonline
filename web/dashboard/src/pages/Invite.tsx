import { useEffect, useState, type SubmitEvent } from 'react';
import { useLocation, useNavigate } from 'react-router';

import { api, type components } from '../api/client';
import { dateAr } from '../lib/billing';
import { problemMessage } from '../lib/problem';
import { roles } from '../lib/settings';
import { field, useSession } from '../session';
import { Login } from './Login';

type Preview = components['schemas']['InvitationPreview'];
type Load = { kind: 'loading' } | { kind: 'gone' } | { kind: 'failed'; message: string } | { kind: 'ready'; preview: Preview };

// Invite is where an invitation email's link lands (D2, the approved
// mockup): /invite#<token>, the token in the fragment so it never reaches
// a server log. It shows the store, who invited, the role and the email,
// then joins: a signed-in invitee in one click, a new one by creating the
// account, one with an account after signing in (the same URL then shows
// the join button). A link that is used, expired or revoked says so.
export function Invite() {
  const { hash } = useLocation();
  const token = decodeURIComponent(hash.replace(/^#/, ''));
  const navigate = useNavigate();
  const { state, reload, signOut } = useSession();
  const [load, setLoad] = useState<Load>(() => (token ? { kind: 'loading' } : { kind: 'gone' }));
  const [tab, setTab] = useState<'new' | 'existing' | null>(null);
  const [signingIn, setSigningIn] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!token) return;
    let live = true;
    void api.POST('/v1/invitations/preview', { body: { token } }).then(({ data, error: err, response }) => {
      if (!live) return;
      if (data) setLoad({ kind: 'ready', preview: data });
      else if (response.status === 404) setLoad({ kind: 'gone' });
      else setLoad({ kind: 'failed', message: problemMessage(err) });
    });
    return () => {
      live = false;
    };
  }, [token]);

  async function join(e?: SubmitEvent<HTMLFormElement>) {
    e?.preventDefault();
    const form = e ? new FormData(e.currentTarget) : null;
    setBusy(true);
    setError('');
    const body = form ? { token, name: field(form, 'name').trim(), password: field(form, 'password'), client: 'web' as const } : { token, client: 'web' as const };
    const { data, error: err } = await api.POST('/v1/invitations/accept', { body });
    if (!data) {
      setBusy(false);
      setError(problemMessage(err));
      return;
    }
    await reload();
    void navigate(`/stores/${data.store_id}/home`, { replace: true });
  }

  if (signingIn && state.kind === 'signed-out') return <Login />;

  const role = load.kind === 'ready' ? roles.find((r) => r.id === load.preview.role) : undefined;
  const me = state.kind === 'signed-in' ? state.me.user.email.toLowerCase() : null;
  const preview = load.kind === 'ready' ? load.preview : null;
  const mine = preview !== null && me !== null && me === preview.email.toLowerCase();
  const shownTab = tab ?? (preview?.has_account ? 'existing' : 'new');

  return (
    <main className="login invite">
      <div className="card">
        {load.kind === 'loading' && <p role="status">جارٍ قراءة الدعوة…</p>}
        {load.kind === 'gone' && (
          <>
            <h1>الدعوة غير صالحة</h1>
            <p role="alert">انتهت صلاحية هذه الدعوة أو استُخدمت. اطلب من صاحب المتجر دعوة جديدة.</p>
          </>
        )}
        {load.kind === 'failed' && <p role="alert">{load.message}</p>}
        {preview && (
          <>
            <div className="invite-head">
              <span className="mark" aria-hidden="true">
                {preview.store_name.trim().charAt(0)}
              </span>
              <div>
                <h1>انضم إلى «{preview.store_name}»</h1>
                {preview.inviter_name && <p className="muted">دعاك {preview.inviter_name} للعمل معه على المتجر.</p>}
              </div>
            </div>
            <dl className="invite-facts">
              <div>
                <dt>دورك</dt>
                <dd>{role?.label ?? preview.role}</dd>
              </div>
              {role && <p className="muted small">{role.can}</p>}
              <div>
                <dt>الدعوة لـ</dt>
                <dd>
                  <bdi dir="ltr">{preview.email}</bdi>
                </dd>
              </div>
              <div>
                <dt>صالحة حتى</dt>
                <dd>{dateAr(preview.expires_at)}</dd>
              </div>
            </dl>

            {state.kind === 'signed-in' &&
              (mine ? (
                <button type="button" disabled={busy} onClick={() => void join()}>
                  {busy ? 'جارٍ الانضمام…' : `انضم إلى «${preview.store_name}»`}
                </button>
              ) : (
                <>
                  <p role="alert" className="warn-note">
                    هذه الدعوة لـ <bdi dir="ltr">{preview.email}</bdi>، وأنت داخل بـ <bdi dir="ltr">{state.me.user.email}</bdi>. اخرج وادخل بالبريد
                    الصحيح.
                  </p>
                  <button type="button" className="secondary" onClick={() => void signOut()}>
                    تسجيل الخروج
                  </button>
                </>
              ))}

            {state.kind === 'signed-out' && (
              <>
                <div className="segmented invite-tabs" role="group" aria-label="الحساب">
                  <button
                    type="button"
                    aria-pressed={shownTab === 'new'}
                    onClick={() => {
                      setTab('new');
                    }}
                  >
                    ليس لدي حساب
                  </button>
                  <button
                    type="button"
                    aria-pressed={shownTab === 'existing'}
                    onClick={() => {
                      setTab('existing');
                    }}
                  >
                    لدي حساب
                  </button>
                </div>
                {shownTab === 'new' ? (
                  preview.has_account ? (
                    <p className="notice">لهذا البريد حساب بالفعل. سجّل الدخول به لتنضم.</p>
                  ) : (
                    <form onSubmit={(e) => void join(e)}>
                      <label>
                        اسمك
                        <input name="name" required maxLength={100} autoComplete="name" />
                      </label>
                      <label>
                        البريد الإلكتروني
                        <input type="email" value={preview.email} readOnly dir="ltr" />
                        <small className="muted">البريد الذي وصلته الدعوة، ولا يتغير.</small>
                      </label>
                      <label>
                        كلمة المرور
                        <input name="password" type="password" required minLength={10} maxLength={128} autoComplete="new-password" dir="ltr" />
                        <small className="muted">10 أحرف على الأقل.</small>
                      </label>
                      {error && <p role="alert">{error}</p>}
                      <button type="submit" disabled={busy}>
                        {busy ? 'جارٍ إنشاء الحساب…' : 'أنشئ حسابي وانضم'}
                      </button>
                    </form>
                  )
                ) : (
                  <>
                    <p className="muted">
                      سجّل الدخول بـ <bdi dir="ltr">{preview.email}</bdi>، ثم تعود إلى هذه الصفحة لتنضم.
                    </p>
                    <button
                      type="button"
                      onClick={() => {
                        setSigningIn(true);
                      }}
                    >
                      سجّل الدخول
                    </button>
                  </>
                )}
              </>
            )}
            {state.kind === 'signed-in' && error && <p role="alert">{error}</p>}
          </>
        )}
      </div>
    </main>
  );
}
