import { useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';

// AdsNotice tells the owner of a free store that its pages show ads, and
// how to remove them (ADR-0062, the approved mockup). It reads the usage
// meters (billing.manage): a member who cannot manage billing sees none.
export function AdsNotice({ storeId }: { storeId: string }) {
  const [ads, setAds] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/usage', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setAds(!data.ad_free);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  if (!ads) return null;
  return (
    <div className="card ads-notice" role="status">
      <span className="ads-notice-icon" aria-hidden="true">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="5" width="18" height="12" rx="2" />
          <path d="M7 21h10" />
        </svg>
      </span>
      <div>
        <strong>متجرك على الباقة المجانية، فتظهر فيه إعلانات</strong>
        <p className="muted">في الرئيسية وصفحات المنتجات والتصنيفات فقط، وأبدًا في السلة والدفع. اشترك في أي باقة، أو «حسب مبيعاتك»، وتختفي الإعلانات فورًا.</p>
      </div>
      <Link className="button primary" to={`/stores/${storeId}/billing/plans`}>
        أزل الإعلانات
      </Link>
    </div>
  );
}
