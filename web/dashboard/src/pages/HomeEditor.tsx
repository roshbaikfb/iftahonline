import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams } from 'react-router';

import '../shop-fonts.css';
import { api, type components } from '../api/client';
import { AddSectionDialog } from '../components/AddSectionDialog';
import { HomePreview, type PreviewCategory } from '../components/HomePreview';
import { Layout } from '../components/Layout';
import { SectionForm, type Choices } from '../components/SectionForm';
import { SectionIcon } from '../components/SectionIcon';
import type { PreviewProduct } from '../components/ThemePreview';
import { useNarrow } from '../lib/narrow';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import {
  fieldErrors as toFieldErrors,
  newID,
  phoneSummary,
  sectionErrors,
  starter,
  summary,
  typeIcon,
  typeLabel,
  type FieldError,
  type Schema,
  type Section,
  type Settings,
} from '../lib/sections';
import { storefrontURL } from '../lib/storefront';
import type { ThemeSettings } from '../lib/theme';

type Page = components['schemas']['Page'];

const saveDelay = 700; // ms of quiet before the draft is saved

type SaveState = 'idle' | 'saving' | 'saved' | 'error';

// HomeEditor is the shop's first page (U6, the approved mockup): its
// sections in order, the chosen one's fields, and the draft as an
// approximate page. Edits are saved as a draft (ADR-0036); shoppers see
// the published page until «نشر». On phones each section opens in place.
export function HomeEditor() {
  const { storeId = '' } = useParams();
  const narrow = useNarrow();
  const [published, setPublished] = useState<Page | null>(null);
  const [sections, setSections] = useState<Section[] | null>(null);
  const [meta, setMeta] = useState({ title: '', description: '' });
  const [hasDraft, setHasDraft] = useState(false);
  const [save, setSave] = useState<SaveState>('idle');
  const [error, setError] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState('');
  const [adding, setAdding] = useState(false);
  const [phone, setPhone] = useState(true);
  const [schemas, setSchemas] = useState<Record<string, Schema>>({});
  const [theme, setTheme] = useState<ThemeSettings | null>(null);
  const [store, setStore] = useState<{ name: string; slug: string } | null>(null);
  const [logo, setLogo] = useState<string | undefined>();
  const [categories, setCategories] = useState<(PreviewCategory & { slug: string; top: boolean })[]>([]);
  const [products, setProducts] = useState<(PreviewProduct & { id: string; slug: string })[]>([]);
  const [pages, setPages] = useState<{ id: string; title: string; slug: string }[]>([]);
  const edited = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([api.GET('/v1/stores/{store_id}/storefront/home', path), api.GET('/v1/stores/{store_id}/storefront/draft', path)]).then(([h, d]) => {
      if (!live) return;
      if (!h.data) {
        setError(problemMessage(h.error));
        return;
      }
      const draft = d.data?.home;
      const list = (draft?.sections ?? h.data.sections).map((s) => ({ ...s, id: s.id ?? newID() }));
      setPublished(h.data);
      setSections(list);
      setMeta({ title: draft?.title ?? h.data.title, description: draft?.description ?? h.data.description });
      setHasDraft(Boolean(draft));
      setSelected(list[0]?.id ?? '');
      const t = d.data?.theme;
      if (t) setTheme(t);
      else
        void api.GET('/v1/stores/{store_id}/storefront/theme', path).then(({ data }) => {
          if (live && data) setTheme(data.settings);
        });
    });
    void api.GET('/v1/stores/{store_id}/storefront/section-types', path).then(({ data }) => {
      if (live && data) setSchemas(Object.fromEntries(data.items.map((t) => [t.type, t.schema as Schema])));
    });
    void api.GET('/v1/stores/{store_id}', path).then(({ data }) => {
      if (live && data) setStore({ name: data.name, slug: data.slug });
    });
    void api.GET('/v1/stores/{store_id}/storefront/logo', path).then(({ data }) => {
      if (live) setLogo(data?.current?.urls?.small.webp);
    });
    void api.GET('/v1/stores/{store_id}/categories', path).then(({ data }) => {
      if (live && data) setCategories(data.items.map((c) => ({ id: c.id, name: c.name, slug: c.slug, top: !c.parent_id })));
    });
    void api.GET('/v1/stores/{store_id}/storefront/pages', path).then(({ data }) => {
      if (live && data) setPages(data.items.filter((p) => p.id !== undefined).map((p) => ({ id: p.id ?? '', title: p.title, slug: p.slug })));
    });
    void api.GET('/v1/stores/{store_id}/products', { params: { path: { store_id: storeId }, query: { status: 'active', limit: 200 } } }).then(({ data }) => {
      if (!live || !data) return;
      setProducts(
        data.items.map((p) => {
          const v = p.variants[0];
          return {
            id: p.id,
            slug: p.slug,
            title: p.title,
            price: v ? money(v.price_minor, v.currency) : '',
            image: p.images.find((i) => i.status === 'ready')?.urls?.thumb.webp,
          };
        }),
      );
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  const saveDraft = useCallback(
    async (next: Section[], m: { title: string; description: string }): Promise<boolean> => {
      clearTimeout(timer.current);
      setSave('saving');
      const { error: err } = await api.PUT('/v1/stores/{store_id}/storefront/draft/home', {
        params: { path: { store_id: storeId } },
        body: { title: m.title, description: m.description, sections: next },
      });
      if (err) {
        setSave('error');
        const fields = (err as { errors?: FieldError[] }).errors ?? [];
        setErrors(toFieldErrors(fields));
        setError(fields.length ? '' : problemMessage(err));
        return false;
      }
      edited.current = false;
      setErrors({});
      setError('');
      setHasDraft(true);
      setSave('saved');
      return true;
    },
    [storeId],
  );

  function change(next: Section[]) {
    setSections(next);
    edited.current = true;
    clearTimeout(timer.current);
    timer.current = setTimeout(() => void saveDraft(next, meta), saveDelay);
  }

  useEffect(() => () => {
    clearTimeout(timer.current);
  }, []);

  async function flush(): Promise<boolean> {
    if (!sections) return false;
    return edited.current ? saveDraft(sections, meta) : true;
  }

  async function publish() {
    if (!published || !(await flush())) return;
    setBusy(true);
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/storefront/draft/home/publish', {
      params: { path: { store_id: storeId } },
      body: { version: published.version },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setPublished(data);
    setSections(data.sections);
    setHasDraft(false);
    setSave('idle');
  }

  async function discard() {
    if (!published || !globalThis.confirm('تجاهل التغييرات والرجوع لآخر نسخة منشورة؟')) return;
    clearTimeout(timer.current);
    setBusy(true);
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/storefront/draft/home', { params: { path: { store_id: storeId } } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    edited.current = false;
    setSections(published.sections);
    setSelected(published.sections[0]?.id ?? '');
    setHasDraft(false);
    setErrors({});
    setSave('idle');
  }

  async function preview() {
    if (!store) return;
    const tab = globalThis.open('about:blank', '_blank');
    if (!(await flush())) {
      tab?.close();
      return;
    }
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/storefront/previews', { params: { path: { store_id: storeId } } });
    if (err) {
      tab?.close();
      setError(problemMessage(err));
      return;
    }
    const url = `${storefrontURL(store.slug)}/?preview=${encodeURIComponent(data.token)}`;
    if (tab) {
      tab.opener = null;
      tab.location.href = url;
    }
  }

  if (!sections || !published) {
    return (
      <Layout storeId={storeId}>
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const index = sections.findIndex((s) => s.id === selected);
  const current = sections[index];
  const dirty = hasDraft || save === 'saving';
  const grid = sections.find((s) => s.type === 'product_grid');
  const choices: Choices = {
    products: products.map((p) => ({ id: p.id, label: p.title, path: `/p/${p.slug}` })),
    categories: categories.map((c) => ({ id: c.id, label: c.name, path: `/c/${c.slug}` })),
    pages: pages.map((p) => ({ id: p.id, label: p.title, path: `/pages/${p.slug}` })),
    gridAnchor: grid?.id ? `#s-${grid.id}` : undefined,
  };

  const setSettings = (id: string, settings: Settings) => {
    change(sections.map((s) => (s.id === id ? { ...s, settings } : s)));
  };
  const move = (i: number, by: number) => {
    const next = [...sections];
    const [s] = next.splice(i, 1);
    if (!s) return;
    next.splice(i + by, 0, s);
    change(next);
  };
  const remove = (i: number) => {
    const s = sections[i];
    if (!s || !globalThis.confirm(`حذف قسم «${typeLabel(s.type)}» من الصفحة؟`)) return;
    const next = sections.filter((_, j) => j !== i);
    if (s.id === selected) setSelected(next[Math.min(i, next.length - 1)]?.id ?? '');
    change(next);
  };
  const add = (type: string) => {
    const s: Section = { id: newID(), type: type as Section['type'], settings: starter(type, schemas[type]) };
    setAdding(false);
    setSelected(s.id ?? '');
    change([...sections, s]);
  };
  const form = (s: Section, i: number) => {
    const schema = schemas[s.type];
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
      disabled={sections.length >= 30}
      onClick={() => {
        setAdding(true);
      }}
    >
      + أضف قسمًا
    </button>
  );

  return (
    <Layout storeId={storeId}>
      <div className="page-head design-head">
        <div>
          <div className="title-line">
            <h1>الصفحة الرئيسية</h1>
            {dirty && !narrow && <span className="status s-pending">تغييرات لم تُنشر</span>}
          </div>
          <p className="muted">{narrow ? 'اضغط قسمًا لتعديله.' : 'رتّب أقسام صفحتك الأولى. التغييرات مسودة حتى تضغط «نشر».'}</p>
          <p className="visually-hidden" role="status">
            {save === 'saving' ? 'جارٍ حفظ المسودة…' : save === 'saved' ? 'حُفظت المسودة' : save === 'error' ? 'لم تُحفظ المسودة: راجع الأقسام المعلّمة' : ''}
          </p>
        </div>
        <div className="actions">
          <button type="button" className="secondary" disabled={busy || !hasDraft} onClick={() => void discard()}>
            تجاهل التغييرات
          </button>
          <button type="button" className="secondary" disabled={busy || !store} onClick={() => void preview()}>
            معاينة في تبويب جديد
          </button>
          <button type="button" className="primary" disabled={busy || !hasDraft} onClick={() => void publish()}>
            نشر
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      {narrow ? (
        <div className="phone-sections">
          {sections.map((s, i) => {
            const open = s.id === selected;
            return (
              <details key={s.id} className={`sec-details${hasErrors(i) ? ' has-error' : ''}`} open={open}>
                <summary
                  onClick={(e) => {
                    e.preventDefault();
                    setSelected(open ? '' : (s.id ?? ''));
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
        </div>
      ) : (
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
                      setSelected(s.id ?? '');
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
              <HomePreview theme={theme} storeName={store?.name ?? ''} logo={logo} sections={sections} selected={selected} categories={categories.filter((c) => c.top)} products={products} phone={phone} />
            )}
            <p className="muted small center">القسم المختار محاط بإطار.</p>
          </section>
        </div>
      )}
      <div className="sticky-actions">
        <button type="button" className="secondary" disabled={busy || !store} onClick={() => void preview()}>
          معاينة
        </button>
        <button type="button" className="primary" disabled={busy || !hasDraft} onClick={() => void publish()}>
          نشر
        </button>
      </div>
      {adding && (
        <AddSectionDialog
          types={Object.keys(schemas)}
          onPick={add}
          onClose={() => {
            setAdding(false);
          }}
        />
      )}
    </Layout>
  );
}
