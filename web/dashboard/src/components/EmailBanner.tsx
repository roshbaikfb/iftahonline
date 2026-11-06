import { useState } from 'react';

import { api } from '../api/client';
import { accountMessage } from '../lib/account';
import { useSession } from '../session';

// EmailBanner asks a member whose email is not confirmed yet to confirm it
// (D3, the approved mockup), on every screen until it is, with a button
// that sends a new link (at most one a minute).
export function EmailBanner() {
  const { state } = useSession();
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  if (state.kind !== 'signed-in' || state.me.user.email_verified) return null;

  async function resend() {
    setBusy(true);
    setNote('');
    try {
      const { error } = await api.POST('/v1/me/email/verification');
      setNote(error ? accountMessage(error) : 'أرسلنا رابطًا جديدًا إلى بريدك.');
    } catch (err) {
      setNote(accountMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div role="status" className="email-banner">
      <span>
        أكّد بريدك <bdi dir="ltr">{state.me.user.email}</bdi> لتصلك الفواتير وتنبيهات الطلبات، ولتستطيع استعادة حسابك إن نسيت كلمة المرور.
        {note && <strong className="email-banner-note"> {note}</strong>}
      </span>
      <button type="button" className="secondary" disabled={busy} onClick={() => void resend()}>
        {busy ? 'جارٍ الإرسال…' : 'أعد إرسال الرابط'}
      </button>
    </div>
  );
}
