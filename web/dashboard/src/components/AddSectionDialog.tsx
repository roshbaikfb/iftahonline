import { useEffect, useRef } from 'react';

import { sectionTypes } from '../lib/sections';
import { SectionIcon } from './SectionIcon';

interface Props {
  types: string[]; // the types the server offers for the home page
  onPick: (type: string) => void;
  onClose: () => void;
}

// AddSectionDialog lists the section types to add (the approved mockup):
// each with its icon and a line on what it shows. Escape or × closes it.
export function AddSectionDialog({ types, onPick, onClose }: Props) {
  const box = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  useEffect(() => {
    close.current = onClose;
  });
  useEffect(() => {
    const back = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLButtonElement>('button.type')?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close.current();
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      back?.focus();
    };
  }, []);
  const known = sectionTypes.filter((t) => types.includes(t.type));
  const others = types.filter((t) => t !== 'product_landing' && !sectionTypes.some((k) => k.type === t));
  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div ref={box} className="modal add-section" role="dialog" aria-modal="true" aria-labelledby="h-add">
        <div className="card-head bare">
          <h2 id="h-add">أضف قسمًا</h2>
          <button type="button" className="secondary icon-button" aria-label="إغلاق" onClick={onClose}>
            ×
          </button>
        </div>
        <p className="muted">يُضاف في أسفل الصفحة، ثم تنقله حيث تريد.</p>
        <div className="types">
          {known.map((t) => (
            <button
              key={t.type}
              type="button"
              className="type"
              onClick={() => {
                onPick(t.type);
              }}
            >
              <span className="ic">
                <SectionIcon icon={t.icon} size={20} />
              </span>
              <span>
                <strong>{t.label}</strong>
                <span className="muted">{t.hint}</span>
              </span>
            </button>
          ))}
          {others.map((t) => (
            <button
              key={t}
              type="button"
              className="type"
              onClick={() => {
                onPick(t);
              }}
            >
              <span className="ic">
                <SectionIcon icon="grid" size={20} />
              </span>
              <span>
                <strong>{t}</strong>
              </span>
            </button>
          ))}
        </div>
        {types.includes('product_landing') && <p className="muted">«منتج واحد» لصفحات الهبوط فقط، تجده في «الصفحات».</p>}
      </div>
    </div>
  );
}
