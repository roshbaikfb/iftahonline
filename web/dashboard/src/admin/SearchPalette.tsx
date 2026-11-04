import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react';
import { useNavigate } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { groupsOf, longEnough, type Result } from './search';

const failedLabel: Record<Result['failed'][number], string> = {
  stores: 'المتاجر',
  owners: 'صاحب المتجر',
  invoices: 'الفواتير',
  payments: 'حركات الدفع',
  agencies: 'الوكالات',
  admins: 'الأدمن',
};

// SearchPalette is the console's search (ADR-0079, the approved mockup):
// it asks as the admin types (from two characters, after a pause), lists
// what the role may read by kind, and opens a row with Enter or a click;
// Enter on an exact invoice number, ID or domain opens it at once.
export function SearchPalette({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const listId = useId();
  const input = useRef<HTMLInputElement>(null);
  const [q, setQ] = useState('');
  const [result, setResult] = useState<Result | null>(null);
  const [asked, setAsked] = useState('');
  const [error, setError] = useState('');
  const [active, setActive] = useState(0);

  useEffect(() => {
    input.current?.focus();
  }, []);

  useEffect(() => {
    const text = q.trim();
    if (!longEnough(text)) return;
    let live = true;
    const timer = window.setTimeout(() => {
      void api.GET('/admin/v1/search', { params: { query: { q: text } } }).then((r) => {
        if (!live) return;
        setAsked(text);
        setActive(0);
        if (r.data) {
          setResult(r.data);
          setError('');
        } else setError(problemMessage(r.error));
      });
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [q]);

  const short = !longEnough(q);
  const groups = !short && result ? groupsOf(result) : [];
  const hits = groups.flatMap((g) => g.hits);
  const open = (to: string) => {
    onClose();
    void navigate(to);
  };

  function onKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'ArrowDown' && hits.length > 0) {
      e.preventDefault();
      setActive((active + 1) % hits.length);
    } else if (e.key === 'ArrowUp' && hits.length > 0) {
      e.preventDefault();
      setActive((active - 1 + hits.length) % hits.length);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const jump = result?.jump;
      const hit = hits[active];
      if (jump && asked === q.trim() && active === 0) open(jump);
      else if (hit) open(hit.to);
    }
  }

  return (
    <div
      className="modal-backdrop search-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="search-palette" role="dialog" aria-modal="true" aria-label="البحث الشامل">
        <div className="search-input">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M11 18a7 7 0 1 0 0-14a7 7 0 0 0 0 14zM21 21l-4.3-4.3" />
          </svg>
          <input
            ref={input}
            type="search"
            role="combobox"
            aria-label="ابحث"
            aria-expanded={hits.length > 0}
            aria-controls={listId}
            aria-activedescendant={hits[active] ? `${listId}-${hits[active].key}` : undefined}
            aria-autocomplete="list"
            placeholder="متجر، دومين، فاتورة، مرجع دفعة، إيميل أو موبايل…"
            maxLength={100}
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
            }}
            onKeyDown={onKey}
          />
          <button type="button" className="search-close" onClick={onClose}>
            إلغاء
          </button>
        </div>
        <div className="search-results">
          {error && <p role="alert">{error}</p>}
          {short && <p className="search-hint">اكتب حرفين على الأقل.</p>}
          {!short && result && asked === q.trim() && hits.length === 0 && !error && (
            <div className="search-empty" role="status">
              <strong>لا نتائج لـ «{asked}»</strong>
              <span>أرقام الموبايل والإيميلات تُطابَق كاملة. جرّب اسم المتجر، أو رقم فاتورة، أو مرجع دفعة، أو دومين.</span>
            </div>
          )}
          <div role="listbox" id={listId} aria-label="النتائج">
            {groups.map((g) => (
              <div key={g.title} role="group" aria-label={g.title}>
                <div className="search-group">{g.title}</div>
                {g.hits.map((h) => {
                  const i = h.index;
                  return (
                    <div
                      key={h.key}
                      id={`${listId}-${h.key}`}
                      role="option"
                      aria-selected={i === active}
                      className={i === active ? 'search-hit on' : 'search-hit'}
                      onMouseEnter={() => {
                        setActive(i);
                      }}
                      onClick={() => {
                        open(h.to);
                      }}
                    >
                      <span className="search-mark" aria-hidden="true">
                        {h.mark}
                      </span>
                      <span className="search-text">
                        <strong className={h.ltr ? 'ltr' : undefined}>{h.title}</strong>
                        {h.sub && <small>{h.sub}</small>}
                      </span>
                      {h.pill && <span className={`status ${h.pill.cls}`}>{h.pill.text}</span>}
                    </div>
                  );
                })}
              </div>
            ))}
          </div>
          {result && result.owner_stores.length > 0 && (
            <p className="search-note">البحث برقم أو إيميل يُسجَّل في سجل الأدمن مخفيًا جزئيًا. الرقم يُطابَق كاملًا فقط.</p>
          )}
          {result && result.failed.length > 0 && (
            <p className="search-note">تعذّرت قراءة: {result.failed.map((f) => failedLabel[f]).join('، ')}. جرّب بعد قليل.</p>
          )}
        </div>
        <div className="search-foot" aria-hidden="true">
          <span>↑↓ للتنقل</span>
          <span>Enter للفتح</span>
          <span>Esc للإغلاق</span>
          <span className="search-foot-tip">رقم فاتورة كامل أو دومين يفتح مباشرة</span>
        </div>
      </div>
    </div>
  );
}
