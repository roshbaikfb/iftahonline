import type { components } from '../api/client';
import { field } from '../session';

export type SiteSettings = components['schemas']['AdminSiteSettings'];
export type SiteFAQ = components['schemas']['AdminSiteFAQ'];
export type SiteFAQInput = components['schemas']['AdminSiteFAQInput'];
export type Socials = components['schemas']['AdminSiteSocials'];
export type Social = keyof Socials;

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

// nextPosition puts a new question last, leaving room to slot one in
// between later.
export function nextPosition(faqs: SiteFAQ[]): number {
  return faqs.reduce((max, f) => Math.max(max, f.position), -10) + 10;
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
