import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { flat, maxDepth, parentChoices, tree, type Node } from '../lib/categories';
import { problemMessage } from '../lib/problem';
import type { Category } from '../lib/products';
import { field } from '../session';

// Categories is the store's category tree (U5, in the design system):
// up to three levels, each shown in the shop's menu by position. A
// category is renamed, moved or reordered in place; one with
// subcategories cannot be deleted until they move.
export function Categories() {
  const { storeId = '' } = useParams();
  const [list, setList] = useState<Category[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(''); // a category's ID, "new", or ""
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/categories', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
      if (!live) return;
      setLoading(false);
      if (data) setList(data.items);
      else setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, [storeId, reloads]);

  const nodes = tree(list);

  async function save(e: SubmitEvent<HTMLFormElement>, id: string) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const parent = field(form, 'parent');
    const body: { name: string; position: number; parent_id?: string } = {
      name: field(form, 'name').trim(),
      position: Number(field(form, 'position')) || 0,
    };
    if (parent) body.parent_id = parent;
    setError('');
    const res =
      id === 'new'
        ? await api.POST('/v1/stores/{store_id}/categories', { params: { path: { store_id: storeId } }, body })
        : await api.PUT('/v1/stores/{store_id}/categories/{category_id}', { params: { path: { store_id: storeId, category_id: id } }, body });
    if (res.error) {
      setError(problemMessage(res.error));
      return;
    }
    setNotice(id === 'new' ? `أُضيف «${res.data.name}».` : `حُفظ «${res.data.name}».`);
    setEditing('');
    setReloads((n) => n + 1);
  }

  async function remove(n: Node) {
    if (n.children.length > 0) {
      setError(`انقل التصنيفات الفرعية تحت «${n.c.name}» أو احذفها أولًا.`);
      return;
    }
    if (!globalThis.confirm(`حذف تصنيف «${n.c.name}»؟ منتجاته تبقى، وتخرج منه فقط.`)) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/categories/{category_id}', { params: { path: { store_id: storeId, category_id: n.c.id } } });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setNotice(`حُذف «${n.c.name}».`);
    setReloads((x) => x + 1);
  }

  const form = (id: string, c?: Category) => (
    <form
      className="cat-form"
      aria-label={c ? `تعديل ${c.name}` : 'تصنيف جديد'}
      onSubmit={(e) => {
        void save(e, id);
      }}
    >
      <label className="field-label">
        الاسم
        <input name="name" required maxLength={120} defaultValue={c?.name ?? ''} />
      </label>
      <label className="field-label">
        تحت
        <select name="parent" defaultValue={c?.parent_id ?? ''}>
          <option value="">— تصنيف رئيسي —</option>
          {parentChoices(nodes, id === 'new' ? '' : id).map((p) => (
            <option key={p.c.id} value={p.c.id}>
              {'— '.repeat(p.depth - 1)}
              {p.c.name}
            </option>
          ))}
        </select>
      </label>
      <label className="field-label pos">
        الترتيب
        <input name="position" type="number" min={0} max={10000} defaultValue={c?.position ?? 0} />
      </label>
      <div className="actions">
        <button type="submit" className="primary small">
          حفظ
        </button>
        <button
          type="button"
          className="link"
          onClick={() => {
            setEditing('');
          }}
        >
          إلغاء
        </button>
      </div>
    </form>
  );

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>التصنيفات</h1>
          <p className="muted">تظهر في قائمة متجرك بالترتيب، حتى {maxDepth} مستويات.</p>
        </div>
        <button
          type="button"
          className="primary"
          onClick={() => {
            setEditing('new');
            setNotice('');
          }}
        >
          أضف تصنيفًا
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      <section className="card editor-card" aria-label="شجرة التصنيفات">
        {editing === 'new' && form('new')}
        {loading && <p role="status">جارٍ التحميل…</p>}
        {!loading && list.length === 0 && editing !== 'new' && <p className="muted">لا تصنيفات بعد. التصنيفات ترتّب منتجاتك في قائمة المتجر.</p>}
        <ul className="cat-tree">
          {flat(nodes).map((n) => (
            <li key={n.c.id} className={`depth-${String(n.depth)}`}>
              {editing === n.c.id ? (
                form(n.c.id, n.c)
              ) : (
                <div className="cat-row">
                  <div>
                    <Link to={`/stores/${storeId}/products?category=${n.c.id}`} className="title">
                      {n.c.name}
                    </Link>
                    <small className="sub" dir="ltr">
                      /c/{n.c.slug}
                    </small>
                  </div>
                  <span className="muted small">ترتيب {n.c.position}</span>
                  <div className="actions">
                    <button
                      type="button"
                      className="secondary small"
                      onClick={() => {
                        setEditing(n.c.id);
                        setNotice('');
                      }}
                    >
                      تعديل
                    </button>
                    <button type="button" className="danger small" aria-label={`احذف ${n.c.name}`} onClick={() => void remove(n)}>
                      حذف
                    </button>
                  </div>
                </div>
              )}
            </li>
          ))}
        </ul>
      </section>
    </Layout>
  );
}
