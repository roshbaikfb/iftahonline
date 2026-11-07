import { useCallback, useEffect, useState, type SubmitEvent } from 'react';

import { api, type components } from '../api/client';
import { deviceName } from '../lib/account';
import { problemMessage } from '../lib/problem';
import { ConsoleLayout } from './ConsoleLayout';
import { dateTimeAr } from './lib';
import { useAdmin } from './session';
import { roleLabel } from './team';

type OwnSession = components['schemas']['AdminOwnSession'];
type NewPhone = components['schemas']['AdminNewPhone'];

// accountMessage says why an account change was refused, in Arabic.
function accountMessage(error: unknown): string {
  const p = error as { status?: number; code?: string; errors?: { field: string; code: string }[] } | undefined;
  const field = p?.errors?.[0];
  if (field?.field === 'current_password') return 'كلمة المرور الحالية غير صحيحة.';
  if (field?.field === 'new_password') return 'كلمة المرور الجديدة قصيرة: 14 حرفًا على الأقل.';
  if (field?.field === 'code') return 'الكود غير صحيح. اكتب الكود الذي يظهر الآن في التطبيق.';
  if (field?.field === 'name') return 'اكتب اسمًا من حرف إلى 100.';
  if (p?.code === 'no_new_phone') return 'انتهت المهلة: ابدأ من جديد بكود من موبايلك القديم.';
  if (p?.status === 429) return 'محاولات كثيرة. انتظر قليلًا ثم حاول.';
  return problemMessage(error);
}

