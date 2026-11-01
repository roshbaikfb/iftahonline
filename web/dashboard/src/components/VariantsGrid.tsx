import { useState } from 'react';

import { colourIndex, maxVariants, swatchOf, type OptionDraft, type VariantDraft } from '../lib/editor';
import { count } from '../lib/home';

interface Props {
  options: OptionDraft[];
  variants: VariantDraft[];
  currency: string; // the symbol, for the price column
  errors: Record<string, string>;
  onChange: (v: VariantDraft[]) => void;
}

// VariantsGrid is every combination of the options (the approved
// mockup): price, price before the discount, the merchant's cost (for
// the profit dashboard, ADR-0068), SKU, stock and whether it
// is on sale, grouped by colour when there is a colour option. Unticking
// «متاح للبيع» hides the variant and keeps its stock (the owner,
// 2026-10-27). A row applies one price or stock to all.
export function VariantsGrid({ options, variants, currency, errors, onChange }: Props) {
  const [all, setAll] = useState({ price: '', compare: '', stock: '' });
  const ci = colourIndex(options);
  const named = options.filter((o) => o.values.length > 0);
  const grouped = ci >= 0 && named.length > 1;

  function set(i: number, patch: Partial<VariantDraft>) {
    onChange(variants.map((v, j) => (j === i ? { ...v, ...patch } : v)));
  }

  function applyAll() {
    onChange(
      variants.map((v) => ({
        ...v,
        ...(all.price.trim() ? { price: all.price.trim() } : {}),
        ...(all.compare.trim() ? { compare: all.compare.trim() } : {}),
        ...(all.stock.trim() && v.tracked ? { stock: all.stock.trim() } : {}),
      })),
    );
  }

  const label = (v: VariantDraft) => (v.values.length ? v.values.join(' / ') : 'المنتج');

  const row = (v: VariantDraft, i: number) => {
    const at = `variants.${String(i)}`;
    const name = label(v);
    return (
      <tr key={v.values.join('\u0000') || 'single'} className={v.hidden ? 'hidden-variant' : undefined}>
        <th scope="row">{name}</th>
        <td data-label="السعر">
          <input
            inputMode="decimal"
            value={v.price}
            aria-label={`سعر ${name}`}
            aria-invalid={errors[`${at}.price`] ? true : undefined}
            onChange={(e) => {
              set(i, { price: e.target.value });
            }}
          />
        </td>
        <td data-label="قبل الخصم">
          <input
            inputMode="decimal"
            value={v.compare}
            aria-label={`السعر قبل الخصم لـ ${name}`}
            aria-invalid={errors[`${at}.compare`] ? true : undefined}
            onChange={(e) => {
              set(i, { compare: e.target.value });
            }}
          />
        </td>
        <td data-label="التكلفة">
          <input
            inputMode="decimal"
            value={v.cost ?? ''}
            aria-label={`تكلفة ${name}`}
            aria-invalid={errors[`${at}.cost`] ? true : undefined}
            onChange={(e) => {
              set(i, { cost: e.target.value });
            }}
          />
        </td>
        <td data-label="رمز SKU">
          <input
            dir="ltr"
            value={v.sku}
            maxLength={64}
            aria-label={`رمز SKU لـ ${name}`}
            onChange={(e) => {
              set(i, { sku: e.target.value });
            }}
          />
        </td>
        <td data-label="المخزون">
          {v.tracked ? (
            <input
              inputMode="numeric"
              className="qty"
              value={v.stock}
              aria-label={`مخزون ${name}`}
              aria-invalid={errors[`${at}.stock`] ? true : undefined}
              onChange={(e) => {
                set(i, { stock: e.target.value });
              }}
            />
          ) : (
            <span className="muted">غير محدود</span>
          )}
        </td>
        <td className="center" data-label="متاح للبيع">
          <input
            type="checkbox"
            checked={!v.hidden}
            aria-label={`${name} متاح للبيع`}
            onChange={(e) => {
              set(i, { hidden: !e.target.checked });
            }}
          />
        </td>
      </tr>
    );
  };

  const head = (
    <thead>
      <tr>
        <th scope="col">النوع</th>
        <th scope="col">السعر ({currency})</th>
        <th scope="col">قبل الخصم</th>
        <th scope="col" title="لا يراها العملاء؛ تُحسب بها أرباحك">التكلفة</th>
        <th scope="col">رمز SKU</th>
        <th scope="col">المخزون</th>
        <th scope="col">متاح للبيع</th>
      </tr>
    </thead>
  );

  const fieldErrors = Object.entries(errors).filter(([k]) => k.startsWith('variants'));

  return (
    <section className="card editor-card" aria-labelledby="variants-title">
      <div className="card-head">
        <h2 id="variants-title">الأنواع</h2>
        <span className="muted">
          {count(variants.length, 'نوع واحد', 'نوعان', 'أنواع', 'نوعًا')}
          {named.length > 1 && ` (${named.map((o) => String(o.values.length)).join(' × ')})`} · الحد {maxVariants}
        </span>
      </div>
      {variants.length > 1 && (
        <div className="apply-all" role="group" aria-label="طبّق على الكل">
          <strong>طبّق على الكل:</strong>
          <label>
            السعر
            <input
              inputMode="decimal"
              value={all.price}
              onChange={(e) => {
                setAll({ ...all, price: e.target.value });
              }}
            />
          </label>
          <label>
            قبل الخصم
            <input
              inputMode="decimal"
              value={all.compare}
              onChange={(e) => {
                setAll({ ...all, compare: e.target.value });
              }}
            />
          </label>
          <label>
            المخزون
            <input
              inputMode="numeric"
              value={all.stock}
              onChange={(e) => {
                setAll({ ...all, stock: e.target.value });
              }}
            />
          </label>
          <button type="button" className="secondary small" onClick={applyAll}>
            طبّق
          </button>
        </div>
      )}
      {fieldErrors.length > 0 && (
        <ul className="field-error" role="alert">
          {[...new Set(fieldErrors.map(([, m]) => m))].map((m) => (
            <li key={m}>{m}</li>
          ))}
        </ul>
      )}
      <div className="scroll">
        <table className="variants">
          {head}
          {grouped ? (
            (options[ci]?.values ?? []).map((colour, g) => {
              const rows = variants.map((v, i) => ({ v, i })).filter(({ v }) => v.values[ci] === colour);
              const units = rows.reduce((n, { v }) => n + (Number(v.stock) || 0), 0);
              const opt = options[ci];
              const hex = opt ? swatchOf(opt, g) : '';
              return (
                <Group key={colour} title={colour} hex={hex} open={g === 0} summary={`${count(rows.length, 'نوع واحد', 'نوعان', 'أنواع', 'نوعًا')} · ${count(units, 'قطعة واحدة', 'قطعتان', 'قطع', 'قطعة')}`}>
                  {rows.map(({ v, i }) => row(v, i))}
                </Group>
              );
            })
          ) : (
            <tbody>{variants.map((v, i) => row(v, i))}</tbody>
          )}
        </table>
      </div>
      <p className="muted small">ألغِ «متاح للبيع» لنوع لا تبيعه الآن: يختفي من المتجر ويبقى مخزونه، وتعيده متى شئت.</p>
    </section>
  );
}

// Group is one colour's rows, folded under a header row.
function Group({ title, hex, open, summary, children }: { title: string; hex: string; open: boolean; summary: string; children: React.ReactNode }) {
  const [shown, setShown] = useState(open);
  return (
    <tbody className="group">
      <tr className="group-head">
        <th colSpan={6} scope="rowgroup">
          <button
            type="button"
            className="link"
            aria-expanded={shown}
            onClick={() => {
              setShown(!shown);
            }}
          >
            <span className={`swatch${hex ? '' : ' unknown'}`} style={hex ? { background: hex } : undefined} aria-hidden="true" />
            {title} · {summary}
          </button>
        </th>
      </tr>
      {shown && children}
    </tbody>
  );
}
