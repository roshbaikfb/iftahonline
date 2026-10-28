import { api } from '../api/client';
import { problemMessage } from './problem';
import { storefrontURL } from './storefront';

// openPreview opens the shop with the drafts in a new tab (ADR-0036):
// the tab opens at the click, so the browser does not block it as a
// popup, then save runs (the pending draft) and a preview link is made.
// It returns what went wrong, or "".
export async function openPreview(storeId: string, slug: string, save: () => Promise<boolean>): Promise<string> {
  const tab = globalThis.open('about:blank', '_blank');
  if (!(await save())) {
    tab?.close();
    return '';
  }
  const { data, error } = await api.POST('/v1/stores/{store_id}/storefront/previews', { params: { path: { store_id: storeId } } });
  if (error) {
    tab?.close();
    return problemMessage(error);
  }
  if (tab) {
    tab.opener = null;
    tab.location.href = `${storefrontURL(slug)}/?preview=${encodeURIComponent(data.token)}`;
  }
  return '';
}
