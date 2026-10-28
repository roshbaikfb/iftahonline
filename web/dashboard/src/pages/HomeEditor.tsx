import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams } from 'react-router';

import '../shop-fonts.css';
import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { SectionsEditor } from '../components/SectionsEditor';
import { useNarrow } from '../lib/narrow';
import { openPreview } from '../lib/preview';
import { problemMessage } from '../lib/problem';
import { fieldErrors as toFieldErrors, homeTypes, newID, type FieldError, type Section } from '../lib/sections';
import { useShopData } from '../lib/shopData';
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
  const data = useShopData(storeId);
  const [published, setPublished] = useState<Page | null>(null);
  const [sections, setSections] = useState<Section[] | null>(null);
  const [meta, setMeta] = useState({ title: '', description: '' });
  const [draftTheme, setDraftTheme] = useState<ThemeSettings | null>(null);
  const [hasDraft, setHasDraft] = useState(false);
  const [save, setSave] = useState<SaveState>('idle');
  const [error, setError] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState('');
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
      setDraftTheme(d.data?.theme ?? null);
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
    const { data: page, error: err } = await api.POST('/v1/stores/{store_id}/storefront/draft/home/publish', {
      params: { path: { store_id: storeId } },
      body: { version: published.version },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setPublished(page);
    setSections(page.sections);
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
    if (!data.store) return;
    const msg = await openPreview(storeId, data.store.slug, flush);
    if (msg) setError(msg);
  }

  if (!sections || !published) {
    return (
      <Layout storeId={storeId}>
        {error ? <p role="alert">{error}</p> : <p role="status">جارٍ التحميل…</p>}
      </Layout>
    );
  }

  const dirty = hasDraft || save === 'saving';

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
          <button type="button" className="secondary" disabled={busy || !data.store} onClick={() => void preview()}>
            معاينة في تبويب جديد
          </button>
          <button type="button" className="primary" disabled={busy || !hasDraft} onClick={() => void publish()}>
            نشر
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <SectionsEditor
        sections={sections}
        onChange={change}
        selected={selected}
        onSelect={setSelected}
        errors={errors}
        data={data}
        theme={draftTheme ?? data.theme}
        narrow={narrow}
        types={homeTypes}
      />
      <div className="sticky-actions">
        <button type="button" className="secondary" disabled={busy || !data.store} onClick={() => void preview()}>
          معاينة
        </button>
        <button type="button" className="primary" disabled={busy || !hasDraft} onClick={() => void publish()}>
          نشر
        </button>
      </div>
    </Layout>
  );
}
