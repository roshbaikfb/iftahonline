import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { roleLabel, roles, useRole } from '../lib/settings';
import { useSession } from '../session';

type Member = components['schemas']['Member'];
type Invitation = components['schemas']['Invitation'];
type Assignable = components['schemas']['NewInvitation']['role'];

const assignable = roles.filter((r): r is (typeof roles)[number] & { id: Assignable } => r.id !== 'owner');

// Team is who works on the store (U6d): the members and their roles, and
// invitations by email with what each role can do (the owner's decision
// 5). Only the owner manages the team; the others see who is on it.
export function Team() {
  const { storeId = '' } = useParams();
  const role = useRole(storeId);
  const { state } = useSession();
  const me = state.kind === 'signed-in' ? state.me.user.id : '';
  const manage = role === 'owner';
  const [members, setMembers] = useState<Member[] | null>(null);
  const [invites, setInvites] = useState<Invitation[]>([]);
  const [email, setEmail] = useState('');
  const [newRole, setNewRole] = useState<Assignable>('support');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [inviteError, setInviteError] = useState('');
  const [notice, setNotice] = useState('');

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void api.GET('/v1/stores/{store_id}/members', path).then(({ data, error: err }) => {
      if (!live) return;
      if (data) setMembers(data.items);
      else setError(problemMessage(err));
    });
    if (manage)
      void api.GET('/v1/stores/{store_id}/invitations', path).then(({ data }) => {
        if (live && data) setInvites(data.items);
      });
    return () => {
      live = false;
    };
  }, [storeId, manage]);

  async function invite(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/invitations', { params: { path: { store_id: storeId } }, body: { email: email.trim(), role: newRole } });
    setBusy(false);
    if (err) {
      const fields = (err as { errors?: unknown[] }).errors ?? [];
      setInviteError(fields.length ? 'اكتب بريدًا إلكترونيًا صحيحًا.' : problemMessage(err));
      return;
    }
    setInvites((cur) => [data, ...cur]);
    setNotice(`أرسلنا الدعوة إلى ${data.email}. الرابط صالح 7 أيام.`);
    setEmail('');
    setInviteError('');
  }

  async function revoke(i: Invitation) {
    if (!globalThis.confirm(`إلغاء دعوة ${i.email}؟ لن يعمل الرابط الذي وصله.`)) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/invitations/{invitation_id}', { params: { path: { store_id: storeId, invitation_id: i.id } } });
    if (err) setError(problemMessage(err));
    else setInvites((cur) => cur.filter((x) => x.id !== i.id));
  }

  async function changeRole(m: Member, r: Assignable) {
    const { error: err } = await api.PATCH('/v1/stores/{store_id}/members/{user_id}', { params: { path: { store_id: storeId, user_id: m.user_id } }, body: { role: r } });
    if (err) setError(problemMessage(err));
    else {
      setMembers((cur) => cur?.map((x) => (x.user_id === m.user_id ? { ...x, role: r } : x)) ?? null);
      setNotice(`صار ${m.name || m.email} «${roleLabel(r)}».`);
    }
  }

  async function remove(m: Member) {
    if (!globalThis.confirm(`إزالة ${m.name || m.email} من فريق المتجر؟ لن يستطيع الدخول إلى لوحة هذا المتجر.`)) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/members/{user_id}', { params: { path: { store_id: storeId, user_id: m.user_id } } });
    if (err) setError(problemMessage(err));
    else setMembers((cur) => cur?.filter((x) => x.user_id !== m.user_id) ?? null);
  }

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>فريق العمل</h1>
          <p className="muted">من يعمل معك على المتجر، وما يستطيع كل واحد فعله.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <div className="team">
        <section className="card" aria-labelledby="h-members">
          <h2 id="h-members">الأعضاء</h2>
          {!members ? (
            !error && <p role="status">جارٍ التحميل…</p>
          ) : (
            <ul className="page-rows">
              {members.map((m) => (
                <li key={m.user_id}>
                  <span className="avatar" aria-hidden="true">
                    {(m.name || m.email).slice(0, 1)}
                  </span>
                  <div className="grow">
                    <strong>
                      {m.name || m.email}
                      {m.user_id === me && <span className="muted"> (أنت)</span>}
                    </strong>
                    <span className="muted small" dir="ltr">
                      {m.email}
                    </span>
                  </div>
                  {manage && m.role !== 'owner' ? (
                    <>
                      <select
                        aria-label={`دور ${m.name || m.email}`}
                        value={m.role}
                        onChange={(e) => void changeRole(m, e.target.value as Assignable)}
                      >
                        {assignable.map((r) => (
                          <option key={r.id} value={r.id}>
                            {r.label}
                          </option>
                        ))}
                      </select>
                      <button type="button" className="link danger-link" onClick={() => void remove(m)}>
                        إزالة
                      </button>
                    </>
                  ) : (
                    <span className={`status ${m.role === 'owner' ? 's-confirmed' : 'p-draft'}`}>{roleLabel(m.role)}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
          {!manage && <p className="muted small">المالك وحده يدعو الأعضاء ويغيّر أدوارهم.</p>}
        </section>

        {manage && (
          <section className="card editor-card" aria-labelledby="h-invite">
            <h2 id="h-invite">ادعُ عضوًا</h2>
            <form className="invite-form" onSubmit={(e) => void invite(e)}>
              <label className="field-label">
                البريد الإلكتروني
                <input
                  type="email"
                  dir="ltr"
                  required
                  value={email}
                  maxLength={254}
                  aria-invalid={Boolean(inviteError) || undefined}
                  onChange={(e) => {
                    setEmail(e.target.value);
                    setInviteError('');
                  }}
                />
              </label>
              {inviteError && <p className="field-error">{inviteError}</p>}
              <fieldset className="choices">
                <legend className="field-label">الدور</legend>
                {assignable.map((r) => (
                  <label key={r.id} className="choice">
                    <input
                      type="radio"
                      name="invite-role"
                      checked={newRole === r.id}
                      onChange={() => {
                        setNewRole(r.id);
                      }}
                    />
                    <span>
                      <strong>{r.label}</strong>
                      <span className="muted small">{r.can}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
              <button type="submit" className="primary" disabled={busy || !email.trim()}>
                {busy ? 'جارٍ الإرسال…' : 'أرسل الدعوة'}
              </button>
            </form>
            {invites.length > 0 && (
              <>
                <h3>دعوات لم تُقبل بعد</h3>
                <ul className="page-rows">
                  {invites.map((i) => (
                    <li key={i.id}>
                      <div className="grow">
                        <strong dir="ltr">{i.email}</strong>
                        <span className="muted small">
                          {roleLabel(i.role)} · صالحة حتى {dateTime(i.expires_at)}
                        </span>
                      </div>
                      <button type="button" className="link danger-link" onClick={() => void revoke(i)}>
                        إلغاء الدعوة
                      </button>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </section>
        )}
      </div>
    </Layout>
  );
}
