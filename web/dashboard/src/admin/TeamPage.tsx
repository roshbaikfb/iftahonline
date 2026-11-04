import { useCallback, useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { Dialog } from './Dialog';
import { dateTimeAr, initial } from './lib';
import { useAdmin } from './session';
import { expiresIn, inviteLink, roleLabel, roles, teamFirst, type Invite, type Member, type Role } from './team';

type Action =
  | { kind: 'invite' }
  | { kind: 'role'; member: Member }
  | { kind: 'disable' | 'enable' | 'reset'; member: Member }
  | { kind: 'renew' | 'cancel'; invite: Invite }
  | { kind: 'link'; name: string; role: Role; token: string; reset: boolean };

// RoleChoice is the role picker: each role with what it may do.
function RoleChoice({ value }: { value?: Role }) {
  return (
    <fieldset className="role-choice">
      <legend className="field-label">الدور</legend>
      {roles.map((r) => (
        <label key={r.value} className="radio">
          <input type="radio" name="role" value={r.value} defaultChecked={r.value === (value ?? 'viewer')} required />
          <span>
            <strong>{r.label}</strong>
            <small>{r.can}</small>
          </span>
        </label>
      ))}
    </fieldset>
  );
}

// LinkShown is a new invite or reset link, shown once to copy.
function LinkShown({ action, onClose }: { action: Extract<Action, { kind: 'link' }>; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const url = inviteLink(action.token);
  return (
    <Dialog
      title={action.reset ? 'رابط إعادة الضبط جاهز' : 'رابط الدعوة جاهز'}
      intro={`أرسله لـ${action.name} (${roleLabel(action.role)}). يُستخدم مرة واحدة وينتهي بعد 72 ساعة.`}
      onSubmit={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClose={onClose}
    >
      <div className="invite-link">
        <label>
          <span className="visually-hidden">رابط الدعوة</span>
          <input readOnly dir="ltr" value={url} onFocus={(e) => { e.currentTarget.select(); }} />
        </label>
        <button
          type="button"
          className="button primary"
          onClick={() => {
            void navigator.clipboard.writeText(url).then(() => {
              setCopied(true);
            });
          }}
        >
          {copied ? 'نُسخ' : 'نسخ'}
        </button>
      </div>
      <p className="notice">
        <strong>لن يظهر هذا الرابط مرة أخرى.</strong> من يملكه يستطيع {action.reset ? 'ضبط الحساب' : 'إنشاء الحساب'}، فأرسله للشخص نفسه فقط. لو
        ضاع، أنشئ رابطًا جديدًا من صفحة الفريق.
      </p>
      <div className="actions">
        <button type="submit" className="secondary">
          تم
        </button>
      </div>
    </Dialog>
  );
}

const statusOf = (m: Member) => (m.resetting ? { text: 'بانتظار إعادة الضبط', cls: 'team-wait' } : m.active ? { text: 'نشط', cls: 'team-on' } : { text: 'موقوف', cls: 'team-off' });

// TeamPage is the console's team (ADR-0078, the approved mockup): who may
// enter, in which role, and the invites not accepted yet. Every change
// asks for a reason, kept in the admin log; nobody changes themselves.
export function TeamPage() {
  const { state } = useAdmin();
  const me = state.kind === 'signed-in' ? state.admin.id : '';
  const [members, setMembers] = useState<Member[]>([]);
  const [invites, setInvites] = useState<Invite[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [action, setAction] = useState<Action | null>(null);
  const [dialogError, setDialogError] = useState('');
  const [busy, setBusy] = useState(false);

  const reload = useCallback(async () => {
    const r = await api.GET('/admin/v1/team');
    if (r.data) {
      setMembers(r.data.admins);
      setInvites(r.data.invites);
      setError('');
    } else setError(problemMessage(r.error));
  }, []);
  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/team').then((r) => {
      if (!live) return;
      if (r.data) {
        setMembers(r.data.admins);
        setInvites(r.data.invites);
      } else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, []);

  const open = (a: Action) => {
    setDialogError('');
    setNotice('');
    setAction(a);
  };
  const close = () => {
    setAction(null);
  };

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!action) return;
    const form = new FormData(e.currentTarget);
    const reason = field(form, 'reason');
    setBusy(true);
    let failed: unknown = null;
    let next: Action | null = null;
    let done = '';
    switch (action.kind) {
      case 'invite': {
        const role = field(form, 'role') as Role;
        const r = await api.POST('/admin/v1/team/invites', { body: { email: field(form, 'email'), name: field(form, 'name'), role, reason } });
        if (r.data) next = { kind: 'link', name: r.data.invite.name, role: r.data.invite.role, token: r.data.token, reset: false };
        else failed = r.error;
        break;
      }
      case 'role': {
        const role = field(form, 'role') as Role;
        const r = await api.PUT('/admin/v1/team/admins/{admin_id}/role', { params: { path: { admin_id: action.member.id } }, body: { role, reason } });
        if (r.error) failed = r.error;
        else done = `صار دور ${action.member.name}: ${roleLabel(role)}.`;
        break;
      }
      case 'disable':
      case 'enable': {
        const path = action.kind === 'disable' ? '/admin/v1/team/admins/{admin_id}/disable' : '/admin/v1/team/admins/{admin_id}/enable';
        const r = await api.POST(path, { params: { path: { admin_id: action.member.id } }, body: { reason } });
        if (r.error) failed = r.error;
        else done = action.kind === 'disable' ? `أُوقف ${action.member.name} وخرج من كل جلساته.` : `عاد ${action.member.name} للعمل.`;
        break;
      }
      case 'reset': {
        const r = await api.POST('/admin/v1/team/admins/{admin_id}/reset', { params: { path: { admin_id: action.member.id } }, body: { reason } });
        if (r.data) next = { kind: 'link', name: action.member.name, role: action.member.role, token: r.data.token, reset: true };
        else failed = r.error;
        break;
      }
      case 'renew': {
        const r = await api.POST('/admin/v1/team/invites/{invite_id}/renew', { params: { path: { invite_id: action.invite.id } }, body: { reason } });
        if (r.data) next = { kind: 'link', name: action.invite.name, role: action.invite.role, token: r.data.token, reset: false };
        else failed = r.error;
        break;
      }
      case 'cancel': {
        const r = await api.POST('/admin/v1/team/invites/{invite_id}/cancel', { params: { path: { invite_id: action.invite.id } }, body: { reason } });
        if (r.error) failed = r.error;
        else done = `أُلغيت دعوة ${action.invite.name}.`;
        break;
      }
      case 'link':
        break;
    }
    setBusy(false);
    if (failed) {
      setDialogError(problemMessage(failed));
      return;
    }
    setAction(next);
    setNotice(done);
    await reload();
  }

  const now = new Date();
  const sorted = teamFirst(members, me);
  const reasonField = (
    <label>
      السبب
      <input name="reason" required minLength={3} maxLength={500} />
    </label>
  );
  const buttons = (label: string, danger = false) => (
    <div className="actions">
      <button type="submit" className={danger ? 'danger-solid' : 'button primary'} disabled={busy}>
        {label}
      </button>
      <button type="button" className="secondary" onClick={close}>
        إلغاء
      </button>
    </div>
  );

  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>الفريق</h1>
          <p className="muted">من يدخل الكونسول، وماذا يستطيع كل واحد أن يفعل. كل تغيير هنا يُسجَّل في سجل الأدمن.</p>
        </div>
        <button
          type="button"
          className="button primary"
          onClick={() => {
            open({ kind: 'invite' });
          }}
        >
          دعوة أدمن
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="notice ok">
          {notice}
        </p>
      )}

      <section className="table-card" aria-label="أعضاء الفريق">
        <div className="scroll">
          <table className="team-table">
            <thead>
              <tr>
                <th scope="col">الأدمن</th>
                <th scope="col">الدور</th>
                <th scope="col">الحالة</th>
                <th scope="col">آخر دخول</th>
                <th scope="col">
                  <span className="visually-hidden">إجراءات</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((m) => {
                const st = statusOf(m);
                const self = m.id === me;
                return (
                  <tr key={m.id}>
                    <td className="t-who">
                      <span className="team-person">
                        <span className="store-mark" aria-hidden="true">
                          {initial(m.name)}
                        </span>
                        <span>
                          <strong>{self ? `${m.name} (أنت)` : m.name}</strong>
                          <small className="ltr">{m.email}</small>
                        </span>
                      </span>
                    </td>
                    <td className="t-role">
                      <span className={`status role-${m.role}`}>{roleLabel(m.role)}</span>
                    </td>
                    <td className="t-status">
                      <span className={`status ${st.cls}`}>{st.text}</span>
                    </td>
                    <td className="t-last muted">{m.last_login_at ? dateTimeAr(m.last_login_at) : 'لم يدخل بعد'}</td>
                    <td className="t-actions">
                      {!self && (
                        <span className="team-actions">
                          {m.active && (
                            <button type="button" className="secondary" onClick={() => { open({ kind: 'role', member: m }); }}>
                              تغيير الدور
                            </button>
                          )}
                          {(m.active || m.resetting) && (
                            <button type="button" className="secondary" onClick={() => { open({ kind: 'reset', member: m }); }}>
                              {m.resetting ? 'رابط إعادة ضبط جديد' : 'إعادة ضبط المصادقة'}
                            </button>
                          )}
                          {m.active ? (
                            <button type="button" className="secondary danger" onClick={() => { open({ kind: 'disable', member: m }); }}>
                              إيقاف
                            </button>
                          ) : (
                            !m.resetting && (
                              <button type="button" className="secondary" onClick={() => { open({ kind: 'enable', member: m }); }}>
                                إعادة التفعيل
                              </button>
                            )
                          )}
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
              {invites.map((i) => (
                <tr key={i.id} className="flagged">
                  <td className="t-who">
                    <span className="team-person">
                      <span className="store-mark" aria-hidden="true">
                        {initial(i.name)}
                      </span>
                      <span>
                        <strong>{i.name}</strong>
                        <small className="ltr">{i.email}</small>
                      </span>
                    </span>
                  </td>
                  <td className="t-role">
                    <span className={`status role-${i.role}`}>{roleLabel(i.role)}</span>
                  </td>
                  <td className="t-status">
                    <span className="status team-wait">دعوة لم تُقبل</span>
                    <span className="sub">{expiresIn(i.expires_at, now)}</span>
                  </td>
                  <td className="t-last muted">—</td>
                  <td className="t-actions">
                    <span className="team-actions">
                      <button type="button" className="secondary" onClick={() => { open({ kind: 'renew', invite: i }); }}>
                        رابط جديد
                      </button>
                      <button type="button" className="secondary danger" onClick={() => { open({ kind: 'cancel', invite: i }); }}>
                        إلغاء الدعوة
                      </button>
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="card team-roles" aria-labelledby="h-roles">
        <h2 id="h-roles">ماذا يستطيع كل دور</h2>
        <div className="team-roles-grid">
          {roles.map((r) => (
            <div key={r.value}>
              <span className={`status role-${r.value}`}>{r.label}</span>
              <p>{r.can}</p>
            </div>
          ))}
        </div>
        <p className="muted small">
          رقم صاحب المتجر وإيميله يراهما المالك والعمليات فقط. سجل الأدمن للمالك فقط. لا يستطيع أحد إيقاف نفسه أو تغيير دوره، ويبقى دائمًا مالك نشط واحد على
          الأقل.
        </p>
      </section>

      {action?.kind === 'link' && <LinkShown action={action} onClose={close} />}
      {action?.kind === 'invite' && (
        <Dialog
          title="دعوة أدمن"
          intro="يضع هو كلمة سره ويضبط تطبيق المصادقة بنفسه. لن تعرف أنت أيًّا منهما."
          error={dialogError}
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          <div className="two">
            <label>
              الاسم
              <input name="name" required maxLength={100} />
            </label>
            <label>
              الإيميل
              <input name="email" type="email" required maxLength={254} dir="ltr" />
            </label>
          </div>
          <RoleChoice />
          {reasonField}
          {buttons('أنشئ رابط الدعوة')}
        </Dialog>
      )}
      {action?.kind === 'role' && (
        <Dialog title={`دور ${action.member.name}`} intro="يسري من طلبه التالي." error={dialogError} onSubmit={(e) => void submit(e)} onClose={close}>
          <RoleChoice value={action.member.role} />
          {reasonField}
          {buttons('غيّر الدور')}
        </Dialog>
      )}
      {action?.kind === 'disable' && (
        <Dialog title={`إيقاف ${action.member.name}`} intro="يخرج من كل جلساته فورًا، ولا يدخل حتى تعيد تفعيله." error={dialogError} onSubmit={(e) => void submit(e)} onClose={close}>
          {reasonField}
          {buttons('أوقفه', true)}
        </Dialog>
      )}
      {action?.kind === 'enable' && (
        <Dialog title={`إعادة تفعيل ${action.member.name}`} intro="يدخل بكلمة سره وتطبيق المصادقة كما كان." error={dialogError} onSubmit={(e) => void submit(e)} onClose={close}>
          {reasonField}
          {buttons('أعد تفعيله')}
        </Dialog>
      )}
      {action?.kind === 'reset' && (
        <Dialog
          title={`إعادة ضبط مصادقة ${action.member.name}`}
          intro="لمن فقد هاتفه: يخرج من جلساته ولا تعمل كلمة سره ولا رمزه، حتى يضع جديدين من الرابط الذي ستنسخه."
          error={dialogError}
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          {reasonField}
          {buttons('أعد الضبط وأنشئ الرابط', true)}
        </Dialog>
      )}
      {action?.kind === 'renew' && (
        <Dialog title={`رابط جديد لـ${action.invite.name}`} intro="الرابط القديم يتوقف عن العمل." error={dialogError} onSubmit={(e) => void submit(e)} onClose={close}>
          {reasonField}
          {buttons('أنشئ رابطًا جديدًا')}
        </Dialog>
      )}
      {action?.kind === 'cancel' && (
        <Dialog title={`إلغاء دعوة ${action.invite.name}`} intro="لن يعمل رابطها بعد الآن." error={dialogError} onSubmit={(e) => void submit(e)} onClose={close}>
          {reasonField}
          {buttons('ألغِ الدعوة', true)}
        </Dialog>
      )}
    </ConsoleLayout>
  );
}
