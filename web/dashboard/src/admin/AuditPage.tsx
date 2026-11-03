import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { dayOf, describe, type Entry, type Family } from './audit';
import { ConsoleLayout } from './ConsoleLayout';
import type { Feature } from './plans';
import { useAdmin } from './session';

// fetchPage reads a page of the log after cursor.
function fetchPage(cursor: string) {
  return api.GET('/admin/v1/audit-log', { params: { query: cursor ? { limit: 50, cursor } : { limit: 50 } } });
}

const glyph: Record<Family, string> = { store: 'م', billing: 'ب', content: 'ح', security: 'د' };
const time = (iso: string) => new Date(iso).toLocaleTimeString('ar-EG-u-nu-latn', { hour: 'numeric', minute: '2-digit' });

// AuditPage is the admin log (the approved mockup), newest first and by
// day: what was done in words, to what (a link where the console shows
// it), why, and who. It is append-only.
export function AuditPage() {
  const { state } = useAdmin();
  const me = state.kind === 'signed-in' ? state.admin.id : '';
  const [items, setItems] = useState<Entry[]>([]);
  const [features, setFeatures] = useState<Feature[]>([]);
  const [cursor, setCursor] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');

  const show = useCallback((r: Awaited<ReturnType<typeof fetchPage>>, more: boolean) => {
    setLoaded(true);
    if (r.error) {
      setError(problemMessage(r.error));
      return;
    }
    const page = r.data;
    setItems((prev) => (more ? [...prev, ...page.items] : page.items));
    setCursor(page.next_cursor ?? '');
  }, []);

  useEffect(() => {
    let live = true;
    void Promise.all([fetchPage(''), api.GET('/admin/v1/features')]).then(([r, f]) => {
      if (!live) return;
      show(r, false);
      if (f.data) setFeatures(f.data.items);
    });
    return () => {
      live = false;
    };
  }, [show]);

  async function more() {
    show(await fetchPage(cursor), true);
  }

  const now = new Date();
  const days: { title: string; rows: Entry[] }[] = [];
  for (const e of items) {
    const title = dayOf(e.occurred_at, now);
    const last = days.at(-1);
    if (last?.title === title) last.rows.push(e);
    else days.push({ title, rows: [e] });
  }

  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>سجل الأدمن</h1>
          <p className="muted">كل ما فعله فريق الكونسول، الأحدث أولًا، ومعه السبب. لا يُعدَّل ولا يُحذف.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {loaded && items.length === 0 && !error && <p className="empty">لا شيء في السجل بعد.</p>}
      {days.map((d) => (
        <section key={d.title} className="audit-day" aria-label={d.title}>
          <h2>{d.title}</h2>
          <ol className="audit-list">
            {d.rows.map((e) => {
              const x = describe(e, me, features);
              return (
                <li key={e.id}>
                  <span className={`audit-mark ${x.danger ? 'danger' : x.family}`} aria-hidden="true">
                    {x.danger ? '!' : glyph[x.family]}
                  </span>
                  <div className="audit-body">
                    <p className="audit-what">
                      <strong>{x.label}</strong>
                      {x.target && (
                        <>
                          {' — '}
                          {x.target.to ? <Link to={x.target.to}>{x.target.text}</Link> : <span>{x.target.text}</span>}
                        </>
                      )}
                    </p>
                    {x.reason && <p className="audit-reason">السبب: {x.reason}</p>}
                    {x.chips.length > 0 && (
                      <ul className="audit-chips">
                        {x.chips.map((c) => (
                          <li key={c}>{c}</li>
                        ))}
                      </ul>
                    )}
                  </div>
                  <div className="audit-meta">
                    <time dateTime={e.occurred_at}>{time(e.occurred_at)}</time>
                    <span>{x.who}</span>
                    {e.ip_prefix && <span className="ltr">{e.ip_prefix}</span>}
                  </div>
                </li>
              );
            })}
          </ol>
        </section>
      ))}
      {cursor && (
        <button type="button" className="secondary audit-more" onClick={() => void more()}>
          عرض الأقدم
        </button>
      )}
    </ConsoleLayout>
  );
}
