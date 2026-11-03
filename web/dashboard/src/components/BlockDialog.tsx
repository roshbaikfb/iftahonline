import { useEffect, useRef, useState, type SubmitEvent } from 'react';

import { field } from '../session';

interface Props {
  title: string;
  intro: string;
  // askPhone adds the number (blocking a number that may never have
  // ordered).
  askPhone?: boolean;
  // onBlock blocks; it returns a message to show when refused.
  onBlock: (input: { phone: string; reason: string }) => Promise<string | null>;
  onClose: () => void;
}

// BlockDialog asks for a block's reason (and the number, when blocking a
// number): the approved mockup's «حظر رقم». Escape or «إلغاء» closes it.
export function BlockDialog({ title, intro, askPhone = false, onBlock, onClose }: Props) {
  const box = useRef<HTMLFormElement>(null);
  const close = useRef(onClose);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    close.current = onClose;
  });
  useEffect(() => {
    const back = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLInputElement>('input')?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close.current();
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      back?.focus();
    };
  }, []);

  async function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    const refused = await onBlock({ phone: field(form, 'phone').trim(), reason: field(form, 'reason').trim() });
    setBusy(false);
    if (refused) setError(refused);
  }

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form ref={box} className="modal block-dialog" role="dialog" aria-modal="true" aria-labelledby="h-block" onSubmit={(e) => void submit(e)}>
        <h2 id="h-block">{title}</h2>
        <p className="muted">{intro}</p>
        {error && <p role="alert">{error}</p>}
        {askPhone && (
          <label>
            <span className="field-label">رقم الهاتف</span>
            <input name="phone" type="tel" required maxLength={64} dir="ltr" />
            <small className="muted">بدون مفتاح الدولة يُقرأ برقم بلد متجرك.</small>
          </label>
        )}
        <label>
          <span className="field-label">السبب (لك وحدك)</span>
          <input name="reason" maxLength={300} />
        </label>
        <div className="actions">
          <button type="submit" className="danger" disabled={busy}>
            {askPhone ? 'حظر الرقم' : 'حظر'}
          </button>
          <button type="button" className="secondary" onClick={onClose}>
            إلغاء
          </button>
        </div>
      </form>
    </div>
  );
}
