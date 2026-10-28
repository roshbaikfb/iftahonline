import { useState } from 'react';

import { enumLabel, fieldLabel, fieldNotes, itemStarter, kindOf, optionalNote, placeholders, type Schema, type Settings } from '../lib/sections';

export interface Choice {
  id: string;
  label: string;
  path: string; // where a link to it goes on the shop
}

export interface Choices {
  products: Choice[];
  categories: Choice[];
  pages: Choice[];
  gridAnchor?: string | undefined; // "#s-…" of the page's product grid: «كل المنتجات»
}

interface Props {
  schema: Schema;
  settings: Settings;
  errors: Record<string, string>; // relative to the settings ("items[0].name")
  choices: Choices;
  onChange: (s: Settings) => void;
  idPrefix: string;
}

// SectionForm edits a section's settings from its type's JSON schema
// (the owner's decision 3, 2026-10-28): every field the schema lists gets
// the control its kind needs, so a section type the server adds later is
// editable too. The hero's button text and link sit side by side, as in
// the approved mockup.
export function SectionForm({ schema, settings, errors, choices, onChange, idPrefix }: Props) {
  const keys = Object.keys(schema.properties ?? {});
  const set = (k: string, v: unknown) => {
    const empty = v === '' || v === undefined || (Array.isArray(v) && v.length === 0 && !schema.required?.includes(k));
    onChange(empty ? without(settings, k) : { ...settings, [k]: v });
  };
  const field = (k: string) => (
    <Field
      key={k}
      name={k}
      id={`${idPrefix}-${k}`}
      schema={schema.properties?.[k] ?? {}}
      value={settings[k]}
      error={errors[k]}
      errors={errors}
      choices={choices}
      settings={settings}
      onChange={(v) => {
        set(k, v);
      }}
    />
  );
  const rows: React.ReactNode[] = [];
  for (const k of keys) {
    if (k === 'button_link' && keys.includes('button_label')) continue;
    if (k === 'button_label' && keys.includes('button_link')) {
      rows.push(
        <div key="button" className="two-cols">
          {field('button_label')}
          {field('button_link')}
        </div>,
      );
      continue;
    }
    // A grid's category and chosen products only matter for their source.
    if (k === 'category_id' && settings['source'] !== 'category') continue;
    if (k === 'product_ids' && settings['source'] !== 'manual') continue;
    rows.push(field(k));
  }
  return <div className="section-form">{rows}</div>;
}

interface FieldProps {
  name: string;
  id: string;
  schema: Schema;
  value: unknown;
  error?: string | undefined;
  errors: Record<string, string>;
  choices: Choices;
  settings: Settings;
  onChange: (v: unknown) => void;
}

