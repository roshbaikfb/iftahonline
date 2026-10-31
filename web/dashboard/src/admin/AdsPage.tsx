import { useCallback, useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { postFile } from '../lib/upload';
import { field } from '../session';
import {
  bannerState,
  bannerStateLabel,
  countries,
  countryName,
  liveCount,
  period,
  placementName,
  placements,
  startOfDay,
  type AdBanner,
  type AdSettings,
  type Country,
  type Placement,
} from './ads';
import { ConsoleLayout } from './ConsoleLayout';

function fetchAds() {
  return api.GET('/admin/v1/ads');
}

type UploadType = 'image/jpeg' | 'image/png' | 'image/webp';
const uploadTypes: string[] = ['image/jpeg', 'image/png', 'image/webp'];

// AdsPage manages ads in free stores (ADR-0062, the approved mockup): the
// source of each placement, AdSense's settings, and our own banners.
export function AdsPage() {
  const [settings, setSettings] = useState<AdSettings | null>(null);
  const [banners, setBanners] = useState<AdBanner[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [now] = useState(() => new Date());

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchAds>>) => {
    if (r.data) {
      setSettings(r.data.settings);
      setBanners(r.data.banners);
    } else setError(problemMessage(r.error));
  }, []);

  useEffect(() => {
    let live = true;
    void fetchAds().then((r) => {
      if (live) apply(r);
    });
    return () => {
      live = false;
    };
  }, [apply]);

  async function saveSettings(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!settings) return;
    const form = new FormData(e.currentTarget);
    const units: AdSettings['units'] = {};
    for (const p of placements) units[p.key] = field(form, `unit_${p.key}`);
    setBusy(true);
    setError('');
    setNotice('');
    const { data, error: err } = await api.PUT('/admin/v1/ads/settings', {
      body: { adsense_enabled: form.get('adsense_enabled') === 'on', publisher_id: field(form, 'publisher_id'), units, reason: field(form, 'reason') },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setSettings(data);
    setNotice('حُفظت الإعدادات. تصل للمتاجر خلال دقائق.');
  }

  async function addBanner(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    const form = new FormData(el);
    const file = (el.elements.namedItem('image') as HTMLInputElement | null)?.files?.[0];
    if (!file || !uploadTypes.includes(file.type)) {
      setError('اختر صورة WebP أو PNG أو JPEG.');
      return;
    }
    setBusy(true);
    setError('');
    setNotice('');
    try {
      const up = await api.POST('/admin/v1/ads/uploads', { body: { content_type: file.type as UploadType } });
      if (up.error) throw new Error(problemMessage(up.error));
      await postFile(up.data.upload, file, 'الصورة أكبر من 2 ميجابايت.');
      const country = field(form, 'country');
      const ends = field(form, 'ends_at');
      const { data, error: err } = await api.POST('/admin/v1/ads/banners', {
        body: {
          title: field(form, 'title'),
          upload_key: up.data.key,
          link_url: field(form, 'link_url'),
          placement: field(form, 'placement') as Placement,
          ...(country ? { country: country as Country } : {}),
          starts_at: startOfDay(field(form, 'starts_at')),
          ...(ends ? { ends_at: startOfDay(ends) } : {}),
          weight: Number(field(form, 'weight')),
          reason: field(form, 'reason'),
        },
      });
      if (err) throw new Error(problemMessage(err));
      setBanners((bs) => [data, ...bs]);
      el.reset();
      setNotice('أُضيف البانر.');
    } catch (x) {
      setError(x instanceof Error ? x.message : String(x));
    } finally {
      setBusy(false);
    }
  }

  async function setActive(b: AdBanner, active: boolean) {
    const reason = window.prompt(active ? 'سبب تشغيل البانر' : 'سبب إيقاف البانر');
    if (!reason) return;
    setError('');
    const { error: err } = await api.PUT('/admin/v1/ads/banners/{banner_id}/active', { params: { path: { banner_id: b.id } }, body: { active, reason } });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setBanners((bs) => bs.map((x) => (x.id === b.id ? { ...x, active } : x)));
  }

  const live = liveCount(banners, now);

  return (
    <ConsoleLayout>
      <div>
        <h1>الإعلانات في المتاجر المجانية</h1>
        <p className="muted">تظهر في المتاجر على الباقة المجانية فقط: الرئيسية وصفحات التصنيف والمنتج. أبدًا في السلة والدفع والطلبات.</p>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}

      {settings && (
        <form className="card ads-sources" onSubmit={(e) => void saveSettings(e)} aria-labelledby="h-sources">
          <h2 id="h-sources">مصدر الإعلان في كل مكان</h2>
          <p className="muted small">
            في كل مكان نعرض بانرًا من بانراتنا إن وُجد واحد نشط له، وإلا إعلان Google إن كان مفعّلًا، وإلا لا شيء (يبقى المكان فارغًا ولا يُحجز).
          </p>
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">المكان</th>
                  <th scope="col">المقاس</th>
                  <th scope="col">بانرات نشطة</th>
                  <th scope="col">Google AdSense (رقم الوحدة)</th>
                </tr>
              </thead>
              <tbody>
                {placements.map((p) => (
                  <tr key={p.key}>
                    <td>{p.name}</td>
                    <td className="muted ltr">{p.size}</td>
                    <td>{live[p.key]}</td>
                    <td>
                      <input
                        name={`unit_${p.key}`}
                        aria-label={`${p.name} — رقم وحدة AdSense`}
                        defaultValue={settings.units[p.key] ?? ''}
                        pattern="[0-9]{10}"
                        inputMode="numeric"
                        dir="ltr"
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="inline-form">
            <label className="grow">
              <span className="field-label">رقم الناشر في AdSense</span>
              <input name="publisher_id" defaultValue={settings.publisher_id} pattern="ca-pub-[0-9]{16}" placeholder="ca-pub-0000000000000000" dir="ltr" />
            </label>
            <label className="check">
              <input type="checkbox" name="adsense_enabled" defaultChecked={settings.adsense_enabled} /> تفعيل إعلانات Google
            </label>
            <label className="grow">
              <span className="field-label">السبب (إجباري)</span>
              <input name="reason" required minLength={3} maxLength={500} />
            </label>
            <button type="submit" className="button primary" disabled={busy}>
              حفظ الإعدادات
            </button>
          </div>
          <p className="muted small">
            يُنشر ‎/ads.txt‎ تلقائيًا على كل دومين متجر مجاني: <code dir="ltr">{settings.ads_txt.trim() || 'google.com, pub-…, DIRECT, f08c47fec0942fa0'}</code>
          </p>
        </form>
      )}

      <section className="table-card" aria-labelledby="h-banners">
        <div className="card-head">
          <h2 id="h-banners">بانراتنا</h2>
          <span className="muted small">نعدّ النقرات فقط، بدون تتبع للزائر.</span>
        </div>
        {banners.length === 0 ? (
          <p className="empty flat">لا توجد بانرات بعد.</p>
        ) : (
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">البانر</th>
                  <th scope="col">المكان</th>
                  <th scope="col">الدولة</th>
                  <th scope="col">الفترة</th>
                  <th scope="col">الوزن</th>
                  <th scope="col">نقرات 30 يومًا</th>
                  <th scope="col">الحالة</th>
                  <th scope="col">
                    <span className="visually-hidden">إجراء</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {banners.map((b) => {
                  const st = bannerState(b, now);
                  return (
                    <tr key={b.id}>
                      <td>
                        <div className="ad-banner-cell">
                          {b.image_url ? (
                            <img className="ad-thumb" src={b.image_url} alt="" width={64} height={32} loading="lazy" />
                          ) : (
                            <span className="ad-thumb" />
                          )}
                          <span>
                            <strong>{b.title}</strong>
                            <small className="muted ltr">{b.link_url}</small>
                          </span>
                        </div>
                      </td>
                      <td>{placementName(b.placement)}</td>
                      <td>{countryName(b.country)}</td>
                      <td className="muted">{period(b)}</td>
                      <td>{b.weight}</td>
                      <td>
                        <strong>{b.clicks.toLocaleString('en-US')}</strong>
                      </td>
                      <td>
                        <span className={`status ad-${st}`}>{bannerStateLabel[st]}</span>
                      </td>
                      <td>
                        <button type="button" className="link-button" onClick={() => void setActive(b, !b.active)}>
                          {b.active ? 'إيقاف' : 'تشغيل'}
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        <form className="inline-form foot" onSubmit={(e) => void addBanner(e)} aria-label="بانر جديد">
          <label className="grow">
            <span className="field-label">العنوان (للكونسول وقارئ الشاشة)</span>
            <input name="title" required maxLength={120} />
          </label>
          <label className="grow">
            <span className="field-label">الصورة (WebP/PNG/JPEG، تُعاد ترميزها)</span>
            <input name="image" type="file" required accept="image/png,image/jpeg,image/webp" />
          </label>
          <label className="grow">
            <span className="field-label">الرابط (https)</span>
            <input name="link_url" type="url" required pattern="https://.*" maxLength={500} placeholder="https://iftahonline.com/start" dir="ltr" />
          </label>
          <label>
            <span className="field-label">المكان</span>
            <select name="placement" defaultValue="home_top">
              {placements.map((p) => (
                <option key={p.key} value={p.key}>
                  {p.name} {p.size}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-label">الدولة</span>
            <select name="country" defaultValue="">
              <option value="">كل الدول</option>
              {countries.map((c) => (
                <option key={c.key} value={c.key}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-label">من</span>
            <input name="starts_at" type="date" required />
          </label>
          <label>
            <span className="field-label">إلى (اختياري)</span>
            <input name="ends_at" type="date" />
          </label>
          <label>
            <span className="field-label">الوزن (1–100)</span>
            <input name="weight" type="number" required min={1} max={100} defaultValue={10} />
          </label>
          <label className="grow">
            <span className="field-label">السبب</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            إضافة البانر
          </button>
        </form>
      </section>
    </ConsoleLayout>
  );
}
