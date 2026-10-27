import { useEffect, useState } from 'react';

import { api } from '../api/client';
import { count as plural } from '../lib/home';
import { problemMessage } from '../lib/problem';

// SamplesBanner reminds the merchant that the starter template's sample
// products are still in the store (ADR-0034), and deletes them in one go.
// It heads the products list and the home page (the approved mockups).
export function SamplesBanner({ storeId }: { storeId: string }) {
  const [count, setCount] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId }, query: { limit: 50 } } }).then(({ data }) => {
      if (live && data) setCount(data.items.filter((p) => p.sample).length);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function remove() {
    if (!globalThis.confirm('حذف المنتجات التجريبية من متجرك؟ منتجاتك أنت لا تتأثر.')) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/products/samples', { params: { path: { store_id: storeId } } });
    setBusy(false);
    if (err) setError(problemMessage(err));
    else setCount(0);
  }

  if (count === 0) return null;
  return (
    <div className="notice samples" role="region" aria-label="المنتجات التجريبية">
      <div>
        <strong>{plural(count, 'منتج تجريبي واحد', 'منتجان تجريبيان', 'منتجات تجريبية', 'منتجًا تجريبيًا')} من القالب</strong>
        <p>تظهر في متجرك بعلامة «تجريبي» ولا يمكن شراؤها. عدّلها لتصبح منتجاتك، أو احذفها كلها مرة واحدة.</p>
      </div>
      {error && <p role="alert">{error}</p>}
      <button type="button" className="secondary" disabled={busy} onClick={() => void remove()}>
        {busy ? 'جارٍ الحذف…' : 'احذف المنتجات التجريبية'}
      </button>
    </div>
  );
}
