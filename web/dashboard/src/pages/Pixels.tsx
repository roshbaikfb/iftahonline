import { useEffect, useState } from 'react';
import { useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import {
  consentChoices,
  draftOf,
  isKnown,
  pixelState,
  providerLook,
  serverErrors,
  toPixelInput,
  type Draft,
  type MarketingSettings,
  type Pixel,
  type PixelProvider,
  type ProviderName,
} from '../lib/marketing';
import { problemMessage } from '../lib/problem';

// Pixels connects the store's ad and analytics providers (6g, the
// approved mockup): the providers and their state on one side, the chosen
// one's IDs and server token on the other, and when shoppers are asked
// for consent. Tokens are write-only: the page only says one is saved.
export function Pixels() {
  const { storeId = '' } = useParams();
  const [providers, setProviders] = useState<PixelProvider[]>([]);
  const [pixels, setPixels] = useState<Pixel[]>([]);
  const [pick, setPick] = useState<ProviderName>('meta');
  const [draft, setDraft] = useState<Draft>(draftOf(undefined));
  const [settings, setSettings] = useState<MarketingSettings | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([api.GET('/v1/stores/{store_id}/marketing/pixels', path), api.GET('/v1/stores/{store_id}/marketing/settings', path)]).then(([p, s]) => {
      if (!live) return;
      if (p.error) {
        setError(problemMessage(p.error));
        return;
      }
      const known = p.data.providers.filter((x) => isKnown(x.provider));
      setProviders(known);
      setPixels(p.data.items);
      const first = known[0]?.provider ?? 'meta';
      setPick(first);
      setDraft(draftOf(p.data.items.find((x) => x.provider === first)));
      if (s.data) setSettings(s.data);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  const spec = providers.find((p) => p.provider === pick);
  const pixel = pixels.find((p) => p.provider === pick);
  const look = providerLook[pick];

  function choose(p: ProviderName) {
    setPick(p);
    setDraft(draftOf(pixels.find((x) => x.provider === p)));
    setErrors({});
    setNotice('');
  }

  const edit = (next: Partial<Draft>) => {
    setDraft({ ...draft, ...next });
    setErrors({});
  };

  async function save() {
    if (!spec) return;
    const { input, errors: bad } = toPixelInput(spec, draft, pixel?.version ?? 0);
    if (!input) {
      setErrors(bad);
      return;
    }
    setBusy(true);
    setError('');
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/marketing/pixels/{provider}', {
      params: { path: { store_id: storeId, provider: pick } },
      body: input,
    });
    setBusy(false);
    if (err) {
      const fields = serverErrors(err);
      setErrors(fields);
      setError(Object.keys(fields).length > 0 ? '' : problemMessage(err));
      return;
    }
    setPixels([...pixels.filter((x) => x.provider !== pick), data]);
    setDraft(draftOf(data));
    setNotice(pixel ? 'حُفظ.' : `رُبط ${look.name}.`);
  }

  async function disconnect() {
    if (!globalThis.confirm(`فصل ${look.name}؟ يُحذف رمزه، ولا تُرسل له أحداث بعد ذلك.`)) return;
    setBusy(true);
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/marketing/pixels/{provider}', { params: { path: { store_id: storeId, provider: pick } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setPixels(pixels.filter((x) => x.provider !== pick));
    setDraft(draftOf(undefined));
    setNotice(`فُصل ${look.name}.`);
  }

  // chooseConsent shows the choice at once and puts the old one back if
  // the save fails.
  async function chooseConsent(consent: MarketingSettings['consent']) {
    if (!settings) return;
    const before = settings;
    setSettings({ ...settings, consent });
    setError('');
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/marketing/settings', {
      params: { path: { store_id: storeId } },
      body: { consent, version: before.version },
    });
    if (err) {
      setSettings(before);
      setError(problemMessage(err));
      return;
    }
    setSettings(data);
    setNotice('حُفظ.');
  }

  const field = (name: string) => ({ 'aria-invalid': Boolean(errors[name]) || undefined });
  const fieldError = (name: string) => errors[name] && <span className="field-error">{errors[name]}</span>;
  const tokenSaved = Boolean(pixel?.has_server_token) && !draft.replacing;

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>بكسلات الإعلانات</h1>
          <p className="muted">اربط حساباتك الإعلانية لتعرف أي إعلان جاب طلبًا، ولتتعلّم الإعلانات من مشترياتك. يُرسل كل حدث من المتصفح ومن الخادم معًا ويُحسب مرة واحدة.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      {spec && (
        <div className="split">
          <section className="card" aria-labelledby="h-prov">
            <h2 id="h-prov">المنصات</h2>
            <ul className="prov-list">
              {providers.map((p) => {
                const l = providerLook[p.provider];
                const st = pixelState(p, pixels.find((x) => x.provider === p.provider));
                return (
                  <li key={p.provider}>
                    <button
                      type="button"
                      className="prov"
                      aria-pressed={p.provider === pick}
                      onClick={() => {
                        choose(p.provider);
                      }}
                    >
                      <span className="logo" style={{ background: l.color }} aria-hidden="true">
                        {l.mark}
                      </span>
                      <span className="what">
                        <strong>{l.name}</strong>
                        <small>{st.hint}</small>
                      </span>
                      <span className={`status ${st.cls}`}>{st.label}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
            <ul className="events" aria-label="الأحداث المرسلة">
              <li>مشاهدة منتج</li>
              <li>إضافة للسلة</li>
              <li>بدء إتمام الطلب</li>
              <li>شراء</li>
            </ul>
            <p className="hint">رقم موبايل العميل لا يخرج إلا مشفّرًا (hash) وبعد موافقته حيث يلزم.</p>
          </section>

          <form
            className="panel"
            aria-labelledby="h-edit"
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <h2 id="h-edit">{look.name}</h2>
            <div className="toggle-row">
              <span>مفعّل</span>
              <button
                type="button"
                className="toggle"
                aria-pressed={draft.enabled}
                aria-label="مفعّل"
                onClick={() => {
                  edit({ enabled: !draft.enabled });
                }}
              />
            </div>
            <label>
              {look.idLabel}
              <input
                dir="ltr"
                autoComplete="off"
                placeholder={look.placeholder}
                value={draft.pixelId}
                {...field('pixel_id')}
                onChange={(e) => {
                  edit({ pixelId: e.target.value });
                }}
              />
              {fieldError('pixel_id')}
            </label>
            {spec.label && (
              <label>
                Conversion label
                <input
                  dir="ltr"
                  autoComplete="off"
                  placeholder="AbC-123_xyz"
                  value={draft.label}
                  {...field('label')}
                  onChange={(e) => {
                    edit({ label: e.target.value });
                  }}
                />
                {fieldError('label')}
              </label>
            )}
            {spec.test_code && (
              <div className="field-with-help">
                <label>
                  رمز الاختبار (اختياري)
                  <input
                    dir="ltr"
                    autoComplete="off"
                    placeholder="TEST12345"
                    value={draft.testCode}
                    {...field('test_code')}
                    onChange={(e) => {
                      edit({ testCode: e.target.value });
                    }}
                  />
                  {fieldError('test_code')}
                </label>
                <small className="muted">لترى الأحداث في أداة الاختبار عند {look.name}. احذفه بعد التجربة.</small>
              </div>
            )}
            {spec.server ? (
              <div>
                {tokenSaved ? (
                  <>
                    <span className="field-label">{look.tokenLabel}</span>
                    <div className="saved-token">
                      <span>محفوظ ومشفّر · لا يظهر مرة أخرى</span>
                      <button
                        type="button"
                        className="link small"
                        onClick={() => {
                          edit({ replacing: true });
                        }}
                      >
                        استبدال
                      </button>
                    </div>
                  </>
                ) : (
                  <label>
                    {look.tokenLabel}
                    <input
                      type="password"
                      dir="ltr"
                      autoComplete="off"
                      placeholder="الصقه هنا"
                      value={draft.token}
                      {...field('server_token')}
                      onChange={(e) => {
                        edit({ token: e.target.value });
                      }}
                    />
                    {fieldError('server_token')}
                  </label>
                )}
                <small className="muted">يرسل الشراء من الخادم أيضًا، فيُحسب حتى مع حاجبات الإعلانات. اختياري.</small>
              </div>
            ) : (
              <p className="hint">من المتصفح فقط حاليًا.</p>
            )}
            <div className="panel-actions">
              <button type="submit" disabled={busy}>
                {pixel ? 'حفظ' : 'ربط'}
              </button>
              {pixel && (
                <button type="button" className="danger" disabled={busy} onClick={() => void disconnect()}>
                  فصل
                </button>
              )}
            </div>
          </form>
        </div>
      )}

      {settings && (
        <section className="card editor-card consent-card" aria-labelledby="h-consent">
          <h2 id="h-consent">موافقة العميل على ملفات التتبع</h2>
          <p className="muted">لا تعمل البكسلات قبل الموافقة حين تظهر رسالة الموافقة. صفحات المتجر لا تبطؤ: تُحمّل البكسلات بعد ظهور الصفحة.</p>
          <div className="choices" role="radiogroup" aria-labelledby="h-consent">
            {consentChoices.map((c) => (
              <label className="choice" key={c.id}>
                <input type="radio" name="consent" checked={settings.consent === c.id} onChange={() => void chooseConsent(c.id)} />
                <span>
                  <strong>{c.title}</strong>
                  <small className="muted">{c.text}</small>
                </span>
              </label>
            ))}
          </div>
        </section>
      )}
    </Layout>
  );
}