function Field({ name, id, schema, value, error, errors, choices, onChange }: FieldProps) {
  const kind = kindOf(name, schema);
  const label = (
    <>
      {fieldLabel(name)}
      {optionalNote(name) && <span className="muted"> (اختياري)</span>}
    </>
  );
  const str = typeof value === 'string' ? value : '';
  const note = fieldNotes[name];
  const tail = (
    <>
      {note && <span className="muted small">{note}</span>}
      {error && <span className="field-error">{error}</span>}
    </>
  );

  switch (kind) {
    case 'items':
      return <Items name={name} id={id} schema={schema} value={value} errors={errors} choices={choices} onChange={onChange} />;
    case 'segmented':
      return (
        <div className="field-label">
          <span id={`${id}-label`}>{label}</span>
          <div className="segmented" role="group" aria-labelledby={`${id}-label`}>
            {(schema.enum ?? []).map((v) => (
              <button
                key={v}
                type="button"
                aria-pressed={(str || (schema.default as string | undefined)) === v}
                onClick={() => {
                  onChange(v);
                }}
              >
                {enumLabel(name, v)}
              </button>
            ))}
          </div>
          {tail}
        </div>
      );
    case 'bool':
      return (
        <label className="check">
          <input
            type="checkbox"
            checked={typeof value === 'boolean' ? value : Boolean(schema.default)}
            onChange={(e) => {
              onChange(e.target.checked);
            }}
          />
          {label}
        </label>
      );
  }

  let control: React.ReactNode;
  switch (kind) {
    case 'enum':
      control = (
        <select
          id={id}
          value={str || ((schema.default as string | undefined) ?? '')}
          onChange={(e) => {
            onChange(e.target.value);
          }}
        >
          {(schema.enum ?? []).map((v) => (
            <option key={v} value={v}>
              {enumLabel(name, v)}
            </option>
          ))}
        </select>
      );
      break;
    case 'number':
      control = (
        <input
          id={id}
          type="number"
          className="narrow-num"
          min={schema.minimum}
          max={schema.maximum}
          value={typeof value === 'number' ? value : ((schema.default as number | undefined) ?? '')}
          onChange={(e) => {
            onChange(e.target.value === '' ? undefined : Number(e.target.value));
          }}
        />
      );
      break;
    case 'datetime':
      control = (
        <input
          id={id}
          type="datetime-local"
          value={str ? toLocal(str) : ''}
          onChange={(e) => {
            onChange(e.target.value ? new Date(e.target.value).toISOString() : undefined);
          }}
        />
      );
      break;
    case 'product':
    case 'category':
      control = (
        <select
          id={id}
          value={str}
          onChange={(e) => {
            onChange(e.target.value);
          }}
        >
          <option value="">{name === 'image_product_id' ? 'بلا صورة' : '— اختر —'}</option>
          {(kind === 'product' ? choices.products : choices.categories).map((c) => (
            <option key={c.id} value={c.id}>
              {c.label}
            </option>
          ))}
        </select>
      );
      break;
    case 'products':
    case 'categories':
      control = <IDList id={id} value={value} options={kind === 'products' ? choices.products : choices.categories} max={schema.maxItems} onChange={onChange} />;
      break;
    case 'lines':
      control = <Lines id={id} value={value} max={schema.maxItems} maxLength={schema.items?.maxLength} onChange={onChange} />;
      break;
    case 'link':
      control = <LinkPicker id={id} value={str} choices={choices} onChange={onChange} />;
      break;
    case 'longtext':
      control = (
        <textarea
          id={id}
          rows={3}
          maxLength={schema.maxLength}
          value={str}
          onChange={(e) => {
            onChange(e.target.value);
          }}
        />
      );
      break;
    default:
      control = (
        <input
          id={id}
          maxLength={schema.maxLength}
          value={str}
          placeholder={placeholders[name]}
          aria-invalid={error ? true : undefined}
          onChange={(e) => {
            onChange(e.target.value);
          }}
        />
      );
  }
  return (
    <div className="field-label">
      <label htmlFor={id}>{label}</label>
      {control}
      {tail}
    </div>
  );
}

// LinkPicker chooses where a button goes: a category, product or page of
// the shop, the page's product grid («كل المنتجات»), or any other link.
function LinkPicker({ id, value, choices, onChange }: { id: string; value: string; choices: Choices; onChange: (v: unknown) => void }) {
  const known = [...choices.categories, ...choices.products, ...choices.pages].some((c) => c.path === value) || value === choices.gridAnchor;
  const [custom, setCustom] = useState(value !== '' && !known);
  return (
    <>
      <select
        id={id}
        value={custom ? 'custom' : value}
        onChange={(e) => {
          const v = e.target.value;
          setCustom(v === 'custom');
          if (v !== 'custom') onChange(v);
        }}
      >
        <option value="">— بلا رابط —</option>
        {choices.gridAnchor && <option value={choices.gridAnchor}>كل المنتجات</option>}
        <optgroup label="التصنيفات">
          {choices.categories.map((c) => (
            <option key={c.id} value={c.path}>
              تصنيف: {c.label}
            </option>
          ))}
        </optgroup>
        <optgroup label="المنتجات">
          {choices.products.map((c) => (
            <option key={c.id} value={c.path}>
              منتج: {c.label}
            </option>
          ))}
        </optgroup>
        {choices.pages.length > 0 && (
          <optgroup label="الصفحات">
            {choices.pages.map((c) => (
              <option key={c.id} value={c.path}>
                صفحة: {c.label}
              </option>
            ))}
          </optgroup>
        )}
        <option value="custom">رابط آخر…</option>
      </select>
      {custom && (
        <input
          dir="ltr"
          aria-label="الرابط"
          placeholder="https://… أو /p/…"
          value={value}
          maxLength={500}
          onChange={(e) => {
            onChange(e.target.value.trim());
          }}
        />
      )}
    </>
  );
}

