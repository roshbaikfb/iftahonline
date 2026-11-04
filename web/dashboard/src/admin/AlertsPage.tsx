import { useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { actionLabel, lasted, otherDevices, pushBlock, readPush, turnOff, turnOn, type Alerts, type PushStatus } from './alerts';
import { ConsoleLayout } from './ConsoleLayout';
import { count, dateTimeAr } from './lib';
import { useAdmin } from './session';

const blockText = {
  iphone: 'على الآيفون: افتح الكونسول في Safari، ثم زر المشاركة ثم «إضافة إلى الشاشة الرئيسية»، وافتحه من الأيقونة لتفعيلها.',
  unsupported: 'هذا المتصفح لا يدعم الإشعارات.',
  denied: 'الإشعارات محظورة لهذا الموقع في إعدادات المتصفح. اسمح بها ثم حدّث الصفحة.',
};

// AlertsPage is the console's alerts (ADR-0080, the approved mockup): how
// they reach the admin, then the open ones and those resolved lately.
export function AlertsPage() {
  const { state } = useAdmin();
  const email = state.kind === 'signed-in' ? state.admin.email : '';
  const [alerts, setAlerts] = useState<Alerts | null>(null);
  const [push, setPush] = useState<PushStatus | null>(null);
  const [here, setHere] = useState<PushSubscription | null>(null);
  const [block] = useState(pushBlock);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [now] = useState(() => Date.now());

  const showPush = ({ status, here: sub }: Awaited<ReturnType<typeof readPush>>) => {
    if (status) setPush(status);
    setHere(sub);
  };

  useEffect(() => {
    let live = true;
    void Promise.all([api.GET('/admin/v1/alerts'), readPush()]).then(([{ data, error: err }, p]) => {
      if (!live) return;
      if (data) setAlerts(data);
      if (err) setError(problemMessage(err));
      if (p.status) setPush(p.status);
      setHere(p.here);
    });
    return () => {
      live = false;
    };
  }, []);

  const setEmail = async (enabled: boolean) => {
    if (!alerts) return;
    setAlerts({ ...alerts, email: enabled });
    const { error: err } = await api.PUT('/admin/v1/alerts/email', { body: { enabled } });
    if (err) {
      setAlerts({ ...alerts, email: !enabled });
      setError(problemMessage(err));
    }
  };

  const toggleHere = async () => {
    setBusy(true);
    setError('');
    try {
      if (here) await turnOff(here);
      else if (push?.public_key && !(await turnOn(push.public_key))) setError(blockText.denied);
      showPush(await readPush());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'تعذّر التفعيل');
    } finally {
      setBusy(false);
    }
  };


  const open = alerts?.open ?? [];
  const others = Math.max(0, (push?.devices ?? 0) - (here ? 1 : 0));

  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>التنبيهات</h1>
          <p className="muted">الكونسول يفحص كل 5 دقائق. التنبيه يُفتح إذا تكرّرت المشكلة في فحصين متتاليين، ويُغلق إذا اختفت في فحصين.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <section className="card delivery" aria-labelledby="h-delivery">
        <h2 id="h-delivery">كيف تصلك التنبيهات</h2>
        <div className="delivery-ways">
          <label className="delivery-way">
            <input type="checkbox" checked={alerts?.email ?? false} disabled={!alerts} onChange={(e) => void setEmail(e.target.checked)} />
            <span>
              <strong>بالإيميل</strong>
              <span className="muted">
                إلى <bdi dir="ltr">{email}</bdi>، عند فتح التنبيه، وعند حلّ التنبيهات الحمراء.
              </span>
            </span>
          </label>
          <div className="delivery-way">
            <span>
              <strong>إشعارات على هذا الجهاز</strong>
              <span className="muted">تصل حتى والكونسول مغلق. على الآيفون: أضف الكونسول إلى الشاشة الرئيسية أولًا.</span>
              {push && !push.public_key ? (
                <span className="muted">غير مفعّلة على الخادم بعد.</span>
              ) : block && !here ? (
                <span className="muted">{blockText[block]}</span>
              ) : (
                push && (
                  <span className="delivery-actions">
                    <button type="button" className={here ? 'button secondary' : 'button primary'} disabled={busy} onClick={() => void toggleHere()}>
                      {here ? 'أوقف على هذا الجهاز' : 'فعّل على هذا الجهاز'}
                    </button>
                    {here && <span className="muted">مفعّلة على هذا الجهاز</span>}
                    {others > 0 && <span className="muted">{otherDevices(others)}</span>}
                  </span>
                )
              )}
            </span>
          </div>
        </div>
        <p className="muted small">يصلك ما يخص دورك: المالك كل شيء، والمالية الفواتير والمدفوعات، والعمليات طابور المهام.</p>
      </section>

      {alerts && (
        <section className="alerts-open" aria-labelledby="h-open">
          <h2 id="h-open">
            مفتوحة الآن{' '}
            {open.length > 0 && (
              <span className="needs-count" aria-label={`${String(open.length)} مفتوحة`}>
                {count(open.length)}
              </span>
            )}
          </h2>
          {open.length === 0 ? (
            <p className="all-good" role="status">
              لا تنبيهات مفتوحة. كل شيء يعمل.
            </p>
          ) : (
            <ul className="alert-list">
              {open.map((a) => (
                <li key={a.id} className={`alert-card ${a.severity}`}>
                  <span className="dot" aria-hidden="true" />
                  <div>
                    <strong>{a.title}</strong>
                    <p>{a.body}</p>
                    <small className="muted">مفتوح منذ {lasted(now - new Date(a.opened_at).getTime())}</small>
                  </div>
                  <Link className="button secondary" to={a.path}>
                    {actionLabel[a.kind]}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {alerts && (
        <section className="card alerts-resolved" aria-labelledby="h-resolved">
          <h2 id="h-resolved">حُلّت آخر 30 يومًا</h2>
          {alerts.resolved.length === 0 ? (
            <p className="muted">لا شيء حُلّ في آخر 30 يومًا.</p>
          ) : (
            <ul>
              {alerts.resolved.map((a) => (
                <li key={a.id}>
                  <span className="dot ok" aria-hidden="true" />
                  <span>{a.title}</span>
                  {a.resolved_at && (
                    <small className="muted">
                      حُلّ {dateTimeAr(a.resolved_at)} · استمر {lasted(new Date(a.resolved_at).getTime() - new Date(a.opened_at).getTime())}
                    </small>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </ConsoleLayout>
  );
}
