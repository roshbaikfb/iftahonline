import { useEffect, useRef, useState, type SubmitEvent } from 'react';

import { api, type components } from '../api/client';
import { transferMessage } from '../lib/ownership';
import { roleLabel } from '../lib/settings';

type Member = components['schemas']['Member'];

interface Props {
  storeId: string;
  // members are the store's members other than the owner, the targets.
  members: Member[];
  // needCode asks for the second factor (the owner has it on).
  needCode: boolean;
  onDone: (to: Member) => void;
  onClose: () => void;
}

const nameOf = (m: Member) => m.name || m.email;

// TransferDialog moves the store's ownership to a member (C2, the approved
// mockup): the member, what changes after, the password and, when on, the
// second factor's code. Escape or «تراجع» closes it.
export function TransferDialog({ storeId, members, needCode, onDone, onClose }: Props) {
  const box = useRef<HTMLFormElement>(null);
  const close = useRef(onClose);
  const [to, setTo] = useState(members[0]?.user_id ?? '');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    close.current = onClose;
  });
  useEffect(() => {
    const back = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLElement>('select')?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close.current();
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      back?.focus();
    };
  }, []);

  const target = members.find((m) => m.user_id === to);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!target) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/ownership', {
      params: { path: { store_id: storeId } },
      body: { user_id: target.user_id, password, ...(needCode ? { code: code.trim() } : {}) },
    });
    setBusy(false);
    if (err) {
      setError(transferMessage(err));
      return;
    }
    onDone(target);
  }

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form ref={box} className="modal transfer-dialog" role="dialog" aria-modal="true" aria-labelledby="h-transfer" onSubmit={(e) => void submit(e)}>
        <h2 id="h-transfer">نقل ملكية المتجر</h2>
        {error && <p role="alert">{error}</p>}
        <label>
          <span className="field-label">إلى</span>
          <select
            value={to}
            onChange={(e) => {
              setTo(e.target.value);
            }}
          >
            {members.map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {nameOf(m)} — {roleLabel(m.role)} — {m.email}
              </option>
            ))}
          </select>
          <small className="muted">الأعضاء النشطون فقط. الدعوات المعلّقة لا تظهر هنا.</small>
        </label>
        {target && (
          <div className="transfer-warn">
            <strong>بعد النقل</strong>
            <span>
              يصبح {nameOf(target)} صاحب المتجر: الفوترة والباقة، والفريق، وسجل النشاط، ونقل الملكية. وتصبح أنت «مديرًا» ويستطيع إزالتك من الفريق. لا رجوع إلا
              إن نقلها إليك هو.
            </span>
          </div>
        )}
        <label>
          <span className="field-label">كلمة المرور</span>
          <input
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
          />
        </label>
        {needCode && (
          <label>
            <span className="field-label">كود التحقق بخطوتين</span>
            <input
              inputMode="numeric"
              dir="ltr"
              required
              autoComplete="one-time-code"
              className="code-input"
              value={code}
              onChange={(e) => {
                setCode(e.target.value);
              }}
            />
            <small className="muted">من تطبيق التحقق، أو كود استرداد.</small>
          </label>
        )}
        <p className="muted small">نرسل إيميلًا لك ولـ{target ? nameOf(target) : 'العضو'}، ويُسجَّل النقل في سجل النشاط.</p>
        <div className="actions">
          <button type="submit" className="danger-solid" disabled={busy || !target || !password || (needCode && !code.trim())}>
            {target ? `انقل الملكية إلى ${nameOf(target)}` : 'انقل الملكية'}
          </button>
          <button type="button" className="secondary" onClick={onClose}>
            تراجع
          </button>
        </div>
      </form>
    </div>
  );
}
