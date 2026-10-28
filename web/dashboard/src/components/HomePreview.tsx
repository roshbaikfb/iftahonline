import type { ReactNode } from 'react';

import { defaults, gridTitle, type Section, type Settings } from '../lib/sections';
import type { ThemeSettings } from '../lib/theme';
import { PreviewCards, ThemePreview, type PreviewProduct } from './ThemePreview';

export interface PreviewCategory {
  id: string;
  name: string;
  image?: string | undefined;
}

interface Props {
  theme: ThemeSettings;
  storeName: string;
  logo?: string | undefined;
  sections: Section[];
  selected: string; // the section outlined
  categories: PreviewCategory[]; // top-level, in the shop's order
  products: (PreviewProduct & { id: string })[];
  phone: boolean;
  now?: Date;
}

// HomePreview is the home editor's approximate page (the approved
// mockup): the draft's sections, top to bottom, in the shop's look, with
// the chosen one outlined. Texts left empty show the shop's defaults.
export function HomePreview({ theme, storeName, logo, sections, selected, categories, products, phone, now = new Date() }: Props) {
  const cards = phone ? 2 : 4;
  const body = sections.map((s, i) => {
    const v = (s.settings ?? {});
    const str = (k: string) => (typeof v[k] === 'string' ? v[k] : '');
    const list = (k: string) => (Array.isArray(v[k]) ? (v[k] as Settings[]) : []);
    let inner: ReactNode;
    switch (s.type) {
      case 'hero': {
        const badge = str('badge');
        const button = str('button_label') || (str('button_link') ? defaults.button : '');
        const image = products.find((p) => p.id === str('image_product_id'))?.image;
        inner = (
          <div className={`pv-hero${str('align') === 'center' || !v['align'] ? ' center' : ''}${image ? ' with-image' : ''}`}>
            {image && <img src={image} alt="" />}
            {badge && <b>{badge}</b>}
            <strong>{str('heading') || storeName}</strong>
            {str('subheading') && <span>{str('subheading')}</span>}
            {button && <em>{button}</em>}
          </div>
        );
        break;
      }
      case 'category_tiles': {
        const ids = Array.isArray(v['category_ids']) ? (v['category_ids'] as string[]) : [];
        const limit = typeof v['limit'] === 'number' ? v['limit'] : 8;
        const shown = (ids.length ? ids.map((id) => categories.find((c) => c.id === id)).filter((c) => c !== undefined) : categories).slice(0, Math.min(limit, phone ? 4 : 6));
        inner = (
          <>
            {str('title') && <h3>{str('title')}</h3>}
            <div className="pv-tiles">
              {shown.map((c) => (
                <span key={c.id}>
                  <i>{c.image && <img src={c.image} alt="" />}</i>
                  {c.name}
                </span>
              ))}
            </div>
          </>
        );
        break;
      }
      case 'product_grid': {
        const ids = Array.isArray(v['product_ids']) ? (v['product_ids'] as string[]) : [];
        const limit = typeof v['limit'] === 'number' ? v['limit'] : 8;
        const pick = str('source') === 'manual' ? ids.map((id) => products.find((p) => p.id === id)).filter((p) => p !== undefined) : products;
        inner = (
          <>
            <h3>{str('title') || gridTitle(str('source'))}</h3>
            <PreviewCards products={pick.slice(0, Math.min(limit, cards))} />
          </>
        );
        break;
      }
      case 'reviews':
        inner = (
          <>
            <h3>{str('title') || defaults.reviews}</h3>
            <div className="pv-quotes">
              {list('items')
                .slice(0, phone ? 1 : 2)
                .map((r, j) => (
                  <blockquote key={j}>
                    <span className="stars">{'★'.repeat(typeof r['rating'] === 'number' ? r['rating'] : 5)}</span>
                    <span>{typeof r['text'] === 'string' ? r['text'] : ''}</span>
                    <cite>{typeof r['name'] === 'string' ? r['name'] : ''}</cite>
                  </blockquote>
                ))}
            </div>
          </>
        );
        break;
      case 'faq':
        inner = (
          <>
            <h3>{str('title') || defaults.faq}</h3>
            <ul className="pv-faq">
              {list('items')
                .slice(0, 3)
                .map((q, j) => (
                  <li key={j}>{typeof q['question'] === 'string' ? q['question'] : ''}</li>
                ))}
            </ul>
          </>
        );
        break;
      case 'trust_badges':
        inner = (
          <div className="pv-badges">
            {list('items').map((b, j) => {
              const icon = typeof b['icon'] === 'string' ? b['icon'] : '';
              return <span key={j}>{(typeof b['title'] === 'string' && b['title']) || (defaults.badges[icon] ?? icon)}</span>;
            })}
          </div>
        );
        break;
      case 'countdown': {
        const left = Math.max(0, new Date(str('ends_at')).getTime() - now.getTime()) || 0;
        const parts = [Math.floor(left / 86_400_000), Math.floor(left / 3_600_000) % 24, Math.floor(left / 60_000) % 60, Math.floor(left / 1000) % 60];
        inner = (
          <div className="pv-countdown">
            <strong>{str('title') || defaults.countdown}</strong>
            {str('subtitle') && <span>{str('subtitle')}</span>}
            <span className="boxes">
              {parts.map((n, j) => (
                <i key={j}>{String(n).padStart(2, '0')}</i>
              ))}
            </span>
          </div>
        );
        break;
      }
      case 'promo_tiles':
        inner = (
          <div className="pv-promos">
            {list('items').map((t, j) => (
              <span key={j} className={`tone-${typeof t['tone'] === 'string' ? t['tone'] : 'amber'}`}>
                {typeof t['label'] === 'string' && t['label'] && <small>{t['label']}</small>}
                <strong>{typeof t['title'] === 'string' ? t['title'] : ''}</strong>
              </span>
            ))}
          </div>
        );
        break;
      case 'whatsapp_cta':
        inner = (
          <div className="pv-whatsapp">
            <strong>{str('title') || defaults.whatsapp}</strong>
            <span>{str('text') || defaults.whatsappText}</span>
          </div>
        );
        break;
      default:
        inner = <p className="pv-unknown">{s.type}</p>;
    }
    return (
      <div key={s.id ?? i} className={`pv-section${s.id === selected ? ' pv-on' : ''}`}>
        {inner}
      </div>
    );
  });
  return <ThemePreview settings={theme} storeName={storeName} logo={logo} categories={categories.map((c) => c.name)} products={[]} phone={phone} body={body} />;
}
