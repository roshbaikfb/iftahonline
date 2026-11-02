import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { postFile } from '../lib/upload';
import { field } from '../session';

export type SiteSettings = components['schemas']['AdminSiteSettings'];
export type SiteFAQ = components['schemas']['AdminSiteFAQ'];
export type SiteFAQInput = components['schemas']['AdminSiteFAQInput'];
export type Socials = components['schemas']['AdminSiteSocials'];
export type Social = keyof Socials;
export type SiteStory = components['schemas']['AdminSiteStory'];
export type SiteImages = components['schemas']['AdminSiteImages'];
export type SitePerson = components['schemas']['AdminSitePerson'];
export type SitePersonInput = components['schemas']['AdminSitePersonInput'];
export type SiteReview = components['schemas']['AdminSiteTestimonial'];
export type SiteReviewInput = components['schemas']['AdminSiteTestimonialInput'];

type ImageType = 'image/jpeg' | 'image/png' | 'image/webp';
const imageTypes: string[] = ['image/jpeg', 'image/png', 'image/webp'];

// uploadImage posts a picked image to the bucket and returns its key for
// upload_key; undefined when none was picked. It throws a message to show.
export async function uploadImage(input: HTMLInputElement | null): Promise<string | undefined> {
  const file = input?.files?.[0];
  if (!file) return undefined;
  if (!imageTypes.includes(file.type)) throw new Error('اختر صورة WebP أو PNG أو JPEG.');
  const up = await api.POST('/admin/v1/site/uploads', { body: { content_type: file.type as ImageType } });
  if (up.error) throw new Error(problemMessage(up.error));
  await postFile(up.data.upload, file, 'الصورة أكبر من 4 ميجابايت.');
  return up.data.key;
}

// withSaved keeps a list in the site's order after one item is saved.
export function withSaved<T extends { id: string; position: number }>(list: T[], saved: T): T[] {
  return [...list.filter((x) => x.id !== saved.id), saved].sort((a, b) => a.position - b.position);
}

// personFromForm reads the person form.
export function personFromForm(form: FormData): SitePersonInput {
  const text = (name: string) => field(form, name).trim();
  return {
    kind: text('kind') === 'team' ? 'team' : 'founder',
    position: Number(text('position')),
    name_ar: text('name_ar'),
    name_en: text('name_en'),
    role_ar: text('role_ar'),
    role_en: text('role_en'),
    bio_ar: text('bio_ar'),
    bio_en: text('bio_en'),
    linkedin_url: text('linkedin_url'),
    x_url: text('x_url'),
    remove_photo: form.get('remove_photo') === 'on',
    reason: text('reason'),
  };
}

// reviewFromForm reads the review form.
export function reviewFromForm(form: FormData): SiteReviewInput {
  const text = (name: string) => field(form, name).trim();
  return {
    position: Number(text('position')),
    name: text('name'),
    store_name: text('store_name'),
    store_url: text('store_url'),
    quote_ar: text('quote_ar'),
    quote_en: text('quote_en'),
    remove_photo: form.get('remove_photo') === 'on',
    reason: text('reason'),
  };
}

// socials are the networks in the site's order (ADR-0073), with an
// example of a profile link.
export const socials: { key: Social; name: string; example: string }[] = [
  { key: 'facebook', name: 'Facebook', example: 'https://facebook.com/iftahonline' },
  { key: 'instagram', name: 'Instagram', example: 'https://instagram.com/iftahonline' },
  { key: 'tiktok', name: 'TikTok', example: 'https://tiktok.com/@iftahonline' },
  { key: 'linkedin', name: 'LinkedIn', example: 'https://linkedin.com/company/iftahonline' },
  { key: 'youtube', name: 'YouTube', example: 'https://youtube.com/@iftahonline' },
  { key: 'x', name: 'X', example: 'https://x.com/iftahonline' },
];

// nextPosition puts a new item last, leaving room to slot one in
// between later.
export function nextPosition(list: { position: number }[]): number {
  return list.reduce((max, f) => Math.max(max, f.position), -10) + 10;
}

// sortFAQs is the site's order: position, then as listed.
export function sortFAQs(faqs: SiteFAQ[]): SiteFAQ[] {
  return [...faqs].sort((a, b) => a.position - b.position);
}

// faqFromForm reads the question form.
export function faqFromForm(form: FormData): SiteFAQInput {
  const text = (name: string) => field(form, name).trim();
  const en = { question_en: text('question_en'), answer_en: text('answer_en') };
  return {
    position: Number(text('position')),
    on_home: form.get('on_home') === 'on',
    question_ar: text('question_ar'),
    answer_ar: text('answer_ar'),
    ...(en.question_en || en.answer_en ? en : {}),
    reason: text('reason'),
  };
}
