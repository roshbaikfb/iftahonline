import { useEffect, useState } from 'react';

import { api } from '../api/client';
import type { PreviewCategory } from '../components/HomePreview';
import type { Choices } from '../components/SectionForm';
import type { PreviewProduct } from '../components/ThemePreview';
import { money } from './orders';
import type { Schema, Section } from './sections';
import type { ThemeSettings } from './theme';

export interface ShopData {
  schemas: Record<string, Schema>;
  theme: ThemeSettings | null; // the published theme
  store: { name: string; slug: string } | null;
  logo?: string | undefined;
  categories: (PreviewCategory & { slug: string; top: boolean })[];
  products: (PreviewProduct & { id: string; slug: string })[];
  pages: { id: string; title: string; slug: string }[];
}

const empty: ShopData = { schemas: {}, theme: null, store: null, categories: [], products: [], pages: [] };

// useShopData loads what the page editors need besides the page: the
// section schemas, the shop's look for the preview, and the categories,
// products and landing pages that sections and buttons point to.
export function useShopData(storeId: string): ShopData {
  const [data, setData] = useState<ShopData>(empty);
  useEffect(() => {
    let live = true;
    const set = (part: Partial<ShopData>) => {
      if (live) setData((d) => ({ ...d, ...part }));
    };
    const path = { params: { path: { store_id: storeId } } };
    void api.GET('/v1/stores/{store_id}/storefront/section-types', path).then(({ data: d }) => {
      if (d) set({ schemas: Object.fromEntries(d.items.map((t) => [t.type, t.schema as Schema])) });
    });
    void api.GET('/v1/stores/{store_id}/storefront/theme', path).then(({ data: d }) => {
      if (d) set({ theme: d.settings });
    });
    void api.GET('/v1/stores/{store_id}', path).then(({ data: d }) => {
      if (d) set({ store: { name: d.name, slug: d.slug } });
    });
    void api.GET('/v1/stores/{store_id}/storefront/logo', path).then(({ data: d }) => {
      set({ logo: d?.current?.urls?.small.webp });
    });
    void api.GET('/v1/stores/{store_id}/categories', path).then(({ data: d }) => {
      if (d) set({ categories: d.items.map((c) => ({ id: c.id, name: c.name, slug: c.slug, top: !c.parent_id })) });
    });
    void api.GET('/v1/stores/{store_id}/storefront/pages', path).then(({ data: d }) => {
      if (d) set({ pages: d.items.filter((p) => p.id !== undefined).map((p) => ({ id: p.id ?? '', title: p.title, slug: p.slug })) });
    });
    void api.GET('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId }, query: { status: 'active', limit: 200 } } }).then(({ data: d }) => {
      if (!d) return;
      set({
        products: d.items.map((p) => {
          const v = p.variants[0];
          return {
            id: p.id,
            slug: p.slug,
            title: p.title,
            price: v ? money(v.price_minor, v.currency) : '',
            image: p.images.find((i) => i.status === 'ready')?.urls?.thumb.webp,
          };
        }),
      });
    });
    return () => {
      live = false;
    };
  }, [storeId]);
  return data;
}

// choicesFor is where a section's buttons and pickers can point: the
// shop's products, categories and landing pages, and the page's first
// product grid as «كل المنتجات» (the shop has no page of every product).
export function choicesFor(d: ShopData, sections: Section[], self?: string): Choices {
  const grid = sections.find((s) => s.type === 'product_grid');
  return {
    products: d.products.map((p) => ({ id: p.id, label: p.title, path: `/p/${p.slug}` })),
    categories: d.categories.map((c) => ({ id: c.id, label: c.name, path: `/c/${c.slug}` })),
    pages: d.pages.filter((p) => p.id !== self).map((p) => ({ id: p.id, label: p.title, path: `/pages/${p.slug}` })),
    gridAnchor: grid?.id ? `#s-${grid.id}` : undefined,
  };
}
