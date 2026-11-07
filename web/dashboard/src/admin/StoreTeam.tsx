import { useCallback, useEffect, useRef, useState, type SubmitEvent } from 'react';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { Dialog } from './Dialog';
import { useCan } from './session';
import { expiresIn } from './team';
import {
  cancelledByLink,
  contactLine,
  fetchTeam,
  kindWords,
  lastSeen,
  memberRole,
  openRequests,
  requestLine,
  seatsLine,
  waiting,
  type StoreMember,
  type SupportRequest,
} from './storeTeam';
import { dateTimeAr } from './lib';

type Team = components['schemas']['AdminStoreTeam'];
type Other = components['schemas']['AdminMemberStores']['items'][number];

type Action = { kind: 'mfa' | 'disable' | 'enable' | 'signout'; member: StoreMember } | { kind: 'cancel'; request: SupportRequest; name: string };

// The three checks support makes before asking (the approved mockup).
const checks = [
  'تواصلت معه على رقم الموبايل المسجّل في حسابه أو على رقم صاحب المتجر، لا على رقم أعطاه في الطلب.',
  'ذكر معلومة لا يعرفها غيره: آخر طلب في المتجر، أو باقته، أو آخر فاتورة دفعها.',
  'تأكدت أنه فقد تطبيق التحقق وأكواد الاسترداد معًا.',
];

