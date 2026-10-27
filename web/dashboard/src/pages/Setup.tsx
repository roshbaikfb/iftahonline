import { useEffect, useState, type CSSProperties, type ChangeEvent } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { WizardFrame } from '../components/WizardFrame';
import { toMinor } from '../lib/amounts';
import { activities, looks, palette, tint, type Activity, type Look, type Onboarding } from '../lib/onboarding';
import { problemMessage } from '../lib/problem';
import { storefrontURL } from '../lib/storefront';

type ShippingConfig = components['schemas']['ShippingConfig'];

// The steps of this page, as the wizard counts them (sign-up is 0 and 1).
const stepActivity = 2;
const stepLook = 3;
const stepShipping = 4;
const stepReady = 5;

const symbols: Record<string, string> = { EGP: 'ج.م', SAR: 'ر.س', AED: 'د.إ' };
const logoTypes = ['image/png', 'image/jpeg', 'image/webp'] as const;

type LogoState = { kind: 'none' } | { kind: 'uploading' } | { kind: 'done'; name: string } | { kind: 'failed'; message: string };

// Setup is the wizard after sign-up (ADR-0034, the approved mockup): what
// the store sells, its look, colour and logo, quick shipping settings,
// and the starter template applied in the background. A merchant who
// leaves comes back here until the wizard is closed.
export function Setup() {
  const { storeId = '' } = useParams();
  const navigate = useNavigate();
  const [ob, setOb] = useState<Onboarding | null>(null);
  const [step, setStep] = useState(stepActivity);
  const [activity, setActivity] = useState<Activity>('clothing');
  const [look, setLook] = useState<Look>('classic');
  const [color, setColor] = useState<string | null>(null);
  const [logo, setLogo] = useState<LogoState>({ kind: 'none' });
  const [ship, setShip] = useState<ShippingConfig | null>(null);
  const [shipChoice, setShipChoice] = useState<'all' | 'some'>('all');
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [rate, setRate] = useState('');
  const [freeOver, setFreeOver] = useState('');
  const [codOn, setCodOn] = useState(true);
  const [codFee, setCodFee] = useState('');
  const [shippingSet, setShippingSet] = useState<boolean | null>(null);
  const [slug, setSlug] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  // Where the merchant is: a closed wizard leaves; a chosen template goes
  // straight to the last step.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/onboarding', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
      if (!live) return;
      if (err) {
        setError(problemMessage(err));
        return;
      }
      setOb(data);
      if (data.activity) setActivity(data.activity);
      if (data.look) setLook(data.look);
      if (data.color) setColor(data.color);
      if (data.template.status !== 'none') setStep(stepReady);
    });
    void api.GET('/v1/stores/{store_id}/shipping', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (!live || !data) return;
      setShip(data);
      setShippingSet(data.zones.length > 0);
    });
    void api.GET('/v1/stores/{store_id}', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setSlug(data.slug);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  // While the template is applied, ask again every two seconds.
  const preparing = ob?.template.status === 'preparing';
  useEffect(() => {
    if (!preparing) return;
    const t = setInterval(() => {
      void api.GET('/v1/stores/{store_id}/onboarding', { params: { path: { store_id: storeId } } }).then(({ data }) => {
        if (data) setOb(data);
      });
    }, 2000);
    return () => {
      clearInterval(t);
    };
  }, [preparing, storeId]);

  const chosen = color ?? palette(activity)[0] ?? '#0f2e52';
  const currency = symbols[ship?.currency ?? 'EGP'] ?? '';

  async function uploadLogo(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    const type = logoTypes.find((t) => t === file.type);
    if (!type) {
      setLogo({ kind: 'failed', message: 'الشعار يكون PNG أو JPG أو WebP.' });
      return;
    }
    setLogo({ kind: 'uploading' });
    try {
      const { data, error: err } = await api.POST('/v1/stores/{store_id}/storefront/logo/uploads', {
        params: { path: { store_id: storeId } },
        body: { content_type: type },
      });
      if (err) throw new Error(problemMessage(err));
      const form = new FormData();
      for (const [k, v] of Object.entries(data.upload.fields)) form.append(k, v);
      form.append('file', file);
      const up = await fetch(data.upload.url, { method: 'POST', body: form });
      if (!up.ok) throw new Error('تعذّر رفع الملف، جرّب صورة أصغر (حتى 2 ميجابايت).');
      const done = await api.POST('/v1/stores/{store_id}/storefront/logo/uploads/{asset_id}/complete', {
        params: { path: { store_id: storeId, asset_id: data.asset.id } },
      });
      if (done.error) throw new Error(problemMessage(done.error));
      setLogo({ kind: 'done', name: file.name });
    } catch (err) {
      setLogo({ kind: 'failed', message: problemMessage(err) });
    }
  }

  // saveShipping saves the quick shipping settings; it returns an error
  // message, or "" when saved.
  async function saveShipping(): Promise<string> {
    if (!ship) return 'تعذّر تحميل إعدادات الشحن';
    const rateMinor = toMinor(rate);
    if (rateMinor === undefined) return 'سعر الشحن: اكتب مبلغًا صحيحًا';
    const free = freeOver.trim() === '' ? undefined : toMinor(freeOver);
    if (free === undefined && freeOver.trim() !== '') return 'الشحن المجاني: اكتب مبلغًا صحيحًا';
    const fee = codOn && codFee.trim() !== '' ? toMinor(codFee) : 0;
    if (fee === undefined) return 'رسوم الدفع عند الاستلام: اكتب مبلغًا صحيحًا';
    const areas = shipChoice === 'all' ? ship.areas.map((a) => a.code) : ship.areas.filter((a) => picked.has(a.code)).map((a) => a.code);
    if (areas.length === 0) return 'اختر محافظة واحدة على الأقل';
    const settings = await api.PUT('/v1/stores/{store_id}/shipping/settings', {
      params: { path: { store_id: storeId } },
      body: {
        cod_fee: { fixed_minor: fee, bp: 0, min_minor: 0, max_minor: 0 },
        version: ship.settings.version,
        ...(free === undefined ? {} : { free_over_minor: free }),
      },
    });
    if (settings.error) return problemMessage(settings.error);
    const zone = await api.POST('/v1/stores/{store_id}/shipping/zones', {
      params: { path: { store_id: storeId } },
      body: { name: shipChoice === 'all' ? 'كل المحافظات' : 'مناطق التوصيل', areas, rate_minor: rateMinor, cities: [] },
    });
    if (zone.error) return problemMessage(zone.error);
    return '';
  }

  // prepare saves the shipping step (unless skipped or already set) and
  // applies the template.
  async function prepare(skipShipping: boolean) {
    setBusy(true);
    setError('');
    try {
      if (!skipShipping && shippingSet === false) {
        const msg = await saveShipping();
        if (msg) {
          setError(msg);
          return;
        }
        setShippingSet(true);
      }
      await choose();
    } finally {
      setBusy(false);
    }
  }

  async function choose() {
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/onboarding/template', {
      params: { path: { store_id: storeId } },
      body: { activity, look, color: chosen },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setOb(data);
    setStep(stepReady);
  }

  async function finish() {
    setBusy(true);
    const { error: err } = await api.POST('/v1/stores/{store_id}/onboarding/complete', { params: { path: { store_id: storeId } } });
    setBusy(false);
    if (err) setError(problemMessage(err));
    else void navigate(`/stores/${storeId}/orders`);
  }

  if (ob?.status === 'completed') return <Navigate to={`/stores/${storeId}/orders`} replace />;

  const nav = (next: () => void, label = 'متابعة') => (
    <div className="wiz-nav">
      <button
        type="button"
        className="secondary"
        onClick={() => {
          setError('');
          setStep(step - 1);
        }}
        hidden={step === stepActivity}
      >
        رجوع
      </button>
      <span className="grow" />
      <button type="button" onClick={next}>
        {label}
      </button>
    </div>
  );

  return (
    <WizardFrame step={step}>
      {step === stepActivity && (
        <>
          <div className="wiz-head">
            <h1>ماذا تبيع؟</h1>
            <p>نجهّز لك تصنيفات ومنتجات تجريبية تناسب نشاطك، تحذفها بضغطة عندما تضيف منتجاتك.</p>
          </div>
          <div className="wiz-activities">
            {activities.map((a) => (
              <button
                key={a.id}
                type="button"
                className="choice"
                aria-pressed={a.id === activity}
                style={{ '--c': a.color } as CSSProperties}
                onClick={() => {
                  setActivity(a.id);
                  setColor(null);
                }}
              >
                <span className="icon" aria-hidden="true">
                  <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                    <path d={a.icon} />
                  </svg>
                </span>
                {a.label}
              </button>
            ))}
          </div>
          {nav(() => {
            setStep(stepLook);
          })}
        </>
      )}

      {step === stepLook && (
        <>
          <div className="wiz-head">
            <h1>شكل متجرك</h1>
            <p>اختر شكلًا ولونًا، وارفع شعارك إن وُجد. يمكنك تغيير كل ذلك لاحقًا.</p>
          </div>
          <div className="wiz-looks" style={{ '--c': chosen, '--t': tint(chosen, 0.13) } as CSSProperties}>
            {looks.map((l) => (
              <button
                key={l.id}
                type="button"
                className={`look look-${l.id}`}
                aria-pressed={l.id === look}
                onClick={() => {
                  setLook(l.id);
                }}
              >
                <span className="preview" aria-hidden="true">
                  <span className="p-head">
                    <span className="p-logo" />
                    <span className="p-name" />
                  </span>
                  <span className="p-hero">
                    <span className="p-banner">
                      <span className="p-text" />
                    </span>
                    <span className="p-side" />
                  </span>
                  <span className="p-chips">
                    <span />
                    <span />
                    <span />
                    <span />
                  </span>
                  <span className="p-grid">
                    <span />
                    <span />
                    <span />
                    <span />
                  </span>
                </span>
                <span className="caption">
                  <strong>{l.name}</strong>
                  <small>{l.hint}</small>
                </span>
              </button>
            ))}
          </div>
          <fieldset className="wiz-choices">
            <legend>اللون الأساسي</legend>
            <div className="swatches">
              {palette(activity).map((hex, i) => (
                <button
                  key={hex}
                  type="button"
                  className="swatch"
                  aria-label={i === 0 ? 'لون النشاط المقترح' : `لون ${String(i + 1)}`}
                  aria-pressed={hex === chosen}
                  style={{ '--sw': hex } as CSSProperties}
                  onClick={() => {
                    setColor(hex);
                  }}
                />
              ))}
              <label className="custom">
                لون آخر
                <input
                  type="color"
                  value={chosen}
                  onChange={(e) => {
                    setColor(e.target.value.toLowerCase());
                  }}
                />
              </label>
            </div>
          </fieldset>
          <label className="wiz-logo">
            <span className="icon" aria-hidden="true">
              <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <path d="M12 16V4M7 9l5-5 5 5M4 16v3a1 1 0 001 1h14a1 1 0 001-1v-3" />
              </svg>
            </span>
            <span className="txt">
              <strong>ارفع شعارك (اختياري)</strong>
              <small aria-live="polite">
                {logo.kind === 'none' && 'PNG أو JPG أو WebP. بدون شعار نكتب اسم متجرك بخط واضح.'}
                {logo.kind === 'uploading' && 'جارٍ رفع الشعار…'}
                {logo.kind === 'done' && `تم رفع «${logo.name}»، يظهر في متجرك خلال لحظات.`}
                {logo.kind === 'failed' && logo.message}
              </small>
            </span>
            <input type="file" accept="image/png,image/jpeg,image/webp" className="visually-hidden" onChange={(e) => void uploadLogo(e)} />
          </label>
          {nav(() => {
            setStep(stepShipping);
          })}
        </>
      )}

      {step === stepShipping && (
        <>
          <div className="wiz-head">
            <h1>التوصيل والدفع عند الاستلام</h1>
            <p>إعداد سريع يكفي لأول طلب. التفاصيل (أسعار لكل منطقة ومدن بسعر خاص) من «مناطق الشحن».</p>
          </div>
          {shippingSet ? (
            <p className="ok-note">مناطق الشحن مضبوطة بالفعل في متجرك؛ تعدّلها من «مناطق الشحن».</p>
          ) : (
            <>
              <fieldset className="wiz-choices">
                <legend>إلى أين توصّل؟</legend>
                <div className="chips">
                  <button
                    type="button"
                    className="chip"
                    aria-pressed={shipChoice === 'all'}
                    onClick={() => {
                      setShipChoice('all');
                    }}
                  >
                    كل المحافظات ({ship?.areas.length ?? 0})
                  </button>
                  <button
                    type="button"
                    className="chip"
                    aria-pressed={shipChoice === 'some'}
                    onClick={() => {
                      setShipChoice('some');
                    }}
                  >
                    محافظات أختارها
                  </button>
                </div>
                {shipChoice === 'some' && (
                  <div className="checks box">
                    {ship?.areas.map((a) => (
                      <label key={a.code}>
                        <input
                          type="checkbox"
                          checked={picked.has(a.code)}
                          onChange={(e) => {
                            const next = new Set(picked);
                            if (e.target.checked) next.add(a.code);
                            else next.delete(a.code);
                            setPicked(next);
                          }}
                        />
                        {a.name_ar}
                      </label>
                    ))}
                  </div>
                )}
              </fieldset>
              <div className="cols-2">
                <label>
                  سعر الشحن
                  <span className="money">
                    <input
                      inputMode="decimal"
                      placeholder="50"
                      value={rate}
                      onChange={(e) => {
                        setRate(e.target.value);
                      }}
                    />
                    <span>{currency}</span>
                  </span>
                </label>
                <label>
                  شحن مجاني للطلبات من (اختياري)
                  <span className="money">
                    <input
                      inputMode="decimal"
                      placeholder="1000"
                      value={freeOver}
                      onChange={(e) => {
                        setFreeOver(e.target.value);
                      }}
                    />
                    <span>{currency}</span>
                  </span>
                </label>
              </div>
              <div className="box cod">
                <label className="inline">
                  <input
                    type="checkbox"
                    checked={codOn}
                    onChange={(e) => {
                      setCodOn(e.target.checked);
                    }}
                  />
                  رسوم على الدفع عند الاستلام
                </label>
                {codOn && (
                  <label>
                    مبلغ ثابت لكل طلب
                    <span className="money">
                      <input
                        inputMode="decimal"
                        placeholder="10"
                        value={codFee}
                        onChange={(e) => {
                          setCodFee(e.target.value);
                        }}
                      />
                      <span>{currency}</span>
                    </span>
                  </label>
                )}
                <small>الدفع عند الاستلام متاح دائمًا. الدفع بالبطاقة تفعّله لاحقًا بربط بوابة دفع.</small>
              </div>
            </>
          )}
          {error && <p role="alert">{error}</p>}
          <div className="wiz-nav">
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setError('');
                setStep(stepLook);
              }}
            >
              رجوع
            </button>
            <span className="grow" />
            {!shippingSet && (
              <button type="button" className="link" disabled={busy} onClick={() => void prepare(true)}>
                تخطَّ، سأضبطه لاحقًا
              </button>
            )}
            <button type="button" disabled={busy} onClick={() => void prepare(false)}>
              {busy ? 'جارٍ الحفظ…' : 'تجهيز متجري'}
            </button>
          </div>
        </>
      )}

      {step === stepReady && ob && (
        <Ready
          ob={ob}
          storeId={storeId}
          slug={slug}
          shippingSet={shippingSet}
          busy={busy}
          error={error}
          onRetry={() => void choose()}
          onFinish={() => void finish()}
        />
      )}
    </WizardFrame>
  );
}

