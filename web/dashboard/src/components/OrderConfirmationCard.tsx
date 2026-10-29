import type { ReactNode } from 'react';
import { Link } from 'react-router';

import { channelLabel, confirmationLabel, failureLabel, type CustomerMessage, type OrderConfirmation } from '../lib/messages';
import { dateTime } from '../lib/orders';

const tick = <path d="M20 6 9 17l-5-5" />;
const clock = <path d="M12 7v5l3 2" />;
const cross = <path d="M18 6 6 18M6 6l12 12" />;

function Step({ tone, icon, children, at }: { tone: 'ok' | 'wait' | 'no'; icon: ReactNode; children: ReactNode; at?: string | undefined }) {
  return (
    <li>
      <span className={`tick ${tone}`} aria-hidden="true">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
          {icon}
        </svg>
      </span>
      <div>
        {children}
        {at && (
          <small>
            <time dateTime={at}>{dateTime(at)}</time>
          </small>
        )}
      </div>
    </li>
  );
}

// OrderConfirmationCard is the order page's "customer confirmation"
// (6g, the approved mockup): the confirmation message's way to the
// customer, their answer, and when an unanswered order is cancelled.
export function OrderConfirmationCard({ storeId, confirmation: c, messages }: { storeId: string; confirmation: OrderConfirmation; messages: CustomerMessage[] }) {
  const pill = confirmationLabel[c.status];
  // The message the customer answers: the latest confirmation sent.
  const m = messages.find((x) => x.kind === 'order_confirmation');
  const sent = m && m.status !== 'queued' && m.status !== 'skipped_quota';
  return (
    <section className="card od-confirm" aria-labelledby="h-confirm">
      <div className="confirm-head">
        <h2 id="h-confirm">تأكيد العميل</h2>
        <span className={`status ${pill.cls}`}>{pill.label}</span>
      </div>
      <ol className="msg-steps">
        {m?.status === 'queued' && (
          <Step tone="wait" icon={clock} at={m.created_at}>
            بانتظار الإرسال
          </Step>
        )}
        {sent && (
          <Step tone="ok" icon={tick} at={m.created_at}>
            أُرسلت على {channelLabel(m.channel)}
          </Step>
        )}
        {(m?.status === 'delivered' || m?.status === 'read') && (
          <Step tone="ok" icon={tick} at={m.updated_at}>
            {m.status === 'read' ? 'وصلت وقرأها العميل' : 'وصلت للعميل'}
          </Step>
        )}
        {m?.status === 'failed' && (
          <Step tone="no" icon={cross} at={m.updated_at}>
            لم تصل{m.error_code ? `: ${failureLabel(m.error_code)}` : ''}
          </Step>
        )}
        {c.status === 'pending' && (
          <Step tone="wait" icon={clock}>
            بانتظار ردّه
            <small>«تأكيد الطلب» أو «إلغاء الطلب»</small>
          </Step>
        )}
        {c.status === 'confirmed' && (
          <Step tone="ok" icon={tick} at={c.answered_at}>
            أكّد العميل الطلب
          </Step>
        )}
        {c.status === 'cancelled' && (
          <Step tone="no" icon={cross} at={c.answered_at}>
            ألغى العميل الطلب
          </Step>
        )}
        {c.status === 'expired' && (
          <Step tone="no" icon={cross} at={c.answered_at ?? c.expires_at}>
            أُلغي تلقائيًا: لم يرد العميل
          </Step>
        )}
      </ol>
      {c.status === 'pending' && (
        <p className="deadline">
          يُلغى تلقائيًا <strong>{dateTime(c.expires_at)}</strong> إن لم يرد. <Link to={`/stores/${storeId}/messages`}>غيّر المدة</Link>
        </p>
      )}
      {c.status === 'unreached' && <p className="deadline">لم تصل الرسالة للعميل: اتصل به بنفسك. لن يُلغى الطلب تلقائيًا.</p>}
      {c.status === 'skipped' && <p className="deadline">نفد رصيد الرسائل فلم تُرسل: أكّد الطلب بنفسك.</p>}
      {c.status === 'stale' && <p className="deadline">غيّرت حالة الطلب قبل أن يرد العميل، فلم يعد ردّه يغيّرها.</p>}
      {messages.length > 0 && (
        <Link className="small" to={`/stores/${storeId}/messages/log?order=${c.order_id}`}>
          رسائل هذا الطلب ({messages.length})
        </Link>
      )}
    </section>
  );
}
