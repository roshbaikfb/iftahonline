import { useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { uploadImage, type SiteImages, type SiteStory } from './site';

interface Props {
  story: SiteStory;
  images: SiteImages;
  onNotice: (msg: string) => void;
  onError: (msg: string) => void;
}

const imageNames = { logo: 'الشعار', share: 'صورة المشاركة' } as const;

// SiteIdentity edits the logo, the share image, the founding story and
// the video (S4b-1).
export function SiteIdentity({ story, images, onNotice, onError }: Props) {
  const [urls, setUrls] = useState({ logo: images.logo_url, share: images.share_url });
  const [videoID, setVideoID] = useState(story.video_id);
  const [busy, setBusy] = useState(false);

  async function setImage(which: 'logo' | 'share', e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    const reason = field(new FormData(el), 'reason');
    setBusy(true);
    try {
      const key = await uploadImage(el.elements.namedItem('image') as HTMLInputElement | null);
      if (!key) throw new Error('اختر صورة أولًا.');
      const { data, error } = await api.PUT('/admin/v1/site/images/{image}', { params: { path: { image: which } }, body: { upload_key: key, reason } });
      if (error) throw new Error(problemMessage(error));
      setUrls((u) => ({ ...u, [which]: data.url }));
      el.reset();
      onNotice(`حُفظ ${imageNames[which]}.`);
    } catch (x) {
      onError(x instanceof Error ? x.message : String(x));
    } finally {
      setBusy(false);
    }
  }

  async function removeImage(which: 'logo' | 'share') {
    const reason = window.prompt(`سبب حذف ${imageNames[which]}`);
    if (!reason) return;
    const { error } = await api.DELETE('/admin/v1/site/images/{image}', { params: { path: { image: which }, query: { reason } } });
    if (error) {
      onError(problemMessage(error));
      return;
    }
    setUrls((u) => ({ ...u, [which]: '' }));
    onNotice(`حُذف ${imageNames[which]}.`);
  }

  async function saveStory(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    const { data, error } = await api.PUT('/admin/v1/site/story', {
      body: { story_ar: field(form, 'story_ar'), story_en: field(form, 'story_en'), video_url: field(form, 'video_url').trim(), reason: field(form, 'reason') },
    });
    setBusy(false);
    if (error) {
      onError(problemMessage(error));
      return;
    }
    setVideoID(data.video_id);
    onNotice('حُفظت القصة والفيديو.');
  }

  return (
    <>
      <section className="card" aria-labelledby="h-identity">
        <h2 id="h-identity">الشعار وصورة المشاركة</h2>
        <p className="muted small">
          الشعار يظهر أعلى وأسفل كل صفحة بدل الأيقونة. صورة المشاركة تظهر عند إرسال رابط الموقع على واتساب وفيسبوك (الأفضل 1200×630). WebP أو PNG أو JPEG، وتُعاد ترميزها.
        </p>
        <div className="site-images">
          {(['logo', 'share'] as const).map((which) => (
            <form key={which} className="site-image" onSubmit={(e) => void setImage(which, e)} aria-label={imageNames[which]}>
              <h3>{imageNames[which]}</h3>
              {urls[which] ? <img src={urls[which]} alt="" className={`preview ${which}`} /> : <p className="empty flat">لا توجد صورة.</p>}
              <label>
                <span className="field-label">صورة جديدة</span>
                <input name="image" type="file" required accept="image/png,image/jpeg,image/webp" />
              </label>
              <label>
                <span className="field-label">السبب (إجباري)</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <div className="inline-form">
                <button type="submit" className="button primary" disabled={busy}>
                  حفظ {imageNames[which]}
                </button>
                {urls[which] && (
                  <button type="button" className="button" onClick={() => void removeImage(which)}>
                    حذف
                  </button>
                )}
              </div>
            </form>
          ))}
        </div>
      </section>

      <form className="card faq-form" onSubmit={(e) => void saveStory(e)} aria-labelledby="h-story">
        <h2 id="h-story">قصة التأسيس والفيديو</h2>
        <p className="muted small">القصة تظهر في صفحة «عن افتح أونلاين»: سطر فارغ يفصل الفقرات. الفيديو يظهر في الرئيسية وصفحة «عن» ويفتح على يوتيوب.</p>
        <label>
          <span className="field-label">القصة بالعربية</span>
          <textarea name="story_ar" rows={6} maxLength={4000} defaultValue={story.story_ar} />
        </label>
        <label>
          <span className="field-label">القصة بالإنجليزية (اختياري)</span>
          <textarea name="story_en" rows={4} maxLength={4000} defaultValue={story.story_en} dir="ltr" />
        </label>
        <label>
          <span className="field-label">رابط فيديو يوتيوب (اتركه فارغًا لإخفاء الفيديو)</span>
          <input
            name="video_url"
            type="url"
            maxLength={300}
            defaultValue={story.video_id ? `https://youtu.be/${story.video_id}` : ''}
            placeholder="https://youtu.be/…"
            dir="ltr"
          />
        </label>
        {videoID && <p className="muted small ltr">youtu.be/{videoID}</p>}
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">السبب (إجباري)</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            حفظ القصة والفيديو
          </button>
        </div>
      </form>
    </>
  );
}
