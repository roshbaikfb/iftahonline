import { useEffect, useState } from 'react';
import { useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import type { ProductFeed } from '../lib/marketing';
import { problemMessage } from '../lib/problem';
import { storefrontURL } from '../lib/storefront';

// Feeds turns the store's product feeds on or off and shows their links
// (6g, the approved mockup; ADR-0044): one for Google Merchant Center, one
// for Meta Commerce Manager, with where to paste each. A new key stops the
// old links.
export function Feeds() {
  const { storeId = '' } = useParams();
  const [feed, setFeed] = useState<ProductFeed | null>(null);
  const [slug, setSlug] = useState('');
  const [copied, setCopied] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([api.GET('/v1/stores/{store_id}/marketing/feed', path), api.GET('/v1/stores/{store_id}', path)]).then(([f, s]) => {
      if (!live) return;
      if (s.data) setSlug(s.data.slug);
      if (f.error) setError(problemMessage(f.error));
      else setFeed(f.data);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function renew(note: string) {
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/marketing/feed', { params: { path: { store_id: storeId } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setFeed(data);
    setCopied('');
    setNotice(note);
  }

  async function turnOff() {
    if (!globalThis.confirm('إيقاف الكتالوج؟ تتوقف الروابط في Google وMeta فورًا.')) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/marketing/feed', { params: { path: { store_id: storeId } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setFeed({ enabled: false });
    setCopied('');
    setNotice('أُوقف الكتالوج: الروابط القديمة لم تعد تعمل.');
  }

  function copy(which: string, url: string) {
    void navigator.clipboard.writeText(url).then(() => {
      setCopied(which);
    });
  }

  const origin = slug ? storefrontURL(slug) : '';
  const cards = feed?.enabled
    ? [
        {
          id: 'google',
          name: 'Google Merchant Center',
          mark: 'G',
          color: '#15803d',
          url: origin + (feed.google_path ?? ''),
          steps: ['افتح Merchant Center، ثم «المنتجات» ثم «إضافة منتجات».', 'اختر «إضافة منتجات من ملف» ثم «إدخال رابط».', 'الصق الرابط واختر الجلب كل يوم.'],
        },
        {
          id: 'meta',
          name: 'Meta Commerce Manager',
          mark: 'M',
          color: '#1d4ed8',
          url: origin + (feed.meta_path ?? ''),
          steps: ['افتح Commerce Manager، ثم الكتالوج ثم «مصادر البيانات».', 'اختر «إضافة عناصر» ثم «موجز بيانات» ثم «رابط».', 'الصق الرابط واختر التحديث كل ساعة.'],
        },
      ]
    : [];

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>كتالوج المنتجات للإعلانات</h1>
          <p className="muted">رابط بمنتجاتك تقرؤه Google وMeta لتعرضها في إعلانات التسوق والكتالوج. يتحدّث وحده كلما غيّرت منتجًا.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      {feed && !feed.enabled && (
        <section className="card offcard" aria-labelledby="h-off">
          <h2 id="h-off">الكتالوج متوقف</h2>
          <ul className="includes">
            <li>كل منتج نشط له صورة، وكل مقاس أو لون منه عنصر مستقل بسعره وتوفّره.</li>
            <li>السعر قبل الخصم وبعده، ورابط يفتح المنتج على المقاس نفسه.</li>
            <li>لا يحوي أي بيانات عملاء أو طلبات.</li>
          </ul>
          <button type="button" className="primary" disabled={busy} onClick={() => void renew('الكتالوج يعمل. انسخ الرابطين إلى المنصتين.')}>
            تشغيل الكتالوج
          </button>
        </section>
      )}

      {feed?.enabled && (
        <>
          <div className="feed-grid">
            {cards.map((c) => (
              <section className="card editor-card feed-card" key={c.id} aria-labelledby={`h-${c.id}`}>
                <h2 id={`h-${c.id}`}>
                  <span className="logo" style={{ background: c.color }} aria-hidden="true">
                    {c.mark}
                  </span>
                  {c.name}
                </h2>
                <div className="field-label">
                  <label htmlFor={`url-${c.id}`}>رابط الكتالوج</label>
                  <span className="copy">
                    <input id={`url-${c.id}`} readOnly dir="ltr" value={c.url} onFocus={(e) => {
                        e.currentTarget.select();
                      }}
                    />
                    <button
                      type="button"
                      className="secondary"
                      aria-label={`نسخ رابط ${c.name}`}
                      onClick={() => {
                        copy(c.id, c.url);
                      }}
                    >
                      {copied === c.id ? 'نُسخ' : 'نسخ'}
                    </button>
                  </span>
                </div>
                <ol className="steps-list">
                  {c.steps.map((s) => (
                    <li key={s}>{s}</li>
                  ))}
                </ol>
              </section>
            ))}
          </div>
          <section className="card editor-card" aria-labelledby="h-key">
            <h2 id="h-key">أمان الرابط</h2>
            <p className="muted">في الرابط مفتاح سري: من يعرفه يرى قائمة منتجاتك (وهي ظاهرة أصلًا في متجرك). إن وصل لغير أهله جدّده، ثم الصق الرابط الجديد في المنصتين.</p>
            <div className="actions-row">
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={() => {
                  if (globalThis.confirm('تجديد المفتاح؟ تتوقف الروابط الحالية، وتلصق الجديدة في المنصتين.')) void renew('جُدّد المفتاح: الروابط القديمة توقفت، الصق الجديدة في المنصتين.');
                }}
              >
                تجديد المفتاح
              </button>
              <button type="button" className="danger" disabled={busy} onClick={() => void turnOff()}>
                إيقاف الكتالوج
              </button>
            </div>
          </section>
        </>
      )}
    </Layout>
  );
}