interface ReadyProps {
  ob: Onboarding;
  storeId: string;
  slug: string;
  shippingSet: boolean | null;
  busy: boolean;
  error: string;
  onRetry: () => void;
  onFinish: () => void;
}

// Ready is the last step: the store being prepared, then what was set up.
function Ready({ ob, storeId, slug, shippingSet, busy, error, onRetry, onFinish }: ReadyProps) {
  const status = ob.template.status;
  if (status === 'preparing' || status === 'none') {
    return (
      <div className="wiz-wait" role="status">
        <span className="spinner" aria-hidden="true" />
        <h1>جارٍ تجهيز متجرك…</h1>
        <p>نضع المظهر والتصنيفات والمنتجات التجريبية. يستغرق ذلك ثوانيَ.</p>
      </div>
    );
  }
  if (status === 'failed') {
    return (
      <div className="wiz-wait" role="alert">
        <h1>تعذّر تجهيز متجرك</h1>
        <p>حدث خطأ أثناء التجهيز. حاول مرة أخرى؛ لن يتكرر شيء أُضيف من قبل.</p>
        {error && <p role="alert">{error}</p>}
        <button type="button" disabled={busy} onClick={onRetry}>
          حاول مرة أخرى
        </button>
      </div>
    );
  }
  const lookName = looks.find((l) => l.id === ob.look)?.name ?? '';
  const activityName = activities.find((a) => a.id === ob.activity)?.label ?? '';
  return (
    <div className="wiz-ready">
      <div className="wiz-done">
        <span className="tick" aria-hidden="true">
          <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M5 12l5 5L20 7" />
          </svg>
        </span>
        <h1>متجرك جاهز</h1>
        {slug && <p dir="ltr">{storefrontURL(slug).replace(/^https?:\/\//, '')}</p>}
      </div>
      <ul className="wiz-list">
        <li>
          <span>المظهر واللون والصفحة الرئيسية</span>
          <small>{lookName}</small>
        </li>
        <li>
          <span>تصنيفات {activityName}</span>
          <small>3 تصنيفات</small>
        </li>
        <li>
          <span>منتجات تجريبية</span>
          <small>6 منتجات · «تجريبي»</small>
        </li>
        <li className={shippingSet ? undefined : 'todo'}>
          <span>{shippingSet ? 'الشحن والدفع عند الاستلام' : 'الشحن لم يُضبط بعد: التوصيل مجاني لكل المحافظات حتى تضبطه'}</span>
          {shippingSet ? <small>مضبوط</small> : <Link to={`/stores/${storeId}/shipping`}>مناطق الشحن</Link>}
        </li>
        <li>
          <span>صفحات السياسات (الاستبدال والشحن والخصوصية)</span>
          <small>مسودة للمراجعة</small>
        </li>
      </ul>
      <p className="notice">المنتجات التجريبية تظهر في متجرك بعلامة «تجريبي» ولا يمكن شراؤها. احذفها بضغطة عندما تضيف منتجاتك.</p>
      {error && <p role="alert">{error}</p>}
      <div className="wiz-actions">
        {slug && (
          <a className="button primary" href={storefrontURL(slug)} target="_blank" rel="noopener noreferrer">
            عرض متجري
          </a>
        )}
        <button type="button" className="secondary" disabled={busy} onClick={onFinish}>
          اذهب إلى لوحة التحكم
        </button>
      </div>
    </div>
  );
}
