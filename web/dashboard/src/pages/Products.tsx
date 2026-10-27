import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { SamplesBanner } from '../components/SamplesBanner';
import { count } from '../lib/home';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import {
  priceLine,
  productStatusLabel,
  productsTotal,
  productTabs,
  selectedProducts,
  stockFilters,
  stockView,
  thumb,
  variantsLine,
  type Category,
  type Product,
  type ProductCounts,
  type ProductStatus,
  type StockFilter,
} from '../lib/products';
import { field } from '../session';

type Hit = components['schemas']['ProductSearchHit'];

const pageSize = 20;

// A row is a full product from the list, or a search hit (which carries
// no variants: the search index has no stock).
type Row = { kind: 'product'; p: Product } | { kind: 'hit'; h: Hit };

interface Page {
  rows: Row[];
  next?: string | undefined; // the list's cursor, or the next search offset
  error?: string;
}

interface Filters {
  status: ProductStatus | '';
  q: string;
  category: string;
  stock: StockFilter;
}

function filtersOf(params: URLSearchParams): Filters {
  const status = params.get('status') ?? '';
  const stock = params.get('stock') ?? '';
  return {
    status: (['active', 'draft', 'archived'] as string[]).includes(status) ? (status as ProductStatus) : '',
    q: params.get('q') ?? '',
    category: params.get('category') ?? '',
    stock: stockFilters.some((s) => s.id === stock) ? (stock as StockFilter) : '',
  };
}

