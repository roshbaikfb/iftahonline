import type { components } from '../api/client';
import { field } from '../session';

export type GuideSummary = components['schemas']['AdminSiteGuideSummary'];
export type Guide = components['schemas']['AdminSiteGuide'];
export type GuideInput = components['schemas']['AdminSiteGuideInput'];

export const guideStatusLabel: Record<Guide['status'], string> = { draft: 'مسودة', published: 'منشور' };

// guideFromForm reads the editor; English goes only when written.
export function guideFromForm(form: FormData): GuideInput {
  const text = (name: string) => field(form, name).trim();
  const en = { title_en: text('title_en'), summary_en: text('summary_en'), body_en: text('body_en') };
  return {
    slug: text('slug').toLowerCase(),
    status: text('status') === 'published' ? 'published' : 'draft',
    title_ar: text('title_ar'),
    summary_ar: text('summary_ar'),
    body_ar: text('body_ar'),
    ...(en.title_en || en.summary_en || en.body_en ? en : {}),
    remove_cover: form.get('remove_cover') === 'on',
    reason: text('reason'),
  };
}

// slugOK says whether an address is lowercase English words joined by
// dashes, as the site's addresses are.
export const slugOK = (slug: string) => /^[a-z0-9]+(-[a-z0-9]+)*$/.test(slug) && slug.length <= 80;
