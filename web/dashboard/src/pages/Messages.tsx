import { useEffect, useRef, useState } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { RiskRulesCard } from '../components/RiskRulesCard';
import {
  canManageMessages,
  partsLabel,
  smsParts,
  smsPreview,
  smsVars,
  type MessageText,
  type MessageUsage,
  type MessagingSettings,
} from '../lib/messages';
import { problemMessage } from '../lib/problem';
import { canManageRules } from '../lib/risk';
import { useRole } from '../lib/settings';

type Locale = MessageText['locale'];

const monthName = (iso: string) => new Date(`${iso.slice(0, 10)}T00:00:00Z`).toLocaleDateString('ar-EG', { month: 'long', timeZone: 'UTC' });

// renewal is the first day of the month after period, as merchants read it.
function renewal(period: string): string {
  const d = new Date(`${period.slice(0, 10)}T00:00:00Z`);
  d.setUTCMonth(d.getUTCMonth() + 1);
  return d.toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', timeZone: 'UTC' });
}

// Messages is the customer messages screen (6g, the approved mockup):
// whether cash orders are confirmed by message and when an unanswered one
// is cancelled, the month's quota, the SMS text in both languages with a
// live preview, and the WhatsApp message (a fixed template).
export function Messages() {
  const { storeId = '' } = useParams();
  const role = useRole(storeId);
  const manage = canManageMessages(role);
  const [settings, setSettings] = useState<MessagingSettings | null>(null);
  const [hours, setHours] = useState('');
  const [usage, setUsage] = useState<MessageUsage | null>(null);
  const [texts, setTexts] = useState<MessageText[]>([]);
  const [locale, setLocale] = useState<Locale>('ar');
  const [drafts, setDrafts] = useState<Partial<Record<Locale, string>>>({});
  const [storeName, setStoreName] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([
      api.GET('/v1/stores/{store_id}/messaging/settings', path),
      api.GET('/v1/stores/{store_id}/messaging/usage', path),
      api.GET('/v1/stores/{store_id}/messaging/texts', path),
      api.GET('/v1/stores/{store_id}', path),
    ]).then(([s, u, t, st]) => {
      if (!live) return;
      if (s.data) {
        setSettings(s.data);
        setHours(String(s.data.auto_cancel_hours));
      }
      if (u.data) setUsage(u.data);
      if (t.data) setTexts(t.data.items);
      if (st.data) setStoreName(st.data.name);
      const err = s.error ?? t.error;
      if (err) setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function saveSettings(next: MessagingSettings) {
    setError('');
    setNotice('');
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/messaging/settings', { params: { path: { store_id: storeId } }, body: next });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setSettings(data);
    setHours(String(data.auto_cancel_hours));
    setNotice('حُفظت الإعدادات.');
  }

  const hoursValue = Number(hours);
  const hoursOK = Number.isInteger(hoursValue) && hoursValue >= 1 && hoursValue <= 72;

  const saved = texts.find((t) => t.locale === locale);
  const body = drafts[locale] ?? saved?.sms_body ?? '';
  const preview = smsPreview(body, locale, storeName);
  const noURL = !body.includes('{{url}}');
  const dirty = saved !== undefined && body !== saved.sms_body;

  function edit(text: string) {
    setDrafts({ ...drafts, [locale]: text });
    setNotice('');
  }

  // insert puts a placeholder where the cursor is (at the end without one).
  function insert(key: string) {
    const el = area.current;
    const at = el ? el.selectionStart : body.length;
    const end = el ? el.selectionEnd : body.length;
    const token = `{{${key}}}`;
    edit(body.slice(0, at) + token + body.slice(end));
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(at + token.length, at + token.length);
    });
  }

  async function saveText() {
    setBusy(true);
    setError('');
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/messaging/texts/{kind}/{locale}', {
      params: { path: { store_id: storeId, kind: 'order_confirmation', locale } },
      body: { sms_body: body.trim() },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setTexts(texts.map((t) => (t.locale === data.locale ? data : t)));
    setDrafts({ ...drafts, [locale]: undefined });
    setNotice('حُفظ النص.');
  }

  async function resetText() {
    setBusy(true);
    setError('');
    const { data, error: err } = await api.DELETE('/v1/stores/{store_id}/messaging/texts/{kind}/{locale}', {
      params: { path: { store_id: storeId, kind: 'order_confirmation', locale } },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setTexts(texts.map((t) => (t.locale === data.locale ? data : t)));
    setDrafts({ ...drafts, [locale]: undefined });
    setNotice('رجع النص الافتراضي.');
  }

  const pct = usage && usage.limit > 0 ? Math.min(100, Math.round((usage.used / usage.limit) * 100)) : 0;
  const parts = smsParts(preview);

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>رسائل العملاء</h1>
          <p className="muted">نرسل لعملائك رسالة تأكيد بعد كل طلب دفع عند الاستلام، واتساب أولًا ثم رسالة نصية إن لم تصل.</p>
        </div>
        <div className="actions">
          <Link className="button" to={`/stores/${storeId}/messages/log`}>
            سجل الرسائل
          </Link>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <div className="msg-grid">
        <div className="settings-cards">
          {settings && (
            <section className="card editor-card" aria-labelledby="h-confirm">
              <div className="toggle-row">
                <div>
                  <h2 id="h-confirm">تأكيد طلبات الدفع عند الاستلام</h2>
                  <p className="muted small">يقلّل الطلبات الوهمية والمرتجعات: لا تشحن إلا ما أكّده العميل.</p>
                </div>
                <button
                  type="button"
                  className="toggle"
                  aria-pressed={settings.confirm_cod}
                  aria-label="تأكيد طلبات الدفع عند الاستلام"
                  disabled={!manage}
                  onClick={() => void saveSettings({ ...settings, confirm_cod: !settings.confirm_cod })}
                />
              </div>
              {settings.confirm_cod ? (
                <>
                  <ol className="flow">
                    <li>يصل الطلب، فنرسل للعميل رسالة واتساب فيها زرّا «تأكيد الطلب» و«إلغاء الطلب».</li>
                    <li>إن لم تصل رسالة واتساب، نرسل رسالة نصية فيها رابط يؤكد منه أو يلغي.</li>
                    <li>يتحوّل الطلب إلى «مؤكد» أو «ملغي» مع ردّ العميل، ويصلك إشعار.</li>
                  </ol>
                  <div>
                    <label className="field-label" htmlFor="auto-cancel">
                      إلغاء تلقائي إن لم يرد العميل خلال
                    </label>
                    <div className="hours">
                      <input
                        id="auto-cancel"
                        type="number"
                        min={1}
                        max={72}
                        dir="ltr"
                        value={hours}
                        disabled={!manage}
                        aria-invalid={!hoursOK || undefined}
                        aria-describedby="auto-cancel-help"
                        onChange={(e) => {
                          setHours(e.target.value);
                          setNotice('');
                        }}
                        onBlur={() => {
                          if (hoursOK && hoursValue !== settings.auto_cancel_hours) void saveSettings({ ...settings, auto_cancel_hours: hoursValue });
                        }}
                      />
                      <span>ساعة</span>
                    </div>
                    {!hoursOK && <p className="field-error">من 1 إلى 72 ساعة.</p>}
                    <p className="muted small" id="auto-cancel-help">
                      من 1 إلى 72 ساعة. لا يُلغى طلب لم تصل رسالته للعميل: نخبرك لتتصل به أنت.
                    </p>
                  </div>
                </>
              ) : (
                <p className="notice" role="note">
                  التأكيد متوقف: ستبقى طلبات الدفع عند الاستلام «بانتظار التأكيد» حتى تؤكدها بنفسك.
                </p>
              )}
            </section>
          )}
          <RiskRulesCard storeId={storeId} manage={canManageRules(role)} />

          {saved && (
            <section className="card editor-card sms" aria-labelledby="h-sms">
              <div className="toggle-row">
                <div>
                  <h2 id="h-sms">نص الرسالة النصية (SMS)</h2>
                  <p className="muted small">تُرسل فقط حين لا تصل رسالة واتساب. رسالة واتساب نفسها قالب ثابت توافق عليه Meta.</p>
                </div>
                <div className="segmented narrow-seg" role="group" aria-label="لغة النص">
                  {(['ar', 'en'] as const).map((l) => (
                    <button
                      key={l}
                      type="button"
                      aria-pressed={locale === l}
                      onClick={() => {
                        setLocale(l);
                      }}
                    >
                      {l === 'ar' ? 'العربية' : 'English'}
                    </button>
                  ))}
                </div>
              </div>
              <label className="field-label">
                النص
                <textarea
                  ref={area}
                  dir={locale === 'ar' ? 'rtl' : 'ltr'}
                  maxLength={400}
                  value={body}
                  disabled={!manage}
                  aria-invalid={noURL || undefined}
                  onChange={(e) => {
                    edit(e.target.value);
                  }}
                />
              </label>
              {manage && (
                <div className="vars" role="group" aria-label="أدرج متغيرًا">
                  <span className="muted small">أدرج:</span>
                  {smsVars.map((v) => (
                    <button
                      key={v.key}
                      type="button"
                      onClick={() => {
                        insert(v.key);
                      }}
                    >
                      {locale === 'ar' ? v.ar : v.en}
                    </button>
                  ))}
                </div>
              )}
              <div className="sms-meta">
                <span className="muted small">
                  {preview.length} حرفًا · نحو {partsLabel(parts)}
                </span>
                {noURL && <span className="field-error">لا بد أن يحوي النص «رابط التأكيد»: منه يؤكد العميل.</span>}
              </div>
              <div className="phone-preview">
                <small>هكذا تصل للعميل (بيانات مثال)</small>
                <div className="bubble" dir={locale === 'ar' ? 'rtl' : 'ltr'} data-testid="sms-preview">
                  {preview}
                </div>
              </div>
              {manage && (
                <div className="actions-row">
                  <button type="button" className="primary" disabled={busy || !dirty || noURL || !body.trim()} onClick={() => void saveText()}>
                    حفظ النص
                  </button>
                  {saved.custom && (
                    <button type="button" className="link" disabled={busy} onClick={() => void resetText()}>
                      الرجوع للنص الافتراضي
                    </button>
                  )}
                  {saved.custom && <span className="status st-on">نصّك</span>}
                </div>
              )}
            </section>
          )}
        </div>

        <div className="msg-side">
          {usage && (
            <section className="card editor-card" aria-labelledby="h-usage">
              <h2 id="h-usage">رصيد الرسائل لشهر {monthName(usage.period)}</h2>
              <div className="usage-num">
                <strong>{usage.used}</strong>
                <span className="muted">من {usage.limit} رسالة</span>
              </div>
              <div className={`meter ${usage.level}`} role="meter" aria-valuemin={0} aria-valuemax={usage.limit} aria-valuenow={usage.used} aria-label="المستخدم من الرصيد">
                <span style={{ inlineSize: `${String(pct)}%` }} />
              </div>
              {usage.level === 'nearly' && (
                <p className="warn-note">استهلكت {pct}%. حين ينفد الرصيد لا تُرسل رسائل التأكيد، وتبقى الطلبات بانتظار تأكيدك.</p>
              )}
              {usage.level === 'reached' && (
                <p className="warn-note reached">نفد الرصيد: لا تُرسل رسائل التأكيد حتى يتجدد، وتبقى الطلبات بانتظار تأكيدك.</p>
              )}
              <p className="muted small">يتجدد الرصيد في {renewal(usage.period)}. الرسالة النصية تُحسب رسالة واحدة مثل واتساب.</p>
            </section>
          )}

          <section className="card editor-card" aria-labelledby="h-wa">
            <h2 id="h-wa">رسالة واتساب</h2>
            <p className="muted small">تُرسل من رقم افتح أونلاين الموثّق باسم متجرك. لا تحتاج حساب واتساب أعمال.</p>
            <div className="phone-preview">
              <div className="bubble wa-bubble">
                أهلاً سارة، شكراً لطلبك من {storeName || 'متجرك'}. رقم الطلب YM2ECZRR والإجمالي 500 ج.م. هل تؤكد الطلب؟
              </div>
              <div className="wa-buttons" aria-hidden="true">
                <span>تأكيد الطلب</span>
                <span>إلغاء الطلب</span>
              </div>
            </div>
            <p className="muted small">إن بدأ العميل المحادثة بنفسه من زر «أكّد على واتساب» في صفحة الشكر، تكون الرسالة مجانية ولا تُحسب من رصيدك.</p>
          </section>
        </div>
      </div>
    </Layout>
  );
}
