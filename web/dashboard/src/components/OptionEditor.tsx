import { useState, type KeyboardEvent } from 'react';

import { guessColour, isColourName, maxValues, normalizeHex, palette, swatchOf, type OptionDraft } from '../lib/editor';

interface Props {
  option: OptionDraft;
  index: number;
  error?: string | undefined;
  onChange: (o: OptionDraft) => void;
  onRemove: () => void;
}

// OptionEditor is one option of the product editor (the approved mockup):
// its name, whether it is a colour, and its values as chips. A colour
// value's swatch opens a picker; until one is picked the shop guesses
// the colour from the value's name (ADR-0035).
export function OptionEditor({ option, index, error, onChange, onRemove }: Props) {
  const [typed, setTyped] = useState('');
  const [picking, setPicking] = useState(-1);
  const [hex, setHex] = useState('');
  const nameId = `option-${String(index)}`;

  function add(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== 'Enter' && e.key !== ',' && e.key !== '،') return;
    e.preventDefault();
    const v = typed.trim();
    if (!v || v.length > 50 || option.values.length >= maxValues) return;
    if (option.values.some((x) => x.toLowerCase() === v.toLowerCase())) {
      setTyped('');
      return;
    }
    onChange({ ...option, values: [...option.values, v], colors: option.colors ? [...option.colors, ''] : null });
    setTyped('');
  }

  function remove(i: number) {
    onChange({ ...option, values: option.values.filter((_, j) => j !== i), colors: option.colors ? option.colors.filter((_, j) => j !== i) : null });
    setPicking(-1);
  }

  function setColour(i: number, c: string) {
    if (!option.colors) return;
    const colors = [...option.colors];
    colors[i] = c;
    onChange({ ...option, colors });
  }

  function open(i: number) {
    setPicking(i === picking ? -1 : i);
    setHex(swatchOf(option, i));
  }

  const shown = (i: number) => swatchOf(option, i);
  const value = option.values[picking];

  return (
    <fieldset className="option" aria-labelledby={`${nameId}-legend`}>
      <legend id={`${nameId}-legend`} className="visually-hidden">
        الخيار {index + 1}
      </legend>
      <div className="option-head">
        <label className="field-label">
          اسم الخيار
          <input
            id={nameId}
            value={option.name}
            maxLength={50}
            placeholder="مثل: المقاس"
            onChange={(e) => {
              const name = e.target.value;
              // A name that reads as a colour makes it a colour option.
              const colors = option.colors ?? (isColourName(name) ? option.values.map(() => '') : null);
              onChange({ ...option, name, colors });
            }}
          />
        </label>
        <label className="check">
          <input
            type="checkbox"
            checked={option.colors !== null}
            onChange={(e) => {
              onChange({ ...option, colors: e.target.checked ? option.values.map(() => '') : null });
              setPicking(-1);
            }}
          />
          خيار ألوان (يظهر كدوائر في المتجر)
        </label>
        <button type="button" className="danger small" onClick={onRemove}>
          حذف الخيار
        </button>
      </div>
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="chips">
        {option.values.map((v, i) => (
          <span key={v} className="chip">
            {option.colors && (
              <button
                type="button"
                className={`swatch${shown(i) ? '' : ' unknown'}`}
                style={shown(i) ? { background: shown(i) } : undefined}
                aria-label={`لون ${v}${option.colors[i] ? `: ${option.colors[i] ?? ''}` : ''}`}
                aria-expanded={picking === i}
                onClick={() => {
                  open(i);
                }}
              />
            )}
            {v}
            <button
              type="button"
              className="x"
              aria-label={`احذف ${v}`}
              onClick={() => {
                remove(i);
              }}
            >
              ×
            </button>
          </span>
        ))}
        <input
          className="chip-input"
          value={typed}
          maxLength={50}
          aria-label={`قيمة جديدة لـ ${option.name || 'الخيار'}`}
          placeholder="أضف قيمة واضغط Enter"
          onChange={(e) => {
            setTyped(e.target.value);
          }}
          onKeyDown={add}
        />
      </div>
      {option.colors && value !== undefined && (
        <div
          className="picker"
          role="dialog"
          aria-label={`لون «${value}»`}
          onKeyDown={(e) => {
            if (e.key === 'Escape') setPicking(-1);
          }}
        >
          <div className="picker-head">
            <strong>لون «{value}»</strong>
            <button
              type="button"
              className="x"
              aria-label="إغلاق"
              onClick={() => {
                setPicking(-1);
              }}
            >
              ×
            </button>
          </div>
          <div className={`picker-preview${shown(picking) ? '' : ' unknown'}`} style={shown(picking) ? { background: shown(picking) } : undefined} />
          <div className="picker-palette">
            {palette.map((c) => (
              <button
                key={c}
                type="button"
                className="swatch"
                style={{ background: c }}
                aria-label={c}
                aria-pressed={option.colors?.[picking] === c}
                onClick={() => {
                  setColour(picking, c);
                  setHex(c);
                }}
              />
            ))}
          </div>
          <label className="picker-hex">
            الكود
            <input
              value={hex}
              dir="ltr"
              maxLength={7}
              onChange={(e) => {
                setHex(e.target.value);
                const h = normalizeHex(e.target.value);
                if (h) setColour(picking, h);
              }}
            />
          </label>
          <p className="muted small">
            {option.colors[picking] ? 'اخترت هذا اللون.' : guessColour(value) ? 'اقترحنا اللون من الاسم. غيّره ليطابق منتجك.' : 'لا نعرف هذا اللون من اسمه؛ اختر لونه.'}
          </p>
          {option.colors[picking] && (
            <button
              type="button"
              className="link"
              onClick={() => {
                setColour(picking, '');
                setHex(guessColour(value));
              }}
            >
              ارجع للون المقترح من الاسم
            </button>
          )}
        </div>
      )}
    </fieldset>
  );
}