// Lines edits a short list of text lines (a product's highlights).
function Lines({ id, value, max, maxLength, onChange }: { id: string; value: unknown; max?: number | undefined; maxLength?: number | undefined; onChange: (v: unknown) => void }) {
  const lines = Array.isArray(value) ? (value as string[]) : [];
  const shown = max === undefined || lines.length < max ? [...lines, ''] : lines;
  return (
    <div className="lines">
      {shown.map((l, i) => (
        <input
          key={i}
          id={i === 0 ? id : undefined}
          aria-label={i === 0 ? undefined : `السطر ${String(i + 1)}`}
          maxLength={maxLength}
          value={l}
          placeholder={i === lines.length ? '+ أضف سطرًا' : undefined}
          onChange={(e) => {
            const next = [...lines];
            next[i] = e.target.value;
            onChange(next.filter((x) => x.trim() !== ''));
          }}
        />
      ))}
    </div>
  );
}

// IDList picks several products or categories, in order.
function IDList({ id, value, options, max, onChange }: { id: string; value: unknown; options: Choice[]; max?: number | undefined; onChange: (v: unknown) => void }) {
  const ids = Array.isArray(value) ? (value as string[]) : [];
  const name = new Map(options.map((o) => [o.id, o.label]));
  return (
    <>
      {ids.length > 0 && (
        <div className="chips">
          {ids.map((x) => (
            <span key={x} className="chip">
              {name.get(x) ?? '…'}
              <button
                type="button"
                className="x"
                aria-label={`أزل ${name.get(x) ?? ''}`}
                onClick={() => {
                  onChange(ids.filter((y) => y !== x));
                }}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      {(max === undefined || ids.length < max) && (
        <select
          id={id}
          value=""
          onChange={(e) => {
            if (e.target.value) onChange([...ids, e.target.value]);
          }}
        >
          <option value="">+ أضف</option>
          {options
            .filter((o) => !ids.includes(o.id))
            .map((o) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
        </select>
      )}
    </>
  );
}

// Items edits a list of small objects (questions, reviews, badges, tiles).
function Items({
  name,
  id,
  schema,
  value,
  errors,
  choices,
  onChange,
}: {
  name: string;
  id: string;
  schema: Schema;
  value: unknown;
  errors: Record<string, string>;
  choices: Choices;
  onChange: (v: unknown) => void;
}) {
  const items = Array.isArray(value) ? (value as Settings[]) : [];
  const item = schema.items ?? {};
  const set = (i: number, v: Settings) => {
    onChange(items.map((x, j) => (j === i ? v : x)));
  };
  return (
    <fieldset className="items">
      <legend>{fieldLabel(name)}</legend>
      {items.map((it, i) => (
        <div key={i} className="item">
          <div className="item-head">
            <strong>{String(i + 1)}</strong>
            {items.length > (schema.minItems ?? 0) && (
              <button
                type="button"
                className="x"
                aria-label={`احذف العنصر ${String(i + 1)}`}
                onClick={() => {
                  onChange(items.filter((_, j) => j !== i));
                }}
              >
                ×
              </button>
            )}
          </div>
          {Object.entries(item.properties ?? {}).map(([k, p]) => (
            <Field
              key={k}
              name={k}
              id={`${id}-${String(i)}-${k}`}
              schema={p}
              value={it[k]}
              error={errors[`${name}[${String(i)}].${k}`]}
              errors={{}}
              choices={choices}
              settings={it}
              onChange={(v) => {
                set(i, v === '' || v === undefined ? without(it, k) : { ...it, [k]: v });
              }}
            />
          ))}
        </div>
      ))}
      {(schema.maxItems === undefined || items.length < schema.maxItems) && (
        <button
          type="button"
          className="secondary small"
          onClick={() => {
            onChange([...items, itemStarter(item)]);
          }}
        >
          + أضف
        </button>
      )}
    </fieldset>
  );
}

// without is the settings with one left out (empty: the shop's default).
function without(s: Settings, key: string): Settings {
  return Object.fromEntries(Object.entries(s).filter(([k]) => k !== key));
}

// toLocal writes an ISO time as a datetime-local value (the merchant's
// clock).
function toLocal(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${String(d.getFullYear())}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