async function fetchPage(storeId: string, f: Filters, threshold: number, at: string | undefined): Promise<Page> {
  try {
    if (f.q) {
      const offset = Number(at ?? 0);
      const query: { q: string; limit: number; offset: number; status?: ProductStatus; category_id?: string } = { q: f.q, limit: pageSize, offset };
      if (f.status) query.status = f.status;
      if (f.category) query.category_id = f.category;
      const { data, error } = await api.GET('/v1/stores/{store_id}/products/search', { params: { path: { store_id: storeId }, query } });
      if (error) return { rows: [], error: problemMessage(error) };
      const more = offset + data.items.length < data.estimated_total && data.items.length === pageSize;
      return { rows: data.items.map((h) => ({ kind: 'hit', h })), next: more ? String(offset + pageSize) : undefined };
    }
    const query: { limit: number; status?: ProductStatus; category_id?: string; stock?: Exclude<StockFilter, ''>; low_threshold?: number; cursor?: string } = {
      limit: pageSize,
    };
    if (f.status) query.status = f.status;
    if (f.category) query.category_id = f.category;
    if (f.stock) {
      query.stock = f.stock;
      query.low_threshold = threshold;
    }
    if (at) query.cursor = at;
    const { data, error } = await api.GET('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId }, query } });
    if (error) return { rows: [], error: problemMessage(error) };
    return { rows: data.items.map((p) => ({ kind: 'product', p })), next: data.next_cursor };
  } catch (err) {
    return { rows: [], error: problemMessage(err) };
  }
}

function rowID(r: Row): string {
  return r.kind === 'product' ? r.p.id : r.h.id;
}

// Products lists the store's products (the approved mockup): the samples
// banner, status tabs with their counts, search, category and stock
// filters, a bulk bar for the selected, and a page at a time.
export function Products() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const [rows, setRows] = useState<Row[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [trail, setTrail] = useState<(string | undefined)[]>([]); // where earlier pages started
  const [at, setAt] = useState<string | undefined>();
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [reloads, setReloads] = useState(0);
  const [counts, setCounts] = useState<ProductCounts | null>(null);
  const [categories, setCategories] = useState<Category[]>([]);
  const [threshold, setThreshold] = useState(5);
  const paramsKey = params.toString();
  const f = filtersOf(params);

  useEffect(() => {
    let live = true;
    void fetchPage(storeId, filtersOf(new URLSearchParams(paramsKey)), threshold, at).then((p) => {
      if (!live) return;
      setRows(p.rows);
      setNext(p.next);
      setError(p.error ?? '');
      setSelected(new Set());
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [storeId, paramsKey, threshold, at, reloads]);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/products/counts', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setCounts(data);
    });
    return () => {
      live = false;
    };
  }, [storeId, reloads]);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/categories', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setCategories(data.items);
    });
    // "Running low" follows the store's own threshold (0 turns the shop's
    // line off; the list still needs a bound).
    void api.GET('/v1/stores/{store_id}/storefront/theme', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      const low = data?.settings.low_stock;
      if (live && low) setThreshold(low);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  function setFilters(nextParams: URLSearchParams) {
    setLoading(true);
    setTrail([]);
    setAt(undefined);
    setParams(nextParams);
  }

  function applyFilters(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const nextParams = new URLSearchParams();
    if (f.status) nextParams.set('status', f.status);
    for (const k of ['q', 'category', 'stock'] as const) {
      const v = field(form, k).trim();
      if (v) nextParams.set(k, v);
    }
    setFilters(nextParams);
  }

  function pickTab(status: ProductStatus | '') {
    const nextParams = new URLSearchParams(params);
    if (status) nextParams.set('status', status);
    else nextParams.delete('status');
    setFilters(nextParams);
  }

  function goNext() {
    setLoading(true);
    setTrail((t) => [...t, at]);
    setAt(next);
  }

  function goBack() {
    setLoading(true);
    setAt(trail.at(-1));
    setTrail((t) => t.slice(0, -1));
  }

  async function bulk(action: ProductStatus | 'delete') {
    if (action === 'delete' && !globalThis.confirm(`حذف ${selectedProducts(selected.size).replace('حدّدت ', '')}؟ لا يمكن التراجع.`)) return;
    setBusy(true);
    setNotice('');
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/products/bulk', {
      params: { path: { store_id: storeId } },
      body: { product_ids: [...selected], action },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    const done = { active: 'نُشر', draft: 'تحوّل إلى مسودة', archived: 'أُرشف', delete: 'حُذف' }[action];
    setNotice(`${done}: ${productsTotal(data.updated)}`);
    setLoading(true);
    setReloads((n) => n + 1);
  }

  function toggle(id: string, on: boolean) {
    const s = new Set(selected);
    if (on) s.add(id);
    else s.delete(id);
    setSelected(s);
  }

  const categoryName = new Map(categories.map((c) => [c.id, c.name]));
  const first = trail.length * pageSize + 1;
  const shownTotal = counts ? counts[productTabs.find((t) => t.status === f.status)?.key ?? 'all'] : undefined;
  const filtered = f.q !== '' || f.category !== '' || f.stock !== '';

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>المنتجات</h1>
          {counts && <p className="muted">{productsTotal(counts.all)}</p>}
        </div>
        <div className="actions">
          <Link className="button secondary" to={`/stores/${storeId}/imports`}>
            استيراد من ملف
          </Link>
          <Link className="button primary" to={`/stores/${storeId}/products/new`}>
            أضف منتجًا
          </Link>
        </div>
      </div>
      <SamplesBanner storeId={storeId} />

      <section className="table-card products">
        <div className="tabs" role="tablist" aria-label="حالة المنتج">
          {productTabs.map((t) => (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={f.status === t.status}
              onClick={() => {
                pickTab(t.status);
              }}
            >
              <span>{t.label}</span>
              {counts && (
                <>
                  {' '}
                  <span className="count">{counts[t.key]}</span>
                </>
              )}
            </button>
          ))}
        </div>

        <form className="filters" onSubmit={applyFilters} aria-label="تصفية المنتجات" key={paramsKey}>
          <label className="grow">
            <span className="visually-hidden">ابحث</span>
            <input name="q" type="search" defaultValue={f.q} placeholder="ابحث بالاسم أو رمز SKU" maxLength={200} />
          </label>
          <label>
            التصنيف
            <select name="category" defaultValue={f.category}>
              <option value="">كل التصنيفات</option>
              {categories.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            المخزون
            <select name="stock" defaultValue={f.stock} disabled={f.q !== ''} aria-describedby={f.q ? 'stock-note' : undefined}>
              {stockFilters.map((s) => (
                <option key={s.id || 'all'} value={s.id}>
                  {s.label}
                </option>
              ))}
            </select>
          </label>
          <button type="submit" className="secondary">
            تصفية
          </button>
          {f.q && (
            <p id="stock-note" className="muted note">
              التصفية بالمخزون لا تعمل مع البحث.
            </p>
          )}
        </form>

        {selected.size > 0 && (
          <div className="bulk" role="region" aria-label="إجراءات المحدد">
            <strong>{selectedProducts(selected.size)}</strong>
            <div className="actions">
              <button type="button" className="secondary" disabled={busy} onClick={() => void bulk('active')}>
                نشر
              </button>
              <button type="button" className="secondary" disabled={busy} onClick={() => void bulk('draft')}>
                تحويل لمسودة
              </button>
              <button type="button" className="secondary" disabled={busy} onClick={() => void bulk('archived')}>
                أرشفة
              </button>
              <button type="button" className="danger" disabled={busy} onClick={() => void bulk('delete')}>
                حذف
              </button>
              <button
                type="button"
                className="link"
                onClick={() => {
                  setSelected(new Set());
                }}
              >
                إلغاء التحديد
              </button>
            </div>
          </div>
        )}
        {notice && <p role="status" className="ok-note">{notice}</p>}
        {error && <p role="alert">{error}</p>}

        <div className="scroll">
          <table className="products-table">
            <thead>
              <tr>
                <th className="pick">
                  <input
                    type="checkbox"
                    aria-label="حدّد الكل"
                    disabled={rows.length === 0}
                    checked={rows.length > 0 && rows.every((r) => selected.has(rowID(r)))}
                    onChange={(e) => {
                      setSelected(e.target.checked ? new Set(rows.map(rowID)) : new Set());
                    }}
                  />
                </th>
                <th>المنتج</th>
                <th>الحالة</th>
                <th>المخزون</th>
                <th>التصنيف</th>
                <th>السعر</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => {
                const id = rowID(r);
                const title = r.kind === 'product' ? r.p.title : r.h.title;
                const status = r.kind === 'product' ? r.p.status : r.h.status;
                const cats = r.kind === 'product' ? r.p.category_ids : r.h.category_ids;
                const img = r.kind === 'product' ? thumb(r.p) : r.h.thumb?.webp;
                return (
                  <tr key={id} className={selected.has(id) ? 'picked' : undefined}>
                    <td className="pick">
                      <input
                        type="checkbox"
                        aria-label={`حدّد ${title}`}
                        checked={selected.has(id)}
                        onChange={(e) => {
                          toggle(id, e.target.checked);
                        }}
                      />
                    </td>
                    <td className="c-product">
                      <div className="product-cell">
                        {img ? <img className="thumb" src={img} alt="" width={44} height={44} /> : <span className="thumb" aria-hidden="true" />}
                        <div>
                          <Link to={`/stores/${storeId}/products/${id}`} className="title">
                            {title}
                          </Link>
                          {r.kind === 'product' && r.p.sample && <span className="status sample">تجريبي</span>}
                          {r.kind === 'product' && <small className="sub">{variantsLine(r.p)}</small>}
                        </div>
                      </div>
                    </td>
                    <td className="c-status">
                      <span className={`status p-${status}`}>{productStatusLabel[status]}</span>
                    </td>
                    <td className="c-stock">{r.kind === 'product' ? <StockCell p={r.p} threshold={threshold} /> : <span className="muted">—</span>}</td>
                    <td className="muted c-category">{cats.map((c) => categoryName.get(c)).filter(Boolean).join('، ') || '—'}</td>
                    <td className="num c-price">{r.kind === 'product' ? <PriceCell p={r.p} /> : `من ${money(r.h.price_minor, r.h.currency)}`}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {loading && <p role="status" className="foot">جارٍ التحميل…</p>}
        {!loading && rows.length === 0 && !error && (
          <div className="empty flat">
            {filtered || f.status ? (
              <p>لا منتجات تطابق التصفية.</p>
            ) : (
              <>
                <p>لا منتجات في متجرك بعد.</p>
                <Link className="button primary" to={`/stores/${storeId}/products/new`}>
                  أضف أول منتج
                </Link>
              </>
            )}
          </div>
        )}
        {!loading && rows.length > 0 && (
          <div className="foot">
            <span>
              يعرض {first}–{first + rows.length - 1}
              {shownTotal !== undefined && !filtered && ` من ${String(shownTotal)}`}
            </span>
            <div className="actions">
              {trail.length > 0 && (
                <button type="button" className="secondary" onClick={goBack}>
                  الصفحة السابقة
                </button>
              )}
              {next && (
                <button type="button" className="secondary" onClick={goNext}>
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

function StockCell({ p, threshold }: { p: Product; threshold: number }) {
  const s = stockView(p, threshold);
  if (s.state === 'untracked') return <span className="muted">غير محدود</span>;
  const spread = p.variants.length > 1 ? <span className="muted"> في {count(s.variants, 'نوع واحد', 'نوعين', 'أنواع', 'نوعًا')}</span> : null;
  if (s.state === 'out')
    return (
      <>
        0 <span className="status stock-out">نفد</span>
      </>
    );
  if (s.state === 'low')
    return (
      <>
        <span className="status stock-low">قارب على النفاد</span>
        {p.variants.length > 1 && <span className="muted"> {count(s.lowVariants, 'نوع واحد', 'نوعان', 'أنواع', 'نوعًا')}</span>}
      </>
    );
  return (
    <>
      {s.units}
      {spread}
    </>
  );
}

function PriceCell({ p }: { p: Product }) {
  const { price, before } = priceLine(p);
  return (
    <>
      {price}
      {before && (
        <>
          {' '}
          <s className="muted">{before}</s>
        </>
      )}
    </>
  );
}
