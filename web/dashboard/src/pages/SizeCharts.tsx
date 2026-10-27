import { useEffect, useState } from 'react';
import { useParams, useSearchParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { count } from '../lib/home';
import { problemMessage } from '../lib/problem';

type SizeChart = components['schemas']['SizeChart'];

// Limits of a chart (ADR-0035).
const maxColumns = 8;
const maxRows = 30;

interface ChartDraft {
  id?: string;
  name: string;
  header: string[];
  body: string[][];
  products: number;
}

const blank = (): ChartDraft => ({ name: '', header: ['المقاس', 'الصدر', 'الطول'], body: [['S', '', ''], ['M', '', ''], ['L', '', '']], products: 0 });

const draftOf = (c: SizeChart): ChartDraft => ({ id: c.id, name: c.name, header: [...c.header], body: c.body.map((r) => [...r]), products: c.products });

// SizeCharts is the store's reusable size charts (the approved mockup):
// the list on one side, the chart being edited on the other, in
// centimetres with free column names, and how it shows on a product page.
export function SizeCharts() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const [charts, setCharts] = useState<SizeChart[]>([]);
  const [draft, setDraft] = useState<ChartDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [reloads, setReloads] = useState(0);
  const wanted = params.get('id');
  const fresh = params.get('new') === '1';

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/size-charts', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
      if (!live) return;
      setLoading(false);
      if (!data) {
        setError(problemMessage(err));
        return;
      }
      setCharts(data.items);
      setDraft((d) => {
        if (fresh) return d ?? blank();
        const pick = data.items.find((c) => c.id === (wanted ?? d?.id)) ?? data.items[0];
        return pick ? draftOf(pick) : blank();
      });
    });
    return () => {
      live = false;
    };
  }, [storeId, wanted, fresh, reloads]);

  function open(c: SizeChart | null) {
    setNotice('');
    setError('');
    setDraft(c ? draftOf(c) : blank());
    setParams(c ? { id: c.id } : { new: '1' });
  }

  function edit(patch: Partial<ChartDraft>) {
    setDraft((d) => (d ? { ...d, ...patch } : d));
  }

  function setCell(r: number, c: number, v: string) {
    if (!draft) return;
    edit({ body: draft.body.map((row, i) => (i === r ? row.map((x, j) => (j === c ? v : x)) : row)) });
  }

  async function save() {
    if (!draft) return;
    setSaving(true);
    setError('');
    setNotice('');
    const body = { name: draft.name, header: draft.header, body: draft.body };
    const res = draft.id
      ? await api.PUT('/v1/stores/{store_id}/size-charts/{size_chart_id}', { params: { path: { store_id: storeId, size_chart_id: draft.id } }, body })
      : await api.POST('/v1/stores/{store_id}/size-charts', { params: { path: { store_id: storeId } }, body });
    setSaving(false);
    if (res.error) {
      setError(problemMessage(res.error));
      return;
    }
    setNotice('حُفظ الجدول.');
    setDraft(draftOf(res.data));
    setParams({ id: res.data.id });
    setReloads((n) => n + 1);
  }

  async function remove() {
    if (!draft?.id) return;
    const uses = draft.products > 0 ? ` سيختفي من ${count(draft.products, 'منتج واحد', 'منتجين', 'منتجات', 'منتجًا')}.` : '';
    if (!globalThis.confirm(`حذف جدول «${draft.name}»؟${uses}`)) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/size-charts/{size_chart_id}', { params: { path: { store_id: storeId, size_chart_id: draft.id } } });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setDraft(null);
    setParams({});
    setReloads((n) => n + 1);
  }

  return (
    <Layout storeId={storeId}>
      <div className="charts">
        <section className="card" aria-labelledby="charts-title">
          <h1 id="charts-title">جداول المقاسات</h1>
          <p className="muted">جدول واحد يخدم منتجات كثيرة. اربطه من شاشة المنتج.</p>
          {loading && <p role="status">جارٍ التحميل…</p>}
          <ul className="chart-list">
            {charts.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  className="link"
                  aria-current={draft?.id === c.id ? 'true' : undefined}
                  onClick={() => {
                    open(c);
                  }}
                >
                  {c.name}
                </button>
                <span className="muted">{c.products === 0 ? 'لا منتجات' : count(c.products, 'منتج واحد', 'منتجان', 'منتجات', 'منتجًا')}</span>
              </li>
            ))}
          </ul>
          <button
            type="button"
            className="secondary"
            onClick={() => {
              open(null);
            }}
          >
            + جدول جديد
          </button>
        </section>

        {draft && (
          <div className="chart-side">
            <section className="card editor-card" aria-labelledby="chart-edit-title">
              <div className="card-head">
                <h2 id="chart-edit-title">{draft.id ? draft.name || 'جدول' : 'جدول جديد'}</h2>
                <div className="actions">
                  {draft.id && (
                    <button type="button" className="danger small" onClick={() => void remove()}>
                      حذف الجدول
                    </button>
                  )}
                  <button type="button" className="primary" disabled={saving} onClick={() => void save()}>
                    {saving ? 'جارٍ الحفظ…' : 'حفظ'}
                  </button>
                </div>
              </div>
              {error && <p role="alert">{error}</p>}
              {notice && (
                <p role="status" className="ok-note">
                  {notice}
                </p>
              )}
              <label className="field-label narrow">
                اسم الجدول (لك فقط)
                <input
                  value={draft.name}
                  maxLength={60}
                  placeholder="مثل: تيشيرتات رجالي"
                  onChange={(e) => {
                    edit({ name: e.target.value });
                  }}
                />
              </label>
              <p className="muted">كل القياسات بالسنتيمتر. سمِّ الأعمدة كما تريد. العمود الأول هو المقاس.</p>
              <div className="scroll">
                <table className="chart-grid">
                  <thead>
                    <tr>
                      {draft.header.map((h, c) => (
                        <th key={c} scope="col">
                          <div className="cell-row">
                            <input
                              className="head"
                              value={h}
                              maxLength={30}
                              aria-label={`اسم العمود ${String(c + 1)}`}
                              onChange={(e) => {
                                edit({ header: draft.header.map((x, j) => (j === c ? e.target.value : x)) });
                              }}
                            />
                            {c > 0 && draft.header.length > 2 && (
                              <button
                                type="button"
                                className="x"
                                aria-label={`احذف عمود ${h || String(c + 1)}`}
                                onClick={() => {
                                  edit({ header: draft.header.filter((_, j) => j !== c), body: draft.body.map((r) => r.filter((_, j) => j !== c)) });
                                }}
                              >
                                ×
                              </button>
                            )}
                          </div>
                        </th>
                      ))}
                      <th scope="col">
                        {draft.header.length < maxColumns && (
                          <button
                            type="button"
                            className="secondary small"
                            onClick={() => {
                              edit({ header: [...draft.header, ''], body: draft.body.map((r) => [...r, '']) });
                            }}
                          >
                            + عمود
                          </button>
                        )}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {draft.body.map((row, r) => (
                      <tr key={r}>
                        {row.map((cell, c) => (
                          <td key={c}>
                            <input
                              value={cell}
                              maxLength={30}
                              inputMode={c === 0 ? 'text' : 'decimal'}
                              aria-label={c === 0 ? `المقاس، صف ${String(r + 1)}` : `${draft.header[c] ?? ''} ${row[0] ?? ''}`}
                              onChange={(e) => {
                                setCell(r, c, e.target.value);
                              }}
                            />
                          </td>
                        ))}
                        <td>
                          {draft.body.length > 1 && (
                            <button
                              type="button"
                              className="x"
                              aria-label={`احذف صف ${row[0] !== undefined && row[0] !== "" ? row[0] : String(r + 1)}`}
                              onClick={() => {
                                edit({ body: draft.body.filter((_, i) => i !== r) });
                              }}
                            >
                              ×
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="actions">
                {draft.body.length < maxRows && (
                  <button
                    type="button"
                    className="secondary small"
                    onClick={() => {
                      edit({ body: [...draft.body, draft.header.map(() => '')] });
                    }}
                  >
                    + صف
                  </button>
                )}
                <span className="muted small">
                  حتى {maxColumns} أعمدة و{maxRows} صفًا.
                </span>
              </div>
              {draft.id && (
                <p className="muted">
                  {draft.products === 0 ? 'لا يستخدمه أي منتج بعد.' : `مستخدم في ${count(draft.products, 'منتج واحد', 'منتجين', 'منتجات', 'منتجًا')}.`}
                </p>
              )}
            </section>

            <section className="card editor-card" aria-labelledby="chart-preview-title">
              <h2 id="chart-preview-title">كيف يظهر في صفحة المنتج</h2>
              <div className="shop-preview">
                <details open>
                  <summary>جدول المقاسات</summary>
                  <div className="scroll">
                    <table>
                      <caption>القياسات بالسنتيمتر</caption>
                      <thead>
                        <tr>
                          {draft.header.map((h, c) => (
                            <th key={c} scope="col">
                              {h}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {draft.body.map((row, r) => (
                          <tr key={r}>
                            {row.map((cell, c) =>
                              c === 0 ? (
                                <th key={c} scope="row">
                                  {cell}
                                </th>
                              ) : (
                                <td key={c}>{cell}</td>
                              ),
                            )}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </details>
              </div>
            </section>
          </div>
        )}
      </div>
    </Layout>
  );
}
