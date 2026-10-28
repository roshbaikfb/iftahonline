type Status = 'draft' | 'published';

interface Props {
  status: Status | null; // null: not saved yet
  dirty: boolean;
  busy: boolean;
  viewURL?: string | undefined; // the page on the shop, once published
  onSave: (status: Status) => void;
  onDelete?: (() => void) | undefined;
}

// PageActions saves a landing page or a policy: as a draft only the
// merchant sees, or published for shoppers; a published page can be
// viewed on the shop or taken down.
export function PageActions({ status, dirty, busy, viewURL, onSave, onDelete }: Props) {
  const save = (s: Status) => () => {
    onSave(s);
  };
  return (
    <div className="actions">
      {onDelete && (
        <button type="button" className="link danger-link" disabled={busy} onClick={onDelete}>
          حذف الصفحة
        </button>
      )}
      {status === 'published' ? (
        <>
          {viewURL && (
            <a className="button secondary" href={viewURL} target="_blank" rel="noreferrer">
              عرض في المتجر
            </a>
          )}
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() => {
              if (globalThis.confirm('إلغاء النشر؟ تختفي الصفحة من المتجر وتبقى مسودة عندك.')) onSave('draft');
            }}
          >
            إلغاء النشر
          </button>
          <button type="button" className="primary" disabled={busy || !dirty} onClick={save('published')}>
            حفظ التغييرات
          </button>
        </>
      ) : (
        <>
          <button type="button" className="secondary" disabled={busy || (status !== null && !dirty)} onClick={save('draft')}>
            حفظ كمسودة
          </button>
          <button type="button" className="primary" disabled={busy} onClick={save('published')}>
            نشر
          </button>
        </>
      )}
    </div>
  );
}