// StoreTeam is the store page's team card (C1, the approved mockup): the
// members with their two-step state, last use and status, the pending
// invitations, support's waiting requests, and each member's actions
// behind a menu. Every action asks for a reason; removing two-step
// verification waits 24 hours and can be cancelled.
export function StoreTeam({ storeId, seatLimit, onChanged }: { storeId: string; seatLimit: number | null; onChanged: () => void }) {
  const can = useCan();
  const manage = can('stores.manage');
  const [team, setTeam] = useState<Team | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [menu, setMenu] = useState('');
  const [action, setAction] = useState<Action | null>(null);
  const [dialogError, setDialogError] = useState('');
  const [busy, setBusy] = useState(false);
  const [checked, setChecked] = useState<boolean[]>([false, false, false]);
  const [reason, setReason] = useState('');
  const [others, setOthers] = useState<Other[] | null>(null);
  const menuBox = useRef<HTMLDivElement>(null);

  const reload = useCallback(async () => {
    const r = await fetchTeam(storeId);
    if (r.data) setTeam(r.data);
    else setError(problemMessage(r.error));
  }, [storeId]);

  useEffect(() => {
    let live = true;
    void fetchTeam(storeId).then((r) => {
      if (!live) return;
      if (r.data) setTeam(r.data);
      else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  // An open menu closes on Escape or a click elsewhere.
  useEffect(() => {
    if (!menu) return;
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMenu('');
    };
    const click = (e: MouseEvent) => {
      if (!menuBox.current?.contains(e.target as Node)) setMenu('');
    };
    document.addEventListener('keydown', key);
    document.addEventListener('mousedown', click);
    return () => {
      document.removeEventListener('keydown', key);
      document.removeEventListener('mousedown', click);
    };
  }, [menu]);

  const open = (a: Action) => {
    setMenu('');
    setNotice('');
    setDialogError('');
    setChecked([false, false, false]);
    setReason('');
    setOthers(null);
    setAction(a);
    if (a.kind === 'disable') {
      void api.GET('/admin/v1/stores/{store_id}/team/{user_id}/stores', { params: { path: { store_id: storeId, user_id: a.member.user_id } } }).then((r) => {
        setOthers(r.data?.items ?? []);
      });
    }
  };
  const close = () => {
    setAction(null);
  };

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!action) return;
    const why = field(new FormData(e.currentTarget), 'reason');
    setBusy(true);
    let failed: unknown = null;
    let done = '';
    if (action.kind === 'cancel') {
      const r = await api.POST('/admin/v1/stores/{store_id}/support-requests/{request_id}/cancel', {
        params: { path: { store_id: storeId, request_id: action.request.id } },
        body: { reason: why },
      });
      failed = r.error;
      done = `سُحب طلب ${kindWords[action.request.kind]} لـ${action.name}.`;
    } else {
      const params = { params: { path: { store_id: storeId, user_id: action.member.user_id } }, body: { reason: why } };
      const name = action.member.name;
      switch (action.kind) {
        case 'mfa': {
          const r = await api.POST('/admin/v1/stores/{store_id}/team/{user_id}/mfa-reset', params);
          failed = r.error;
          if (r.data) done = `أُرسل الطلب. يُلغى التحقق بخطوتين لـ${name} ${dateTimeAr(r.data.effective_at)} إن لم يُلغَ الطلب.`;
          break;
        }
        case 'disable':
          failed = (await api.POST('/admin/v1/stores/{store_id}/team/{user_id}/disable', params)).error;
          done = `أُوقف حساب ${name} وخرج من كل أجهزته.`;
          break;
        case 'enable':
          failed = (await api.POST('/admin/v1/stores/{store_id}/team/{user_id}/enable', params)).error;
          done = `أُعيد تفعيل حساب ${name}.`;
          break;
        case 'signout':
          failed = (await api.POST('/admin/v1/stores/{store_id}/team/{user_id}/sign-out', params)).error;
          done = `خرج ${name} من كل أجهزته.`;
          break;
      }
    }
    setBusy(false);
    if (failed) {
      setDialogError(problemMessage(failed));
      return;
    }
    setAction(null);
    setNotice(done);
    await reload();
    onChanged();
  }

  const now = new Date();
  const nameOf = (user: string) => team?.members.find((m) => m.user_id === user)?.name ?? 'عضو سابق';
  const reasonField = (
    <label>
      السبب
      <input
        name="reason"
        required
        minLength={3}
        maxLength={500}
        value={reason}
        onChange={(e) => {
          setReason(e.target.value);
        }}
      />
    </label>
  );
  const buttons = (label: string, danger: boolean, ready = true) => (
    <div className="actions">
      <button type="submit" className={danger ? 'danger-solid' : 'button primary'} disabled={busy || !ready}>
        {label}
      </button>
      <button type="button" className="secondary" onClick={close}>
        تراجع
      </button>
    </div>
  );

  return (
    <section className="card store-card store-team" aria-labelledby="h-team">
      <div>
        <h2 id="h-team">فريق المتجر</h2>
        <p className="muted small">{team ? `${seatsLine(team, seatLimit)} ` : ''}كل إجراء هنا يطلب سببًا ويُسجَّل في سجل الأدمن وسجل نشاط المتجر.</p>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="notice ok">
          {notice}
        </p>
      )}
      {team &&
        openRequests(team).map((r) => (
          <div key={r.id} role="status" className="support-wait">
            <svg
              aria-hidden="true"
              width="20"
              height="20"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <circle cx="12" cy="12" r="9" />
              <path d="M12 7v5l3 2" />
            </svg>
            <div className="grow">
              <strong>
                طلب {kindWords[r.kind]} لـ{nameOf(r.user_id)}
              </strong>
              <div className="small">{requestLine(r, now)}</div>
            </div>
            {manage && (
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  open({ kind: 'cancel', request: r, name: nameOf(r.user_id) });
                }}
              >
                ألغِ الطلب
              </button>
            )}
          </div>
        ))}
      {team &&
        cancelledByLink(team).map((r) => (
          <p key={r.id} className="muted small">
            ألغى صاحب الحساب طلب {kindWords[r.kind]} لـ{nameOf(r.user_id)} من رابط الإيميل{r.closed_at ? ` ${dateTimeAr(r.closed_at)}` : ''}. راجع التذكرة قبل
            أي طلب آخر.
          </p>
        ))}
      {team && (
        <div className="scroll">
          <table className="store-team-table">
            <thead>
              <tr>
                <th scope="col">العضو</th>
                <th scope="col">الدور</th>
                <th scope="col">التحقق بخطوتين</th>
                <th scope="col">آخر ظهور</th>
                <th scope="col">الحالة</th>
                <th scope="col">
                  <span className="visually-hidden">إجراءات</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {team.members.map((m) => {
                const pending = waiting(team, m.user_id, 'mfa_reset');
                return (
                  <tr key={m.user_id}>
                    <td>
                      <strong>{m.name}</strong>
                      {contactLine(m).length > 0 && (
                        <div className="muted small">
                          {contactLine(m).map((c, i) => (
                            <span key={c}>
                              {i > 0 && ' · '}
                              <bdi dir="ltr">{c}</bdi>
                            </span>
                          ))}
                        </div>
                      )}
                    </td>
                    <td>{m.role === 'owner' ? <span className="status role-owner">{memberRole(m.role)}</span> : memberRole(m.role)}</td>
                    <td>
                      {m.mfa_enabled ? 'مفعّل' : <span className="muted">غير مفعّل</span>}
                      {pending && <span className="warn-text small"> · إلغاء معلّق</span>}
                    </td>
                    <td>{lastSeen(m)}</td>
                    <td>{m.disabled ? <span className="status store-suspended">موقوف من المنصة</span> : <span className="status store-active">نشط</span>}</td>
                    <td className="t-actions">
                      {manage && (
                        <div className="row-menu" ref={menu === m.user_id ? menuBox : undefined}>
                          <button
                            type="button"
                            className="secondary icon-only"
                            aria-label={`إجراءات على ${m.name}`}
                            aria-expanded={menu === m.user_id}
                            onClick={() => {
                              setMenu(menu === m.user_id ? '' : m.user_id);
                            }}
                          >
                            ⋯
                          </button>
                          {menu === m.user_id && (
                            <div role="menu" className="row-menu-list">
                              {m.mfa_enabled && !pending && (
                                <button
                                  type="button"
                                  role="menuitem"
                                  onClick={() => {
                                    open({ kind: 'mfa', member: m });
                                  }}
                                >
                                  اطلب إلغاء التحقق بخطوتين
                                </button>
                              )}
                              <button
                                type="button"
                                role="menuitem"
                                onClick={() => {
                                  open({ kind: 'signout', member: m });
                                }}
                              >
                                اخرجه من كل الأجهزة
                              </button>
                              <hr />
                              {m.disabled ? (
                                <button
                                  type="button"
                                  role="menuitem"
                                  onClick={() => {
                                    open({ kind: 'enable', member: m });
                                  }}
                                >
                                  أعد تفعيل الحساب
                                </button>
                              ) : (
                                <button
                                  type="button"
                                  role="menuitem"
                                  className="danger"
                                  onClick={() => {
                                    open({ kind: 'disable', member: m });
                                  }}
                                >
                                  أوقف الحساب
                                </button>
                              )}
                            </div>
                          )}
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
              {team.invites.map((inv, i) => (
                <tr key={`${inv.email ?? ''}-${String(i)}`}>
                  <td>
                    <strong className="muted">دعوة معلّقة</strong>
                    <div className="muted small">
                      {inv.email && (
                        <>
                          <bdi dir="ltr">{inv.email}</bdi> ·{' '}
                        </>
                      )}
                      {expiresIn(inv.expires_at, now)}
                    </div>
                  </td>
                  <td>{memberRole(inv.role)}</td>
                  <td className="muted">—</td>
                  <td className="muted">—</td>
                  <td>
                    <span className="status">لم تُقبَل</span>
                  </td>
                  <td />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="muted small store-card-foot">
        الإيميل والموبايل يظهران لدوري المالك والعمليات فقط، والإجراءات لمن له «إدارة المتاجر». إيقاف حساب يشمل كل المتاجر التي يعمل عليها صاحبه.
      </p>

      {action?.kind === 'mfa' && (
        <Dialog
          title={`إلغاء التحقق بخطوتين لـ${action.member.name}`}
          intro="للعضو الذي فقد موبايله وأكواد الاسترداد معًا."
          error={dialogError}
          className="support-dialog"
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          <div className="info-box">
            <strong>لا يُنفَّذ الآن: بعد 24 ساعة</strong>
            <span>
              {action.member.role === 'owner'
                ? `نرسل الآن إيميلًا لـ${action.member.name} (صاحب المتجر)`
                : `نرسل الآن إيميلًا لـ${action.member.name} ولصاحب المتجر`}{' '}
              فيه رابط «ألغِ الطلب». إن كان من طلب الإلغاء منتحلًا، يلغيه صاحب الحساب قبل أن يُنفَّذ.
            </span>
          </div>
          <fieldset className="checklist">
            <legend>تأكدت من هويته (كلها مطلوبة)</legend>
            {checks.map((c, i) => (
              <label key={c}>
                <input
                  type="checkbox"
                  checked={checked[i] ?? false}
                  onChange={(e) => {
                    const next = [...checked];
                    next[i] = e.target.checked;
                    setChecked(next);
                  }}
                />
                {c}
              </label>
            ))}
          </fieldset>
          {reasonField}
          <div className="small">
            <strong>عند التنفيذ</strong>
            <p className="muted">
              يُمسح مفتاح التحقق وأكواد الاسترداد، ويخرج {action.member.name} من كل أجهزته، ويدخل بكلمة المرور وحدها، ثم ننصحه بتفعيله من جديد. إن نسي كلمة
              المرور أيضًا، يستعمل «نسيت كلمة المرور».
            </p>
            <p className="muted">يُسجَّل الطلب وتنفيذه أو إلغاؤه في سجل الأدمن وفي سجل نشاط المتجر.</p>
          </div>
          {buttons('أرسل الطلب', false, checked.every(Boolean) && reason.trim().length >= 3)}
          {!(checked.every(Boolean) && reason.trim().length >= 3) && <p className="muted small">يتفعّل الزر بعد التأكد من البنود الثلاثة وكتابة السبب.</p>}
        </Dialog>
      )}
      {action?.kind === 'disable' && (
        <Dialog
          title={`إيقاف حساب ${action.member.name}`}
          intro="لحساب مسروق أو احتيال. يُنفَّذ فورًا: يخرج من كل أجهزته ولا يدخل حتى يُفعَّل."
          error={dialogError}
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          {others && others.length > 0 && (
            <div className="warn-box">
              <strong>{others.length === 1 ? 'الحساب عضو في متجر آخر' : `الحساب عضو في ${String(others.length)} متاجر أخرى`}</strong>
              <span>{others.map((o) => `${o.name} (${memberRole(o.role)})`).join(' · ')}. الإيقاف يشملها كلها.</span>
            </div>
          )}
          {reasonField}
          <p className="muted small">نرسل له إيميلًا بأن حسابه أُوقف وكيف يتواصل مع الدعم. صاحب المتجر لا يُبلَّغ إلا إن كان الحساب حسابه.</p>
          {buttons('أوقف الحساب', true)}
        </Dialog>
      )}
      {action?.kind === 'enable' && (
        <Dialog
          title={`إعادة تفعيل حساب ${action.member.name}`}
          intro="يدخل من جديد في كل متاجره بكلمة مروره، ويصله إيميل بذلك."
          error={dialogError}
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          {reasonField}
          {buttons('أعد تفعيل الحساب', false)}
        </Dialog>
      )}
      {action?.kind === 'signout' && (
        <Dialog
          title={`إخراج ${action.member.name} من كل الأجهزة`}
          intro="بلا إيقاف: يدخل من جديد بكلمة المرور. يصله إيميل بأن جلساته أُنهيت."
          error={dialogError}
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          {reasonField}
          {buttons('اخرجه من كل الأجهزة', true)}
        </Dialog>
      )}
      {action?.kind === 'cancel' && (
        <Dialog
          title={`سحب طلب ${kindWords[action.request.kind]} لـ${action.name}`}
          intro="لا يتغيّر شيء في حسابه، وتتوقف روابط الإيميل."
          error={dialogError}
          onSubmit={(e) => void submit(e)}
          onClose={close}
        >
          {reasonField}
          {buttons('اسحب الطلب', false)}
        </Dialog>
      )}
    </section>
  );
}
