import { useEffect, useState, type ChangeEvent } from 'react';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { postFile } from '../lib/upload';

type Image = components['schemas']['ProductImage'];

const types = ['image/jpeg', 'image/png', 'image/webp'] as const;
const maxImages = 20; // domain.MaxProductImages

const failures: Record<NonNullable<Image['failure_reason']>, string> = {
  file_too_large: 'الملف أكبر من المسموح',
  unsupported_type: 'نوع الملف غير مدعوم',
  too_many_pixels: 'أبعاد الصورة كبيرة جدًا',
  corrupt_image: 'الملف تالف',
  upload_missing: 'لم يكتمل الرفع',
};

// ProductImages is the product's pictures (the approved mockup): the
// first is the main one; new ones upload straight to object storage and
// are processed by the server (re-encoded, never SVG), so they show once
// ready.
export function ProductImages({ storeId, productId, images: initial }: { storeId: string; productId: string; images: Image[] }) {
  const [images, setImages] = useState(initial);
  const [busy, setBusy] = useState(0);
  const [error, setError] = useState('');
  const processing = images.some((i) => i.status === 'pending' || i.status === 'processing');

  // While pictures are being processed, look again every two seconds.
  useEffect(() => {
    if (!processing) return;
    const t = setTimeout(() => {
      void api.GET('/v1/stores/{store_id}/products/{product_id}', { params: { path: { store_id: storeId, product_id: productId } } }).then(({ data }) => {
        if (data) setImages(data.images);
      });
    }, 2000);
    return () => {
      clearTimeout(t);
    };
  }, [processing, images, storeId, productId]);

  async function upload(e: ChangeEvent<HTMLInputElement>) {
    const files = [...(e.target.files ?? [])].slice(0, maxImages - images.length);
    e.target.value = '';
    setError('');
    for (const file of files) {
      const type = types.find((t) => t === file.type);
      if (!type) {
        setError('الصور تكون JPG أو PNG أو WebP.');
        continue;
      }
      setBusy((n) => n + 1);
      try {
        const { data, error: err } = await api.POST('/v1/stores/{store_id}/products/{product_id}/images', {
          params: { path: { store_id: storeId, product_id: productId } },
          body: { content_type: type },
        });
        if (err) throw new Error(problemMessage(err));
        await postFile(data.upload, file, 'تعذّر رفع الصورة، جرّب صورة أصغر (حتى 15 ميجابايت).');
        const done = await api.POST('/v1/stores/{store_id}/products/{product_id}/images/{image_id}/complete', {
          params: { path: { store_id: storeId, product_id: productId, image_id: data.image.id } },
        });
        if (done.error) throw new Error(problemMessage(done.error));
        setImages((list) => [...list, done.data]);
      } catch (err) {
        setError(problemMessage(err));
      } finally {
        setBusy((n) => n - 1);
      }
    }
  }

  async function remove(id: string) {
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/products/{product_id}/images/{image_id}', {
      params: { path: { store_id: storeId, product_id: productId, image_id: id } },
    });
    if (err) setError(problemMessage(err));
    else setImages((list) => list.filter((i) => i.id !== id));
  }

  async function makeMain(id: string) {
    const { data, error: err } = await api.PATCH('/v1/stores/{store_id}/products/{product_id}/images/{image_id}', {
      params: { path: { store_id: storeId, product_id: productId, image_id: id } },
      body: { position: 0 },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setImages((list) => [data, ...list.filter((i) => i.id !== id)]);
  }

  return (
    <section className="card editor-card" aria-labelledby="images-title">
      <div className="card-head">
        <h2 id="images-title">الصور</h2>
        <span className="muted">
          {images.length} من {maxImages}
        </span>
      </div>
      {error && <p role="alert">{error}</p>}
      <ul className="image-grid">
        {images.map((img, i) => (
          <li key={img.id}>
            {img.status === 'ready' && img.urls ? (
              <img src={img.urls.small.webp} alt={img.alt || `صورة ${String(i + 1)}`} />
            ) : (
              <span className="image-state" role="status">
                {img.status === 'failed' ? failures[img.failure_reason ?? 'corrupt_image'] : 'جارٍ تجهيز الصورة…'}
              </span>
            )}
            {i === 0 ? (
              <span className="tag">الرئيسية</span>
            ) : (
              img.status === 'ready' && (
                <button type="button" className="secondary small main-btn" onClick={() => void makeMain(img.id)}>
                  اجعلها الرئيسية
                </button>
              )
            )}
            <button type="button" className="x remove" aria-label={`احذف الصورة ${String(i + 1)}`} onClick={() => void remove(img.id)}>
              ×
            </button>
          </li>
        ))}
        {images.length < maxImages && (
          <li>
            <label className="add-image">
              <span aria-hidden="true">+</span>
              {busy > 0 ? 'جارٍ الرفع…' : 'أضف صورًا'}
              <input type="file" multiple accept={types.join(',')} className="visually-hidden" disabled={busy > 0} onChange={(e) => void upload(e)} />
            </label>
          </li>
        )}
      </ul>
      <p className="muted small">JPG أو PNG أو WebP حتى 15 ميجابايت. نعيد ضغط الصور تلقائيًا.</p>
    </section>
  );
}
