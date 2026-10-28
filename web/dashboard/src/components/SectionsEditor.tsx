import { useState } from 'react';

import { choicesFor, type ShopData } from '../lib/shopData';
import { newID, phoneSummary, sectionErrors, starter, summary, typeIcon, typeLabel, type Section, type Settings } from '../lib/sections';
import type { ThemeSettings } from '../lib/theme';
import { AddSectionDialog } from './AddSectionDialog';
import { HomePreview } from './HomePreview';
import { SectionForm } from './SectionForm';
import { SectionIcon } from './SectionIcon';

interface Props {
  sections: Section[];
  onChange: (next: Section[]) => void;
  selected: string;
  onSelect: (id: string) => void;
  errors: Record<string, string>; // the server's, by "sections[i].settings…"
  data: ShopData;
  theme: ThemeSettings | null; // the look the preview shows
  narrow: boolean;
  types: string[]; // the types offered in «أضف قسمًا», in its order
  self?: string | undefined; // the landing page edited, left out of its own links
}

export const maxSections = 30;

// SectionsEditor edits a page's sections (the approved home page mockup):
// the list with move and delete, the chosen section's fields and the
// draft as an approximate page; on phones each section opens in place.
// The home page and the landing pages share it.
export function SectionsEditor({ sections, onChange, selected, onSelect, errors, data, theme, narrow, types, self }: Props) {
  const [adding, setAdding] = useState(false);
  const [phone, setPhone] = useState(true);
  const index = sections.findIndex((s) => s.id === selected);
  const current = sections[index];
  const choices = choicesFor(data, sections, self);

  const setSettings = (id: string, settings: Settings) => {
    onChange(sections.map((s) => (s.id === id ? { ...s, settings } : s)));
  };
  const move = (i: number, by: number) => {
    const next = [...sections];
    const [s] = next.splice(i, 1);
    if (!s) return;
    next.splice(i + by, 0, s);
    onChange(next);
  };
  const remove = (i: number) => {
    const s = sections[i];
    if (!s || !globalThis.confirm(`حذف قسم «${typeLabel(s.type)}» من الصفحة؟`)) return;
    const next = sections.filter((_, j) => j !== i);
    if (s.id === selected) onSelect(next[Math.min(i, next.length - 1)]?.id ?? '');
    onChange(next);
  };
  const add = (type: string) => {
    const s: Section = { id: newID(), type: type as Section['type'], settings: starter(type, data.schemas[type]) };
    setAdding(false);
    onSelect(s.id ?? '');
    onChange([...sections, s]);
  };
  const form = (s: Section, i: number) => {
    const schema = data.schemas[s.type];
    const errs = sectionErrors(errors, i);
    return schema ? (
      <>
        {errs[''] && <p className="field-error">{errs['']}</p>}
        <SectionForm
          key={s.id}
          schema={schema}
          settings={(s.settings ?? {})}
          errors={errs}
          choices={choices}
          idPrefix={`f-${s.id ?? String(i)}`}
          onChange={(v) => {
            setSettings(s.id ?? '', v);
          }}
        />
      </>
    ) : (
      <p className="muted">جارٍ التحميل…</p>
    );
  };
  const hasErrors = (i: number) => Object.keys(errors).some((k) => k === `sections[${String(i)}]` || k.startsWith(`sections[${String(i)}].`));
  const moveButton = (s: Section, i: number) => {
    const down = i === 0;
    if (sections.length < 2) return null;
    return (
      <button
        type="button"
        className="icon-only"
        aria-label={`انقل ${typeLabel(s.type)} ${down ? 'لأسفل' : 'لأعلى'}`}
        onClick={(e) => {
          e.preventDefault();
          move(i, down ? 1 : -1);
        }}
      >
        <SectionIcon icon={down ? 'down' : 'up'} />
      </button>
    );
  };
  const deleteButton = (s: Section, i: number) => (
    <button
      type="button"
      className="icon-only"
      aria-label={`احذف ${typeLabel(s.type)}`}
      onClick={(e) => {
        e.preventDefault();
        remove(i);
      }}
    >
      <SectionIcon icon="trash" />
    </button>
  );
  const addButton = (
    <button
      type="button"
      className="secondary add-section-button"
      disabled={sections.length >= maxSections}
      onClick={() => {
        setAdding(true);
      }}
    >
      + أضف قسمًا
    </button>
  );
  const dialog = adding && (
    <AddSectionDialog
      types={[...types.filter((t) => t in data.schemas), ...Object.keys(data.schemas).filter((t) => !types.includes(t) && t !== 'product_landing')]}
      landingNote={!types.includes('product_landing')}
      onPick={add}
      onClose={() => {
        setAdding(false);
      }}
    />
  );

  if (narrow) {
    return (
      <div className="phone-sections">
        {sections.map((s, i) => {
          const open = s.id === selected;
          return (
            <details key={s.id} className={`sec-details${hasErrors(i) ? ' has-error' : ''}`} open={open}>
              <summary
                onClick={(e) => {
                  e.preventDefault();
                  onSelect(open ? '' : (s.id ?? ''));
                }}
              >
                <strong>{typeLabel(s.type)}</strong>
                {open ? (
                  <>
                    {moveButton(s, i)}
                    {deleteButton(s, i)}
                  </>
                ) : (
                  <span className="muted">{phoneSummary(s)}</span>
                )}
              </summary>
              {open && <div className="sec-body">{form(s, i)}</div>}
            </details>
          );
        })}
        {addButton}
        {dialog}
      </div>
    );
  }

  return (
    <div className="home-editor">
      <section className="card editor-card sections-card" aria-labelledby="h-secs">
        <div className="card-head bare">
          <h2 id="h-secs">الأقسام</h2>
          <span className="muted">من أعلى الصفحة لأسفلها</span>
        </div>
        <ol className="sec-list">
          {sections.map((s, i) => (
            <li key={s.id} className={`sec${s.id === selected ? ' on' : ''}${hasErrors(i) ? ' has-error' : ''}`}>
              <span className="sec-icon">
                <SectionIcon icon={typeIcon(s.type)} />
              </span>
              <button
                type="button"
                className="name"
                aria-current={s.id === selected ? 'true' : undefined}
                onClick={() => {
                  onSelect(s.id ?? '');
                }}
              >
                <strong>{typeLabel(s.type)}</strong>
                <span className="muted">{summary(s)}</span>
              </button>
              {moveButton(s, i)}
              {deleteButton(s, i)}
            </li>
          ))}
        </ol>
        {sections.length === 0 && <p className="muted">الصفحة فارغة. أضف قسمًا لتبدأ.</p>}
        {addButton}
      </section>

      <section className="card editor-card section-card" aria-labelledby="h-edit">
        {current ? (
          <>
            <div className="card-head bare">
              <h2 id="h-edit">{typeLabel(current.type)}</h2>
              <span className="muted">
                القسم {index + 1} من {sections.length}
              </span>
            </div>
            {form(current, index)}
          </>
        ) : (
          <p className="muted" id="h-edit">
            اختر قسمًا من القائمة لتعديله.
          </p>
        )}
      </section>

      <section className="design-preview" aria-label="معاينة">
        <div className="card-head bare">
          <strong>معاينة المسودة</strong>
          <div className="segmented" role="group" aria-label="حجم المعاينة">
            <button
              type="button"
              aria-pressed={!phone}
              onClick={() => {
                setPhone(false);
              }}
            >
              كمبيوتر
            </button>
            <button
              type="button"
              aria-pressed={phone}
              onClick={() => {
                setPhone(true);
              }}
            >
              هاتف
            </button>
          </div>
        </div>
        {theme && (
          <HomePreview
            theme={theme}
            storeName={data.store?.name ?? ''}
            logo={data.logo}
            sections={sections}
            selected={selected}
            categories={data.categories.filter((c) => c.top)}
            products={data.products}
            phone={phone}
          />
        )}
        <p className="muted small center">القسم المختار محاط بإطار.</p>
      </section>
      {dialog}
    </div>
  );
}
