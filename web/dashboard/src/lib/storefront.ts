// storefrontURL is a store's storefront address: VITE_STOREFRONT_URL
// with {slug} replaced (set at build time per environment), else the
// platform's domain, or the local storefront in development.
export function storefrontURL(slug: string, template = defaultTemplate()): string {
  return template.replace('{slug}', encodeURIComponent(slug));
}

function defaultTemplate(): string {
  const set = import.meta.env['VITE_STOREFRONT_URL'] as string | undefined;
  if (set) return set;
  return import.meta.env.DEV ? 'http://{slug}.iftahonline.localhost:18081' : 'https://{slug}.iftahonline.com';
}
