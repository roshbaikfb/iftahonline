import { useEffect, useRef } from 'react';

import { typeInfo } from '../lib/sections';
import { SectionIcon } from './SectionIcon';

interface Props {
  types: string[]; // the types to offer, in order
  landingNote?: boolean; // say where «منتج واحد» is (the home page)
  onPick: (type: string) => void;
  onClose: () => void;
}

// AddSectionDialog lists the section types to add (the approved mockup):
// each with its icon and a line on what it shows. Escape or × closes it.
export function AddSectionDialog({ types, landingNote = false, onPick, onClose }: Props) {
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
          {types.map((type) => {
            const t = typeInfo(type);
            return (
              <button
                key={type}
                type="button"
                className="type"
                onClick={() => {
                  onPick(type);
                }}
              >
                <span className="ic">
                  <SectionIcon icon={t?.icon ?? 'grid'} size={20} />
                </span>
                <span>
                  <strong>{t?.label ?? type}</strong>
                  {t && <span className="muted">{t.hint}</span>}
                </span>
              </button>
            );
          })}
        </div>
        {landingNote && <p className="muted">«منتج واحد» لصفحات الهبوط فقط، تجده في «الصفحات».</p>}
      </div>
    </div>
  );
}