// AccountPage is the signed-in admin's own account (C3, the approved
// mockup): the name, the password, moving two-step verification to a new
// phone, and the devices signed in. The role is another owner's to change.
export function AccountPage() {
  const { state, signedIn } = useAdmin();
  const admin = state.kind === 'signed-in' ? state.admin : null;
  const [name, setName] = useState(admin?.name ?? '');
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [oldCode, setOldCode] = useState('');
  const [newCode, setNewCode] = useState('');
  const [phone, setPhone] = useState<NewPhone | null>(null);
  const [sessions, setSessions] = useState<OwnSession[]>([]);
  const [busy, setBusy] = useState('');
  const [said, setSaid] = useState<{ part: string; ok: boolean; text: string } | null>(null);

  const loadSessions = useCallback(async () => {
    const r = await api.GET('/admin/v1/me/sessions');
    if (r.data) setSessions(r.data.items);
  }, []);
  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/me/sessions').then((r) => {
      if (live && r.data) setSessions(r.data.items);
    });
    return () => {
      live = false;
    };
  }, []);

  // run sends one part's change and says how it went, under that part.
  async function run(part: string, call: () => Promise<{ error?: unknown }>, done: string): Promise<boolean> {
    setBusy(part);
    setSaid(null);
    const { error } = await call();
    setBusy('');
    setSaid({ part, ok: !error, text: error ? accountMessage(error) : done });
    return !error;
  }
  const note = (part: string) =>
    said?.part === part && (
      <p role={said.ok ? 'status' : 'alert'} className={said.ok ? 'notice ok' : undefined}>
        {said.text}
      </p>
    );

  async function saveName(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    let renamed: components['schemas']['AdminProfile'] | undefined;
    await run(
      'name',
      async () => {
        const r = await api.PATCH('/admin/v1/me', { body: { name } });
        renamed = r.data;
        return r;
      },
      'حُفظ الاسم.',
    );
    if (renamed) signedIn(renamed);
  }

  async function changePassword(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const ok = await run(
      'password',
      () => api.POST('/admin/v1/me/password', { body: { current_password: current, new_password: next } }),
      'تغيّرت كلمة المرور، وخرجنا من أجهزتك الأخرى. أرسلنا لك إيميل تنبيه.',
    );
    if (ok) {
      setCurrent('');
      setNext('');
      await loadSessions();
    }
  }

  async function startPhone(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    let started: NewPhone | undefined;
    await run(
      'phone',
      async () => {
        const r = await api.POST('/admin/v1/me/phone/start', { body: { code: oldCode.trim() } });
        started = r.data;
        return r;
      },
      'امسح الرمز بالموبايل الجديد، ثم اكتب كودًا منه.',
    );
    if (started) {
      setPhone(started);
      setOldCode('');
    }
  }

  async function confirmPhone(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const ok = await run(
      'phone',
      () => api.POST('/admin/v1/me/phone/confirm', { body: { code: newCode.trim() } }),
      'انتقل التحقق بخطوتين إلى الموبايل الجديد. الموبايل القديم توقف.',
    );
    if (ok) {
      setPhone(null);
      setNewCode('');
    }
  }

  async function revoke(s: OwnSession) {
    if (await run('devices', () => api.POST('/admin/v1/me/sessions/{session_id}/revoke', { params: { path: { session_id: s.id } } }), 'خرجنا من الجهاز.')) {
      await loadSessions();
    }
  }

  async function revokeOthers() {
    if (await run('devices', () => api.POST('/admin/v1/me/sessions/revoke-others'), 'خرجنا من باقي أجهزتك.')) await loadSessions();
  }

  if (!admin) return null;
  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>حسابي</h1>
          <p className="muted">بياناتك وأمان دخولك إلى الكونسول. الدور يغيّره مالك آخر من «الفريق».</p>
        </div>
      </div>
      <div className="console-account">
        <section className="card" aria-labelledby="h-me">
          <h2 id="h-me">بياناتك</h2>
          <form onSubmit={(e) => void saveName(e)}>
            <label>
              الاسم
              <input
                required
                maxLength={100}
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                }}
              />
            </label>
            <dl className="store-facts">
              <dt>البريد</dt>
              <dd>
                <bdi dir="ltr">{admin.email}</bdi>
              </dd>
              <dt>الدور</dt>
              <dd>{roleLabel(admin.role)}</dd>
            </dl>
            {note('name')}
            <div>
              <button type="submit" className="button primary" disabled={busy === 'name' || !name.trim() || name.trim() === admin.name}>
                احفظ الاسم
              </button>
            </div>
          </form>
        </section>

        <section className="card" aria-labelledby="h-password">
          <h2 id="h-password">كلمة المرور</h2>
          <form onSubmit={(e) => void changePassword(e)}>
            <div className="two">
              <label>
                الحالية
                <input
                  type="password"
                  required
                  autoComplete="current-password"
                  value={current}
                  onChange={(e) => {
                    setCurrent(e.target.value);
                  }}
                />
              </label>
              <label>
                الجديدة
                <input
                  type="password"
                  required
                  minLength={14}
                  autoComplete="new-password"
                  value={next}
                  onChange={(e) => {
                    setNext(e.target.value);
                  }}
                />
                <small className="muted">14 حرفًا على الأقل.</small>
              </label>
            </div>
            <p className="muted small">بعد التغيير نخرجك من أجهزتك الأخرى، ونرسل لك إيميل تنبيه، ويُسجَّل في سجل الأدمن.</p>
            {note('password')}
            <div>
              <button type="submit" className="secondary" disabled={busy === 'password' || !current || next.length < 14}>
                غيّر كلمة المرور
              </button>
            </div>
          </form>
        </section>

        <section className="card" aria-labelledby="h-phone">
          <div className="section-head">
            <h2 id="h-phone">جهاز التحقق بخطوتين</h2>
            <span className="status store-active">مفعّل دائمًا</span>
          </div>
          <p className="muted">التحقق بخطوتين إلزامي في الكونسول. لنقله إلى موبايل جديد وموبايلك القديم معك:</p>
          {!phone ? (
            <form className="phone-step" onSubmit={(e) => void startPhone(e)}>
              <label>
                1. اكتب كودًا من الموبايل القديم
                <input
                  inputMode="numeric"
                  dir="ltr"
                  required
                  autoComplete="one-time-code"
                  className="code-input"
                  value={oldCode}
                  onChange={(e) => {
                    setOldCode(e.target.value);
                  }}
                />
              </label>
              <div>
                <button type="submit" className="secondary" disabled={busy === 'phone' || oldCode.trim().length < 6}>
                  تابع
                </button>
              </div>
            </form>
          ) : (
            <form className="phone-new" onSubmit={(e) => void confirmPhone(e)}>
              <img src={`data:image/svg+xml;utf8,${encodeURIComponent(phone.qr_svg)}`} alt="رمز QR للموبايل الجديد" width={140} height={140} />
              <div className="grow">
                <p>
                  2. امسح الرمز بالموبايل الجديد، أو اكتب المفتاح:{' '}
                  <bdi dir="ltr" className="mono">
                    {phone.secret.replace(/(.{4})/g, '$1 ').trim()}
                  </bdi>
                </p>
                <label>
                  ثم اكتب كودًا من الموبايل الجديد
                  <input
                    inputMode="numeric"
                    dir="ltr"
                    required
                    autoComplete="one-time-code"
                    className="code-input"
                    value={newCode}
                    onChange={(e) => {
                      setNewCode(e.target.value);
                    }}
                  />
                </label>
                <div>
                  <button type="submit" className="button primary" disabled={busy === 'phone' || newCode.trim().length < 6}>
                    انقل إلى الموبايل الجديد
                  </button>
                </div>
              </div>
            </form>
          )}
          {note('phone')}
          <p className="muted small">الموبايل القديم يتوقف فور التأكيد. ضاع موبايلك؟ اطلب من مالك آخر «إعادة تعيين» من صفحة الفريق.</p>
        </section>

        <section className="card" aria-labelledby="h-devices">
          <div className="section-head">
            <h2 id="h-devices">الأجهزة المسجّل دخولها</h2>
            {sessions.length > 1 && (
              <button type="button" className="secondary danger" disabled={busy === 'devices'} onClick={() => void revokeOthers()}>
                اخرج من باقي الأجهزة
              </button>
            )}
          </div>
          {note('devices')}
          <ul className="device-list">
            {sessions.map((s) => (
              <li key={s.id}>
                <div className="grow">
                  <strong>{deviceName(s.user_agent)}</strong>
                  <div className="muted small">
                    آخر نشاط {dateTimeAr(s.last_seen_at)} · دخل {dateTimeAr(s.created_at)}
                    {s.ip_prefix && (
                      <>
                        {' · '}
                        <bdi dir="ltr">{s.ip_prefix}</bdi>
                      </>
                    )}
                  </div>
                </div>
                {s.current ? (
                  <span className="status store-active">هذا الجهاز</span>
                ) : (
                  <button type="button" className="secondary" disabled={busy === 'devices'} onClick={() => void revoke(s)}>
                    اخرج من هذا الجهاز
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      </div>
    </ConsoleLayout>
  );
}
