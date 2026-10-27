import { useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { problemMessage } from '../lib/problem';
import { stockFilters, type Product, type StockFilter } from '../lib/products';

type Variant = Product['variants'][number];

const pageSize = 25; // products a page; their variants are the rows

interface Row {
  p: Product;
  v: Variant;
}

const digits = /[٠-٩]/g;

function readCount(text: string): number | undefined {
  const t = text.trim().replace(digits, (d) => String(d.charCodeAt(0) - 0x0660));
  return /^\d{1,7}$/.test(t) && Number(t) <= 1_000_000 ? Number(t) : undefined;
}

// Inventory is every variant's stock in one table (U5, in the design
// system): on hand, held by orders not yet shipped, and free to sell. The
// merchant types a new count and saves the row; the stock filter and the
// low threshold match the products list.
export function Inventory() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const [rows, setRows] = useState<Row[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [trail, setTrail] = useState<(string | undefined)[]>([]);
  const [at, setAt] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [threshold, setThreshold] = useState(5);
  const [typed, setTyped] = useState<Record<string, string>>({});
  // Per row: "saving", "saved", an error message, or "".
  const [state, setState] = useState<Record<string, string>>({});
  // Running low first; "all" shows every variant.
  const stockParam = params.get('stock') ?? 'low';
  const stock: StockFilter = stockParam === 'all' ? '' : stockFilters.some((s) => s.id === stockParam) ? (stockParam as StockFilter) : 'low';

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/storefront/theme', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      const low = data?.settings.low_stock;
      if (live && low) setThreshold(low);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  useEffect(() => {
    let live = true;
    const query: { limit: number; stock?: Exclude<StockFilter, ''>; low_threshold: number; cursor?: string } = { limit: pageSize, low_threshold: threshold };
    if (stock) query.stock = stock;
    if (at) query.cursor = at;
    void api.GET('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId }, query } }).then(({ data, error: err }) => {
      if (!live) return;
      setLoading(false);
      if (!data) {
        setError(problemMessage(err));
        return;
      }
      setError('');
      // A product matches as a whole; its rows are the variants that do.
      const keep = (v: Variant) =>
        stock === 'low'
          ? v.stock.tracked && v.stock.available > 0 && v.stock.available <= threshold
          : stock === 'out'
            ? v.stock.tracked && v.stock.available === 0
            : true;
      setRows(data.items.flatMap((p) => p.variants.filter(keep).map((v) => ({ p, v }))));
      setNext(data.next_cursor);
    });
    return () => {
      live = false;
    };
  }, [storeId, stock, threshold, at]);

  function pick(s: StockFilter) {
    setLoading(true);
    setTrail([]);
    setAt(undefined);
    setParams(s === 'low' ? {} : { stock: s || 'all' });
  }

  async function save(r: Row) {
    const id = r.v.id;
    const n = readCount(typed[id] ?? '');
    if (n === undefined) {
      setState((s) => ({ ...s, [id]: 'اكتب عددًا صحيحًا' }));
      return;
    }
    setState((s) => ({ ...s, [id]: 'saving' }));
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/variants/{variant_id}/inventory', {
      params: { path: { store_id: storeId, variant_id: id } },
      body: { on_hand: n, tracked: true },
    });
    if (err) {
      setState((s) => ({ ...s, [id]: problemMessage(err) }));
      return;
    }
    setRows((list) => list.map((x) => (x.v.id === id ? { ...x, v: { ...x.v, stock: data } } : x)));
    setTyped((t) => ({ ...t, [id]: '' }));
    setState((s) => ({ ...s, [id]: 'saved' }));
  }

  const current = stock || 'all';
  const tabs: { id: string; label: string }[] = [
    { id: 'low', label: 'قارب على النفاد' },
    { id: 'out', label: 'نفد' },
    { id: 'in_stock', label: 'متوفر' },
    { id: 'all', label: 'الكل' },
  ];

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>المخزون</h1>
          <p className="muted">«قارب على النفاد» = {threshold} قطع أو أقل، حسب إعداد متجرك.</p>
        </div>
      </div>
      <section className="table-card products">
        <div className="tabs" role="tablist" aria-label="المخزون">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={current === t.id}
              onClick={() => {
                pick(t.id === 'all' ? '' : (t.id as StockFilter));
              }}
            >
              {t.label}
            </button>
          ))}
        </div>
        {error && <p role="alert">{error}</p>}
        <div className="scroll">
          <table className="inventory">
            <thead>
              <tr>
                <th>المنتج</th>
                <th>رمز SKU</th>
                <th>في المخزن</th>
                <th>محجوز لطلبات</th>
                <th>متاح للبيع</th>
                <th>عدد جديد</th>
              </tr>
            </thead>
            <tbody>
              {rows.map(({ p, v }) => {
                const name = v.option_values.length ? `${p.title} — ${v.option_values.join(' / ')}` : p.title;
                const st = state[v.id];
                return (
                  <tr key={v.id}>
                    <td>
                      <Link to={`/stores/${storeId}/products/${p.id}`} className="title">
                        {p.title}
                      </Link>
                      {v.option_values.length > 0 && <small className="sub">{v.option_values.join(' / ')}</small>}
                      {v.hidden && <span className="status p-draft">مخفي</span>}
                    </td>
                    <td dir="ltr" className="muted">
                      {v.sku ?? '—'}
                    </td>
                    {v.stock.tracked ? (
                      <>
                        <td className="num">{v.stock.on_hand}</td>
                        <td className="num muted">{v.stock.reserved}</td>
                        <td className="num">
                          {v.stock.available === 0 ? (
                            <span className="status stock-out">نفد</span>
                          ) : v.stock.available <= threshold ? (
                            <span className="status stock-low">{v.stock.available}</span>
                          ) : (
                            v.stock.available
                          )}
                        </td>
                        <td>
                          <form
                            className="stock-form"
                            onSubmit={(e) => {
                              e.preventDefault();
                              void save({ p, v });
                            }}
                          >
                            <input
                              inputMode="numeric"
                              aria-label={`عدد جديد في المخزن لـ ${name}`}
                              placeholder={String(v.stock.on_hand)}
                              value={typed[v.id] ?? ''}
                              onChange={(e) => {
                                setTyped((t) => ({ ...t, [v.id]: e.target.value }));
                                setState((s) => ({ ...s, [v.id]: '' }));
                              }}
                            />
                            <button type="submit" className="secondary small" disabled={!typed[v.id] || st === 'saving'}>
                              حفظ
                            </button>
                            {st === 'saved' && (
                              <span role="status" className="ok-note small">
                                حُفظ
                              </span>
                            )}
                            {st && st !== 'saved' && st !== 'saving' && (
                              <span role="alert" className="field-error">
                                {st}
                              </span>
                            )}
                          </form>
                        </td>
                      </>
                    ) : (
                      <td colSpan={4} className="muted">
                        لا يُتابَع مخزونه (غير محدود)
                      </td>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {loading && <p role="status" className="foot">جارٍ التحميل…</p>}
        {!loading && rows.length === 0 && !error && (
          <p className="empty flat">{stock === 'low' ? 'لا شيء قارب على النفاد.' : stock === 'out' ? 'لا شيء نفد.' : 'لا منتجات هنا.'}</p>
        )}
        {!loading && (trail.length > 0 || next) && (
          <div className="foot">
            <span className="muted small">صفحة {trail.length + 1}</span>
            <div className="actions">
              {trail.length > 0 && (
                <button
                  type="button"
                  className="secondary"
                  onClick={() => {
                    setLoading(true);
                    setAt(trail.at(-1));
                    setTrail((t) => t.slice(0, -1));
                  }}
                >
                  الصفحة السابقة
                </button>
              )}
              {next && (
                <button
                  type="button"
                  className="secondary"
                  onClick={() => {
                    setLoading(true);
                    setTrail((t) => [...t, at]);
                    setAt(next);
                  }}
                >
                  الصفحة التالية
                </button>
              )}
            </div>
          </div>
        )}
      </section>
    </Layout>
  );
}
