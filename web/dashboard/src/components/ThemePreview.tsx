import type { CSSProperties, ReactNode } from 'react';

import { fonts, radii, type ThemeSettings } from '../lib/theme';

export interface PreviewProduct {
  title: string;
  price: string;
  image?: string | undefined;
  badge?: string | undefined;
}

export interface PreviewHero {
  heading: string;
  sub?: string | undefined;
  button?: string | undefined;
}

interface Props {
  settings: ThemeSettings;
  storeName: string;
  logo?: string | undefined;
  categories: string[];
  products: PreviewProduct[];
  hero?: PreviewHero | undefined;
  phone: boolean;
  body?: ReactNode; // the page's own sections, in place of the hero and cards
}

// ThemePreview is the design screen's approximate shop (the approved
// mockup): the announcement bar, the header for the chosen look, a hero
// and a few cards in the draft's colours, font and corners. The real
// shop, with every page, is the preview link. The home editor passes
// its sections as the body.
export function ThemePreview({ settings, storeName, logo, categories, products, hero, phone, body }: Props) {
  const c = settings.colors;
  const radius = radii.find((r) => r.id === settings.radius)?.css ?? '8px';
  const look = settings.look ?? 'classic';
  const style = {
    '--p-primary': c.primary,
    '--p-accent': c.accent,
    '--p-bg': c.background,
    '--p-text': c.text,
    '--p-radius': radius,
    fontFamily: fonts.find((f) => f.id === settings.font)?.family,
  } as CSSProperties;
  const brand = logo ? <img src={logo} alt={storeName} className="pv-logo" /> : <span className="pv-brand">{storeName}</span>;
  return (
    <div className={`theme-preview look-${look}${phone ? ' phone' : ''}`} style={style} aria-hidden="true">
      {settings.announcement && <div className="pv-bar">{settings.announcement}</div>}
      <div className="pv-head">
        {brand}
        {look === 'soft' ? (
          <div className="pv-circles">
            {categories.slice(0, 4).map((name) => (
              <span key={name}>
                <i />
                {name}
              </span>
            ))}
          </div>
        ) : (
          <div className="pv-nav">
            <span>الرئيسية</span>
            {categories.slice(0, phone ? 2 : 4).map((name) => (
              <span key={name}>{name}</span>
            ))}
          </div>
        )}
      </div>
      {body ?? (
        <>
          {hero && (
            <div className="pv-hero">
              <strong>{hero.heading}</strong>
              {hero.sub && <span>{hero.sub}</span>}
              {hero.button && <em>{hero.button}</em>}
            </div>
          )}
          <PreviewCards products={products.slice(0, phone ? 2 : look === 'modern' ? 2 : 4)} />
        </>
      )}
    </div>
  );
}

export function PreviewCards({ products }: { products: PreviewProduct[] }) {
  return (
    <div className="pv-grid">
      {products.map((p, i) => (
        <div key={`${p.title}-${String(i)}`} className="pv-card">
          <div className="pv-img">
            {p.image && <img src={p.image} alt="" />}
            {p.badge && <b>{p.badge}</b>}
          </div>
          <span>{p.title}</span>
          <strong>{p.price}</strong>
        </div>
      ))}
    </div>
  );
}
