import { useCallback, useEffect, useRef, useState, type ChangeEvent } from 'react';
import { useParams } from 'react-router';

import '../shop-fonts.css';
import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { ThemePreview, type PreviewHero, type PreviewProduct } from '../components/ThemePreview';
import { uploadLogo } from '../lib/logo';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { storefrontURL } from '../lib/storefront';
import { colorRows, contrast, fonts, hexColor, looks, minContrast, presets, radii, sameSettings, type ThemeSettings } from '../lib/theme';

type Theme = components['schemas']['Theme'];
type LogoState = components['schemas']['StoreLogo'];

const saveDelay = 700; // ms of quiet before the draft is saved

type SaveState = 'idle' | 'saving' | 'saved' | 'error';

// Design is the shop's look (U6, the approved mockup): the layout,
// colours, font, corners, logo, texts and contact, saved as a draft as
// the merchant edits (ADR-0036). Shoppers see the published theme until
// «نشر»; «معاينة في تبويب جديد» opens the shop with the draft, and the
// side shows an approximate shop as it changes.
export function Design() {
  const { storeId = '' } = useParams();
  const [published, setPublished] = useState<Theme | null>(null);
  const [s, setS] = useState<ThemeSettings | null>(null);
  const [hasDraft, setHasDraft] = useState(false);
  const [save, setSave] = useState<SaveState>('idle');
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [phone, setPhone] = useState(false);
  const [presetsOpen, setPresetsOpen] = useState(false);
  const [logo, setLogo] = useState<LogoState | null>(null);
  const [logoBusy, setLogoBusy] = useState('');
  const [store, setStore] = useState<{ name: string; slug: string; currency: string } | null>(null);
  const [categories, setCategories] = useState<string[]>([]);
  const [products, setProducts] = useState<PreviewProduct[]>([]);
  const [hero, setHero] = useState<PreviewHero>({ heading: '' });
  const edited = useRef(false); // the merchant changed something since the last save
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([api.GET('/v1/stores/{store_id}/storefront/theme', path), api.GET('/v1/stores/{store_id}/storefront/draft', path)]).then(
      ([t, d]) => {
        if (!live) return;
        if (!t.data) {
          setError(problemMessage(t.error));
          return;
        }
        setPublished(t.data);
        setS(d.data?.theme ?? t.data.settings);
        setHasDraft(Boolean(d.data?.theme));
        const home = d.data?.home?.sections;
        if (home) setHeroFrom(home);
      },
    );
    void api.GET('/v1/stores/{store_id}', path).then(({ data }) => {
      if (live && data) setStore({ name: data.name, slug: data.slug, currency: data.currency });
    });
    void api.GET('/v1/stores/{store_id}/storefront/logo', path).then(({ data }) => {
      if (live && data) setLogo(data);
    });
    void api.GET('/v1/stores/{store_id}/categories', path).then(({ data }) => {
      if (live && data) setCategories(data.items.filter((c) => !c.parent_id).map((c) => c.name));
    });
    void api.GET('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId }, query: { status: 'active', limit: 4 } } }).then(({ data }) => {
      if (!live || !data) return;
      setProducts(
        data.items.map((p) => {
          const v = p.variants[0];
          const off = v?.compare_at_minor ? Math.round((1 - v.price_minor / v.compare_at_minor) * 100) : 0;
          return {
            title: p.title,
            price: v ? money(v.price_minor, v.currency) : '',
            image: p.images.find((i) => i.status === 'ready')?.urls?.thumb.webp,
            badge: off > 0 ? `−${String(off)}%` : undefined,
          };
        }),
      );
    });
    void api.GET('/v1/stores/{store_id}/storefront/home', path).then(({ data }) => {
      if (live && data) setHeroFrom(data.sections, true);
    });
    function setHeroFrom(sections: components['schemas']['Section'][], fallbackOnly = false) {
      const h = sections.find((x) => x.type === 'hero')?.settings as Record<string, unknown> | undefined;
      const heading = typeof h?.['heading'] === 'string' ? h['heading'] : '';
      if (!heading) return;
      setHero((cur) =>
        fallbackOnly && cur.heading
          ? cur
          : { heading, sub: typeof h?.['subheading'] === 'string' ? h['subheading'] : undefined, button: typeof h?.['button_label'] === 'string' ? h['button_label'] : undefined },
      );
    }
    return () => {
      live = false;
    };
  }, [storeId]);

  // saveDraft sends the theme as the draft now; false when it could not.
  const saveDraft = useCallback(
    async (next: ThemeSettings): Promise<boolean> => {
      clearTimeout(timer.current);
      if (!valid(next)) return false;
      setSave('saving');
      const { error: err } = await api.PUT('/v1/stores/{store_id}/storefront/draft/theme', { params: { path: { store_id: storeId } }, body: next });
      if (err) {
        setSave('error');
        const fields = (err as { errors?: { field: string; message: string }[] }).errors ?? [];
        setFieldErrors(Object.fromEntries(fields.map((f) => [f.field, f.message])));
        setError(fields.length ? '' : problemMessage(err));
        return false;
      }
      edited.current = false;
      setFieldErrors({});
      setError('');
      setHasDraft(true);
      setSave('saved');
      return true;
    },
    [storeId],
  );

  function change(next: ThemeSettings) {
    setS(next);
    edited.current = true;
    clearTimeout(timer.current);
    timer.current = setTimeout(() => void saveDraft(next), saveDelay);
  }

  useEffect(() => () => {
    clearTimeout(timer.current);
  }, []);

  async function flush(): Promise<boolean> {
    if (!s) return false;
    return edited.current ? saveDraft(s) : true;
  }

  async function publish() {
    if (!published || !(await flush())) return;
    setBusy(true);
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/storefront/draft/theme/publish', {
      params: { path: { store_id: storeId } },
      body: { version: published.version },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setPublished(data);
    setS(data.settings);
    setHasDraft(false);
    setSave('idle');
  }

  async function discard() {
    if (!published || !globalThis.confirm('تجاهل التغييرات والرجوع لآخر نسخة منشورة؟')) return;
    clearTimeout(timer.current);
    setBusy(true);
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/storefront/draft/theme', { params: { path: { store_id: storeId } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    edited.current = false;
    setS(published.settings);
    setHasDraft(false);
    setFieldErrors({});
    setSave('idle');
  }

  async function preview() {
    if (!store) return;
    // Opened at the click, so the browser does not block it as a popup.
    const tab = globalThis.open('about:blank', '_blank');
    if (!(await flush())) {
      tab?.close();
      return;
    }
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/storefront/previews', { params: { path: { store_id: storeId } } });
    if (err) {
      tab?.close();
      setError(problemMessage(err));
      return;
    }
    const url = `${storefrontURL(store.slug)}/?preview=${encodeURIComponent(data.token)}`;
    if (tab) {
      tab.opener = null;
      tab.location.href = url;
    }
  }

  async function pickLogo(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setLogoBusy('جارٍ رفع الشعار…');
    try {
      await uploadLogo(storeId, file);
      // The server re-encodes it; look again until it is ready.
      for (let i = 0; i < 15; i++) {
        const { data } = await api.GET('/v1/stores/{store_id}/storefront/logo', { params: { path: { store_id: storeId } } });
        if (data) setLogo(data);
        if (!data?.latest || data.latest.status === 'ready' || data.latest.status === 'failed') break;
        await new Promise((r) => setTimeout(r, 1000));
      }
      setLogoBusy('');
    } catch (err) {
      setLogoBusy(problemMessage(err));
    }
  }

  async function removeLogo() {
    if (!globalThis.confirm('إزالة الشعار من متجرك؟ يظهر اسم المتجر مكانه.')) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/storefront/logo', { params: { path: { store_id: storeId } } });
    if (err) setLogoBusy(problemMessage(err));
    else setLogo({});
  }

  if (!s || !published) {
    return (
      <Layout storeId={storeId}>
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const ratio = contrast(s.colors.text, s.colors.background);
  const logoURL = logo?.current?.urls?.small.webp;
  const font = fonts.find((f) => f.id === s.font);
  const dirty = hasDraft && !sameSettings(s, published.settings);
  const err = (k: string) => fieldErrors[k];

  return (
    <Layout storeId={storeId}>
      <div className="page-head design-head">
        <div>
          <div className="title-line">
            <h1>التصميم</h1>
            {(dirty || save === 'saving') && <span className="status s-pending">تغييرات لم تُنشر</span>}
          </div>
          <p className="muted desk-only">تُحفظ تلقائيًا كمسودة. العملاء يرون آخر نسخة منشورة حتى تضغط «نشر».</p>
          <p className="visually-hidden" role="status">
            {save === 'saving' ? 'جارٍ حفظ المسودة…' : save === 'saved' ? 'حُفظت المسودة' : save === 'error' ? 'لم تُحفظ المسودة: راجع الحقول المعلّمة' : ''}
          </p>
        </div>
        <div className="actions">
          <button type="button" className="secondary" disabled={busy || !hasDraft} onClick={() => void discard()}>
            تجاهل التغييرات
          </button>
          <button type="button" className="secondary" disabled={busy || !store} onClick={() => void preview()}>
            معاينة في تبويب جديد
          </button>
          <button type="button" className="primary" disabled={busy || !hasDraft} onClick={() => void publish()}>
            نشر
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <div className="design">
        <div className="design-settings">
          <section className="card editor-card" aria-labelledby="h-look">
            <h2 id="h-look">شكل المتجر</h2>
            <div className="looks">
              {looks.map((l) => (
                <button
                  key={l.id}
                  type="button"
                  className={`look-card look-${l.id}`}
                  aria-pressed={(s.look ?? 'classic') === l.id}
                  onClick={() => {
                    change({ ...s, look: l.id });
                  }}
                >
                  <span className="mini" aria-hidden="true">
                    {l.id === 'soft' ? (
                      <span className="m-dots">
                        <i />
                        <i />
                        <i />
                      </span>
                    ) : (
                      <i className="m-bar" />
                    )}
                    {l.id === 'soft' ? (
                      <i className="m-pill" />
                    ) : (
                      <span className="m-cards">
                        {(l.id === 'classic' ? [1, 2, 3, 4] : [1, 2]).map((n) => (
                          <i key={n} />
                        ))}
                      </span>
                    )}
                  </span>
                  <strong>{l.label}</strong>
                  <span className="muted small desk-only">{l.hint}</span>
                </button>
              ))}
            </div>
          </section>

          <section className="card editor-card" aria-labelledby="h-colors">
            <div className="card-head">
              <h2 id="h-colors">الألوان</h2>
              <button
                type="button"
                className="link"
                aria-expanded={presetsOpen}
                onClick={() => {
                  setPresetsOpen(!presetsOpen);
                }}
              >
                ألوان جاهزة
              </button>
            </div>
            {presetsOpen && (
              <div className="presets" role="group" aria-label="ألوان جاهزة">
                {presets.map((p) => (
                  <button
                    key={p.label}
                    type="button"
                    className="preset"
                    onClick={() => {
                      change({ ...s, colors: p.colors });
                      setPresetsOpen(false);
                    }}
                  >
                    <span className="dots" aria-hidden="true">
                      <i style={{ background: p.colors.primary }} />
                      <i style={{ background: p.colors.accent }} />
                      <i style={{ background: p.colors.background }} />
                    </span>
                    {p.label}
                  </button>
                ))}
              </div>
            )}
            <div className="color-rows">
              {colorRows.map((r) => {
                const v = s.colors[r.key];
                const ok = hexColor.test(v);
                return (
                  <div key={r.key} className="color-row">
                    <input
                      type="color"
                      className="swatch-input"
                      value={ok ? v.toLowerCase() : '#000000'}
                      aria-label={`اختر ${r.label}`}
                      onChange={(e) => {
                        change({ ...s, colors: { ...s.colors, [r.key]: e.target.value } });
                      }}
                    />
                    <div className="grow">
                      <strong>
                        <span className="desk-only">{r.label}</span>
                        <span className="phone-only">{r.short}</span>
                      </strong>
                      {r.hint && <div className="muted small desk-only">{r.hint}</div>}
                    </div>
                    <input
                      className="hex"
                      dir="ltr"
                      maxLength={7}
                      value={v.toUpperCase()}
                      aria-label={`كود ${r.label}`}
                      aria-invalid={!ok || Boolean(err(`colors.${r.key}`)) || undefined}
                      onChange={(e) => {
                        // One leading "#", whether or not the merchant types it.
                        const t = e.target.value.trim().replace(/^#+/, '');
                        change({ ...s, colors: { ...s.colors, [r.key]: `#${t}` } });
                      }}
                    />
                  </div>
                );
              })}
            </div>
            {ratio >= minContrast ? (
              <p className="contrast ok">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                  <path d="M20 6 9 17l-5-5" />
                </svg>
                <span className="desk-only">النص واضح على الخلفية (التباين {ratio.toFixed(1)} : 1)</span>
                <span className="phone-only">النص واضح على الخلفية</span>
              </p>
            ) : (
              <p className="contrast bad" role="alert">
                النص غير واضح على الخلفية (التباين {ratio.toFixed(1)} : 1، والمطلوب {minContrast} على الأقل). غيّر أحد اللونين.
              </p>
            )}
          </section>

          <section className="card editor-card" aria-labelledby="h-type">
            <h2 id="h-type">الخط والحواف</h2>
            <label className="field-label">
              الخط
              <select
                value={s.font}
                onChange={(e) => {
                  change({ ...s, font: e.target.value as ThemeSettings['font'] });
                }}
              >
                {fonts.map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.label}
                  </option>
                ))}
              </select>
            </label>
            <p className="font-sample" style={{ fontFamily: font?.family }}>
              تسوّق أحدث التشكيلات بأفضل الأسعار
            </p>
            <div>
              <span className="field-label" id="radius-label">
                <span className="desk-only">زوايا الأزرار والبطاقات</span>
                <span className="phone-only">الزوايا</span>
              </span>
              <select
                className="radius-select"
                aria-labelledby="radius-label"
                value={s.radius}
                onChange={(e) => {
                  change({ ...s, radius: e.target.value as ThemeSettings['radius'] });
                }}
              >
                {radii.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.label}
                  </option>
                ))}
              </select>
              <div className="segmented radius-buttons" role="group" aria-labelledby="radius-label">
                {radii.map((r) => (
                  <button
                    key={r.id}
                    type="button"
                    aria-pressed={s.radius === r.id}
                    onClick={() => {
                      change({ ...s, radius: r.id });
                    }}
                  >
                    {r.label}
                  </button>
                ))}
              </div>
            </div>
          </section>

          <section className="card editor-card" aria-labelledby="h-logo">
            <h2 id="h-logo">الشعار</h2>
            <div className="logo-row">
              <div className="logo-box">{logoURL ? <img src={logoURL} alt="الشعار الحالي" /> : <span>{store?.name ?? ''}</span>}</div>
              <div className="logo-actions">
                <label className="button secondary small">
                  <span className="desk-only">{logoURL ? 'تغيير الشعار' : 'ارفع الشعار'}</span>
                  <span className="phone-only">{logoURL ? 'تغيير' : 'رفع'}</span>
                  <input type="file" accept="image/png,image/jpeg,image/webp" className="visually-hidden" onChange={(e) => void pickLogo(e)} />
                </label>
                {logoURL && (
                  <button type="button" className="link danger-link" onClick={() => void removeLogo()}>
                    إزالة
                  </button>
                )}
              </div>
            </div>
            {logoBusy && <p role="status">{logoBusy}</p>}
            <p className="muted desk-only">PNG أو JPG أو WebP. يظهر بارتفاع 40 بكسل في رأس الصفحة.</p>
          </section>

          <section className="card editor-card texts-card" aria-labelledby="h-text">
            <h2 id="h-text">
              <span className="desk-only">نصوص المتجر</span>
              <span className="phone-only">النصوص والتواصل</span>
            </h2>
            <label className="field-label">
              <span>
                شريط الإعلان <span className="muted desk-only">(اختياري)</span>
              </span>
              <input
                value={s.announcement ?? ''}
                maxLength={120}
                aria-invalid={Boolean(err('announcement')) || undefined}
                onChange={(e) => {
                  change({ ...s, announcement: e.target.value });
                }}
              />
            </label>
            <p className="muted small desk-only">سطر واحد فوق رأس الصفحة. {(s.announcement ?? '').length} من 120 حرفًا.</p>
            <label className="field-label">
              نبذة عن المتجر
              <textarea
                rows={3}
                value={s.about ?? ''}
                maxLength={300}
                onChange={(e) => {
                  change({ ...s, about: e.target.value });
                }}
              />
            </label>
            <p className="muted small desk-only">تظهر في أسفل كل صفحة.</p>
          </section>

          <section className="card editor-card contact-card" aria-labelledby="h-contact">
            <h2 id="h-contact" className="desk-only">
              التواصل وتنبيه المخزون
            </h2>
            <div className="two-cols">
              <label className="field-label">
                واتساب
                <input
                  dir="ltr"
                  inputMode="tel"
                  value={s.contact?.whatsapp ?? ''}
                  maxLength={32}
                  aria-invalid={Boolean(err('contact.whatsapp')) || undefined}
                  onChange={(e) => {
                    change({ ...s, contact: { ...s.contact, whatsapp: e.target.value } });
                  }}
                />
              </label>
              <label className="field-label">
                البريد
                <input
                  dir="ltr"
                  type="email"
                  value={s.contact?.email ?? ''}
                  maxLength={254}
                  aria-invalid={Boolean(err('contact.email')) || undefined}
                  onChange={(e) => {
                    change({ ...s, contact: { ...s.contact, email: e.target.value } });
                  }}
                />
              </label>
            </div>
            {(err('contact.whatsapp') ?? err('contact.email')) && <p className="field-error">{err('contact.whatsapp') ?? err('contact.email')}</p>}
            <p className="muted small desk-only">زر واتساب يظهر في صفحة المنتج ويفتح محادثة فيها اسم المنتج واختياراته.</p>
            <label className="field-label">
              <span className="desk-only">قل «باقي X قطع فقط» عندما يتبقى</span>
              <span className="phone-only">«باقي X قطع» عند</span>
              <span className="inline">
                <input
                  type="number"
                  min={0}
                  max={10}
                  className="narrow-num"
                  value={s.low_stock ?? 5}
                  onChange={(e) => {
                    change({ ...s, low_stock: Math.max(0, Math.min(10, Number(e.target.value) || 0)) });
                  }}
                />
                <span className="muted desk-only">قطع أو أقل · 0 يخفي التنبيه</span>
              </span>
            </label>
          </section>
        </div>

        <section className="design-preview" aria-label="معاينة">
          <div className="card-head bare">
            <strong>معاينة المسودة</strong>
            <div className="segmented" role="group" aria-label="حجم المعاينة">
              <button
                type="button"
                aria-pressed={!phone}
                onClick={() => {
                  setPhone(false);
                }}
              >
                كمبيوتر
              </button>
              <button
                type="button"
                aria-pressed={phone}
                onClick={() => {
                  setPhone(true);
                }}
              >
                هاتف
              </button>
            </div>
          </div>
          <ThemePreview
            settings={s}
            storeName={store?.name ?? ''}
            logo={logoURL}
            categories={categories}
            products={products}
            hero={hero.heading ? hero : { heading: store ? `أهلًا بك في ${store.name}` : '' }}
            phone={phone}
          />
          <p className="muted small">معاينة تقريبية تتغير مع كل تعديل. للمعاينة الكاملة بصفحاتك ومنتجاتك الحقيقية: «معاينة في تبويب جديد».</p>
        </section>
      </div>
      <div className="sticky-actions">
        <button type="button" className="secondary" disabled={busy || !store} onClick={() => void preview()}>
          معاينة
        </button>
        <button type="button" className="primary" disabled={busy || !hasDraft} onClick={() => void publish()}>
          نشر
        </button>
      </div>
    </Layout>
  );
}

// valid is what the server would refuse before it is asked: colours that
// are not "#rrggbb" and text that cannot be read on the background.
function valid(t: ThemeSettings): boolean {
  const c = t.colors;
  return [c.primary, c.accent, c.background, c.text].every((v) => hexColor.test(v)) && contrast(c.text, c.background) >= minContrast;
}
