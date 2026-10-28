import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { domainStatus, failureReasons, maxCustomDomains, useRole } from '../lib/settings';

type Domain = components['schemas']['Domain'];

// Domains is the store's addresses (U6d): the platform's, which always
// works, and the merchant's own, verified by a TXT record (checked by
// itself for about three days, or now on request) and then pointed at the
// store; one of the working ones is the main address.
export function Domains() {
  const { storeId = '' } = useParams();
  const role = useRole(storeId);
  const manage = role === 'owner' || role === 'manager'; // domains.manage
  const [domains, setDomains] = useState<Domain[] | null>(null);
  const [host, setHost] = useState('');
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [addError, setAddError] = useState('');
  const [copied, setCopied] = useState('');

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/domains', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
      if (!live) return;
      if (data) setDomains(data.items);
      else setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  const path = (id: string) => ({ params: { path: { store_id: storeId, domain_id: id } } });
  const replace = (d: Domain) => {
    setDomains((cur) => cur?.map((x) => (x.id === d.id ? d : d.is_primary ? { ...x, is_primary: false } : x)) ?? null);
  };

  async function add(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const h = host.trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/.*$/, '');
    if (!h) return;
    setBusy('add');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/domains', { params: { path: { store_id: storeId } }, body: { host: h } });
    setBusy('');
    if (err) {
      const fields = (err as { errors?: { field: string; code: string }[] }).errors ?? [];
      setAddError(fields.length ? 'اكتب الدومين فقط، مثل shop.example.com، بلا مسافات.' : problemMessage(err));
      return;
    }
    setDomains((cur) => [...(cur ?? []), data]);
    setHost('');
    setAddError('');
  }

  async function verify(d: Domain) {
    setBusy(d.id);
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/domains/{domain_id}/verify', path(d.id));
    setBusy('');
    if (err) setError(problemMessage(err));
    else replace(data);
  }

  async function makePrimary(d: Domain) {
    if (!globalThis.confirm(`جعل ${d.host} العنوان الرئيسي لمتجرك؟ ستتحول إليه الروابط وتظهر به في محركات البحث.`)) return;
    setBusy(d.id);
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/domains/{domain_id}/primary', path(d.id));
    setBusy('');
    if (err) setError(problemMessage(err));
    else replace(data);
  }

  async function remove(d: Domain) {
    if (!globalThis.confirm(`إزالة ${d.host} من متجرك؟ لن يفتح المتجر عليه بعد ذلك.`)) return;
    setBusy(d.id);
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/domains/{domain_id}', path(d.id));
    setBusy('');
    if (err) setError(problemMessage(err));
    else setDomains((cur) => cur?.filter((x) => x.id !== d.id) ?? null);
  }

  function copy(text: string) {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(text);
    });
  }

  const platform = domains?.find((d) => d.kind === 'subdomain');
  const custom = domains?.filter((d) => d.kind === 'custom') ?? [];

  const copyRow = (label: string, value: string) => (
    <div className="dns-row">
      <span className="muted small">{label}</span>
      <code dir="ltr">{value}</code>
      <button
        type="button"
        className="secondary small"
        aria-label={`انسخ ${label}`}
        onClick={() => {
          copy(value);
        }}
      >
        {copied === value ? 'نُسخ ✓' : 'نسخ'}
      </button>
    </div>
  );

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>الدومينات</h1>
          <p className="muted">العناوين التي يفتح عليها متجرك. رابط المنصة يعمل دائمًا، ويمكنك إضافة دومينك الخاص.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {!domains ? (
        !error && <p role="status">جارٍ التحميل…</p>
      ) : (
        <>
          <section className="card" aria-labelledby="h-domains">
            <h2 id="h-domains">عناوين متجرك</h2>
            <ul className="domain-list">
              {domains.map((d) => {
                const st = domainStatus(d);
                return (
                  <li key={d.id} className="domain">
                    <div className="domain-head">
                      <strong dir="ltr">{d.host}</strong>
                      <span className={`status ${st.className}`}>{st.label}</span>
                      {d.is_primary && <span className="status s-confirmed">العنوان الرئيسي</span>}
                      {d.kind === 'subdomain' && <span className="muted small">رابط المنصة</span>}
                      <span className="grow" />
                      {manage && d.status === 'verified' && !d.is_primary && (
                        <button type="button" className="secondary small" disabled={busy !== ''} onClick={() => void makePrimary(d)}>
                          اجعله الرئيسي
                        </button>
                      )}
                      {manage && d.kind === 'custom' && !d.is_primary && (
                        <button type="button" className="link danger-link" disabled={busy !== ''} onClick={() => void remove(d)}>
                          إزالة
                        </button>
                      )}
                    </div>
                    {manage && d.kind === 'custom' && d.status !== 'verified' && d.verification && (
                      <div className="dns-steps">
                        {d.status === 'failed' && d.failure_reason && <p className="field-error">{failureReasons[d.failure_reason] ?? d.failure_reason}</p>}
                        <ol>
                          <li>افتح إعدادات DNS عند الشركة التي اشتريت منها الدومين.</li>
                          <li>
                            أضف سجلًا من نوع <code dir="ltr">{d.verification.record_type}</code> بهذا الاسم وهذه القيمة:
                            {copyRow('الاسم', d.verification.name)}
                            {copyRow('القيمة', d.verification.value)}
                          </li>
                          {platform && (
                            <li>
                              وأضف سجلًا من نوع <code dir="ltr">CNAME</code> للدومين نفسه يشير إلى رابط المنصة:
                              {copyRow('يشير إلى', platform.host)}
                            </li>
                          )}
                          <li>نتحقق تلقائيًا لمدة 3 أيام تقريبًا، وقد يستغرق ظهور السجلات ساعات.</li>
                        </ol>
                        <div className="actions">
                          <button type="button" className="secondary small" disabled={busy !== ''} onClick={() => void verify(d)}>
                            {busy === d.id ? 'جارٍ التحقق…' : 'تحقّق الآن'}
                          </button>
                          {d.last_checked_at && <span className="muted small">آخر تحقق: {dateTime(d.last_checked_at)}</span>}
                        </div>
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          </section>

          {manage && (
          <section className="card" aria-labelledby="h-add-domain">
            <h2 id="h-add-domain">أضف دومينك</h2>
            {custom.length >= maxCustomDomains ? (
              <p className="muted">وصلت إلى الحد: {maxCustomDomains} دومينات. أزل واحدًا لتضيف غيره.</p>
            ) : (
              <form className="inline-form" onSubmit={(e) => void add(e)}>
                <label className="field-label grow">
                  الدومين
                  <input
                    dir="ltr"
                    value={host}
                    maxLength={253}
                    placeholder="shop.example.com"
                    aria-invalid={Boolean(addError) || undefined}
                    onChange={(e) => {
                      setHost(e.target.value);
                      setAddError('');
                    }}
                  />
                </label>
                <button type="submit" className="primary" disabled={busy !== '' || !host.trim()}>
                  {busy === 'add' ? 'جارٍ الإضافة…' : 'أضف'}
                </button>
              </form>
            )}
            {addError && <p className="field-error">{addError}</p>}
            <p className="muted small">اشترِ الدومين أولًا من أي شركة تسجيل دومينات، ثم أضفه هنا لتظهر لك خطوات ربطه.</p>
          </section>
          )}
        </>
      )}
    </Layout>
  );
}
