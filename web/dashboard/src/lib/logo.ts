import { api } from '../api/client';

import { problemMessage } from './problem';
import { postFile } from './upload';

const logoTypes = ['image/png', 'image/jpeg', 'image/webp'] as const;

// uploadLogo sends a new store logo: an upload slot, the file straight to
// storage, then "complete" (the server re-encodes it and makes it live).
// It throws a message for the merchant.
export async function uploadLogo(storeId: string, file: File): Promise<void> {
  const type = logoTypes.find((t) => t === file.type);
  if (!type) throw new Error('الشعار يكون PNG أو JPG أو WebP.');
  const { data, error } = await api.POST('/v1/stores/{store_id}/storefront/logo/uploads', {
    params: { path: { store_id: storeId } },
    body: { content_type: type },
  });
  if (error) throw new Error(problemMessage(error));
  await postFile(data.upload, file, 'تعذّر رفع الملف، جرّب صورة أصغر (حتى 2 ميجابايت).');
  const done = await api.POST('/v1/stores/{store_id}/storefront/logo/uploads/{asset_id}/complete', {
    params: { path: { store_id: storeId, asset_id: data.asset.id } },
  });
  if (done.error) throw new Error(problemMessage(done.error));
}
