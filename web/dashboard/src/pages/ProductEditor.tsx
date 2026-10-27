import { useEffect, useState, type KeyboardEvent } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { OptionEditor } from '../components/OptionEditor';
import { ProductImages } from '../components/ProductImages';
import { VariantsGrid } from '../components/VariantsGrid';
import { build, draftOf, emptyDraft, maxOptions, maxVariants, newOption, regenerate, variantCount, type Draft, type OptionDraft } from '../lib/editor';
import { count } from '../lib/home';
import { problemMessage } from '../lib/problem';
import { productStatusLabel, type Category, type Product } from '../lib/products';
import { storefrontURL } from '../lib/storefront';

type SizeChart = components['schemas']['SizeChart'];

const symbols: Record<string, string> = { EGP: 'ج.م', SAR: 'ر.س', AED: 'د.إ' };

// ProductEditor creates or edits a product (the approved mockup): the
// basics, pictures, options that make the variants grid, and on the side
// its status, categories and size chart. Saving sends the whole product;
// stock of existing variants goes through the inventory endpoint.
export function ProductEditor() {
  const { storeId = '', productId } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const isNew = !productId;
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [saved, setSaved] = useState<Product | null>(null);
  const [loading, setLoading] = useState(!isNew);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState('');
  // A new product's page opens with "saved" (navigation state).
  const [notice, setNotice] = useState(() => ((location.state as { notice?: string } | null)?.notice ?? ''));
  const [saving, setSaving] = useState(false);
  const [categories, setCategories] = useState<Category[]>([]);
  const [charts, setCharts] = useState<SizeChart[]>([]);
  const [currency, setCurrency] = useState('EGP');
  const [slug, setSlug] = useState('');
  const [catText, setCatText] = useState('');

  useEffect(() => {
    if (isNew) return;
    let live = true;
    void api.GET('/v1/stores/{store_id}/products/{product_id}', { params: { path: { store_id: storeId, product_id: productId } } }).then(({ data, error }) => {
      if (!live) return;
      if (data) {
        setSaved(data);
        setDraft(draftOf(data));
      } else setFailure(problemMessage(error));
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [storeId, productId, isNew]);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/categories', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setCategories(data.items);
    });
    void api.GET('/v1/stores/{store_id}/size-charts', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setCharts(data.items);
    });
    void api.GET('/v1/stores/{store_id}', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) {
        setCurrency(data.currency);
        setSlug(data.slug);
      }
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  function setOptions(options: OptionDraft[]) {
    setDraft((d) =>
      variantCount(options) > maxVariants ? { ...d, options } : { ...d, options, gridOptions: options, variants: regenerate(d.gridOptions, options, d.variants) },
    );
  }

  async function addCategory(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    const name = catText.trim();
    if (!name) return;
    let cat = categories.find((c) => c.name.trim().toLowerCase() === name.toLowerCase());
    if (!cat) {
      const { data, error } = await api.POST('/v1/stores/{store_id}/categories', { params: { path: { store_id: storeId } }, body: { name, position: 0 } });
      if (error) {
        setErrors((x) => ({ ...x, category_ids: problemMessage(error) }));
        return;
      }
      cat = data;
      setCategories((list) => [...list, data]);
    }
    const id = cat.id;
    setDraft((d) => (d.categoryIds.includes(id) ? d : { ...d, categoryIds: [...d.categoryIds, id] }));
    setCatText('');
  }

  async function save() {
    setNotice('');
    setFailure('');
    const b = build(draft);
    if (variantCount(draft.options) > maxVariants) b.errors['variants'] = `الخيارات تكوّن أكثر من ${String(maxVariants)} نوع. قلّل القيم.`;
    setErrors(b.errors);
    if (Object.keys(b.errors).length > 0) {
      setFailure('راجع الحقول المعلّمة.');
      return;
    }
    // Variants left out are deleted with their stock: say so first.
    const keep = new Set(draft.variants.map((v) => v.id));
    const lost = (saved?.variants ?? []).filter((v) => !keep.has(v.id) && v.stock.on_hand > 0);
    if (lost.length > 0) {
      const units = lost.reduce((n, v) => n + v.stock.on_hand, 0);
      const names = lost.map((v) => v.option_values.join(' / ')).join('، ');
      if (!globalThis.confirm(`سيُحذف ${count(lost.length, 'نوع واحد', 'نوعان', 'أنواع', 'نوعًا')} (${names}) ومعه مخزون ${String(units)} قطعة. للاحتفاظ بالمخزون ألغِ «متاح للبيع» بدل حذف القيمة. متابعة؟`)) return;
    }
    setSaving(true);
    const res = saved
      ? await api.PUT('/v1/stores/{store_id}/products/{product_id}', { params: { path: { store_id: storeId, product_id: saved.id } }, body: b.body })
      : await api.POST('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId } }, body: b.body });
    if (res.error) {
      setSaving(false);
      setFailure(problemMessage(res.error));
      return;
    }
    let product = res.data;
    for (const s of b.stock) {
      const { error } = await api.PUT('/v1/stores/{store_id}/variants/{variant_id}/inventory', {
        params: { path: { store_id: storeId, variant_id: s.id } },
        body: { on_hand: s.onHand, tracked: s.tracked },
      });
      if (error) {
        setFailure(`حُفظ المنتج، وتعذّر تحديث مخزون نوع: ${problemMessage(error)}`);
        break;
      }
    }
    if (b.stock.length > 0) {
      const again = await api.GET('/v1/stores/{store_id}/products/{product_id}', { params: { path: { store_id: storeId, product_id: product.id } } });
      if (again.data) product = again.data;
    }
    setSaving(false);
    setSaved(product);
    setDraft(draftOf(product));
    setNotice('حُفظ المنتج.');
    if (!saved) void navigate(`/stores/${storeId}/products/${product.id}`, { replace: true, state: { notice: 'حُفظ المنتج.' } });
  }

  const chart = charts.find((c) => c.id === draft.sizeChartId);
  const catName = new Map(categories.map((c) => [c.id, c.name]));
  const err = (k: string) => errors[k];

  if (loading)
    return (
      <Layout storeId={storeId}>
        <p role="status">جارٍ التحميل…</p>
      </Layout>
    );

  return (
    <Layout storeId={storeId}>
      <div className="page-head editor-head">
        <div>
          <Link to={`/stores/${storeId}/products`} className="back">
            → المنتجات
          </Link>
          <div className="title-line">
            <h1>{saved?.title ?? 'منتج جديد'}</h1>
            {saved && <span className={`status p-${saved.status}`}>{productStatusLabel[saved.status]}</span>}
            {saved?.sample && <span className="status sample">تجريبي</span>}
          </div>
        </div>
        <div className="actions">
          {saved?.status === 'active' && slug && (
            <a className="button secondary" href={`${storefrontURL(slug)}/p/${encodeURIComponent(saved.slug)}`} target="_blank" rel="noreferrer">
              عرض في المتجر
            </a>
          )}
          <button type="button" className="primary" disabled={saving} onClick={() => void save()}>
            {saving ? 'جارٍ الحفظ…' : saved ? 'حفظ التغييرات' : 'حفظ المنتج'}
          </button>
        </div>
      </div>
      {failure && <p role="alert">{failure}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <div className="editor">
        <div className="editor-main">
          <section className="card editor-card" aria-labelledby="basics-title">
            <h2 id="basics-title">الأساسيات</h2>
            <label className="field-label">
              اسم المنتج
              <input
                value={draft.title}
                maxLength={200}
                aria-invalid={err('title') ? true : undefined}
                onChange={(e) => {
                  setDraft({ ...draft, title: e.target.value });
                }}
              />
            </label>
            {err('title') && <p className="field-error">{err('title')}</p>}
            <label className="field-label">
              الوصف
              <textarea
                rows={5}
                value={draft.description}
                maxLength={20000}
                onChange={(e) => {
                  setDraft({ ...draft, description: e.target.value });
                }}
              />
            </label>
          </section>

          {saved ? (
            <ProductImages storeId={storeId} productId={saved.id} images={saved.images} />
          ) : (
            <section className="card editor-card" aria-labelledby="images-title">
              <h2 id="images-title">الصور</h2>
              <p className="muted">احفظ المنتج أولًا، ثم أضف صوره.</p>
            </section>
          )}

          <section className="card editor-card" aria-labelledby="options-title">
            <div className="card-head">
              <h2 id="options-title">الخيارات</h2>
              <span className="muted">حتى {maxOptions} خيارات · نكوّن الأنواع منها تلقائيًا</span>
            </div>
            {draft.options.map((o, i) => (
              <OptionEditor
                key={o.uid}
                option={o}
                index={i}
                error={err(`options.${String(i)}`)}
                onChange={(next) => {
                  setOptions(draft.options.map((x, j) => (j === i ? next : x)));
                }}
                onRemove={() => {
                  setOptions(draft.options.filter((_, j) => j !== i));
                }}
              />
            ))}
            {variantCount(draft.options) > maxVariants && (
              <p className="field-error" role="alert">
                الخيارات تكوّن {variantCount(draft.options)} نوعًا، والحد {maxVariants}. قلّل القيم.
              </p>
            )}
            {draft.options.length < maxOptions && (
              <div>
                <button
                  type="button"
                  className="secondary"
                  onClick={() => {
                    setOptions([...draft.options, newOption()]);
                  }}
                >
                  + أضف خيارًا
                </button>
              </div>
            )}
          </section>

          <VariantsGrid
            options={draft.gridOptions}
            variants={draft.variants}
            currency={symbols[currency] ?? currency}
            errors={errors}
            onChange={(variants) => {
              setDraft({ ...draft, variants });
            }}
          />
        </div>

        <aside className="editor-side">
          <section className="card editor-card" aria-labelledby="status-title">
            <h2 id="status-title">الحالة</h2>
            <select
              aria-labelledby="status-title"
              value={draft.status}
              onChange={(e) => {
                setDraft({ ...draft, status: e.target.value as Draft['status'] });
              }}
            >
              <option value="active">منشور — يظهر في المتجر</option>
              <option value="draft">مسودة — مخفي</option>
              <option value="archived">مؤرشف</option>
            </select>
          </section>

          <section className="card editor-card" aria-labelledby="cats-title">
            <h2 id="cats-title">التصنيفات</h2>
            {draft.categoryIds.length > 0 && (
              <div className="chips">
                {draft.categoryIds.map((id) => (
                  <span key={id} className="chip">
                    {catName.get(id) ?? '…'}
                    <button
                      type="button"
                      className="x"
                      aria-label={`أزل ${catName.get(id) ?? ''}`}
                      onClick={() => {
                        setDraft({ ...draft, categoryIds: draft.categoryIds.filter((c) => c !== id) });
                      }}
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            )}
            <input
              list="category-names"
              value={catText}
              maxLength={120}
              aria-label="أضف تصنيفًا"
              placeholder="ابحث عن تصنيف أو أنشئ جديدًا"
              onChange={(e) => {
                setCatText(e.target.value);
              }}
              onKeyDown={(e) => void addCategory(e)}
            />
            <datalist id="category-names">
              {categories
                .filter((c) => !draft.categoryIds.includes(c.id))
                .map((c) => (
                  <option key={c.id} value={c.name} />
                ))}
            </datalist>
            <p className="muted small">اضغط Enter لإضافته؛ اسم جديد يُنشئ تصنيفًا.</p>
            {err('category_ids') && <p className="field-error">{err('category_ids')}</p>}
          </section>

          <section className="card editor-card" aria-labelledby="chart-title">
            <div className="card-head">
              <h2 id="chart-title">جدول المقاسات</h2>
              <span className="muted">بالسنتيمتر</span>
            </div>
            <select
              aria-labelledby="chart-title"
              value={draft.sizeChartId}
              onChange={(e) => {
                if (e.target.value === 'new') {
                  void navigate(`/stores/${storeId}/size-charts?new=1`);
                  return;
                }
                setDraft({ ...draft, sizeChartId: e.target.value });
              }}
            >
              <option value="">بلا جدول</option>
              {charts.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
              <option value="new">+ جدول جديد…</option>
            </select>
            {chart && (
              <>
                <div className="scroll mini-chart">
                  <table>
                    <thead>
                      <tr>
                        {chart.header.map((h) => (
                          <th key={h} scope="col">
                            {h}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {chart.body.map((r, i) => (
                        <tr key={i}>
                          {r.map((c, j) => (
                            <td key={j}>{c}</td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="muted small">
                  {chart.products === 0 ? 'لا يستخدمه أي منتج بعد.' : `مستخدم في ${count(chart.products, 'منتج واحد', 'منتجين', 'منتجات', 'منتجًا')}. تعديله يغيّرها كلها.`}
                </p>
                <Link className="button secondary small" to={`/stores/${storeId}/size-charts?id=${chart.id}`}>
                  تعديل الجدول
                </Link>
              </>
            )}
          </section>
        </aside>
      </div>
    </Layout>
  );
}
