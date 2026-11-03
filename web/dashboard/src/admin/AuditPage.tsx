import { useCallback, useEffect, useState, type SubmitEvent } from 'react';
import { Link, useSearchParams } from 'react-router';

import { api, type components } from '../api/client';
import type { paths } from '../api/schema';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { dayOf, type Entry, type Family } from './audit';
import { AuditRow } from './AuditRow';
import { ConsoleLayout } from './ConsoleLayout';
import type { Feature } from './plans';
import { useAdmin } from './session';

type AdminSummary = components['schemas']['AdminAdminSummary'];
type Query = NonNullable<paths['/admin/v1/audit-log']['get']['parameters']['query']>;

const families: { value: Family | ''; label: string }[] = [
  { value: '', label: 'الكل' },
  { value: 'store', label: 'المتاجر' },
  { value: 'billing', label: 'الفوترة والباقات' },
  { value: 'content', label: 'المحتوى' },
  { value: 'security', label: 'الدخول والأمان' },
];
const isFamily = (v: string | null): v is Family => v === 'store' || v === 'billing' || v === 'content' || v === 'security';

// queryOf reads the log's filters from the address.
function queryOf(params: URLSearchParams): Query {
  const q: Query = { limit: 50 };
  const family = params.get('family');
  if (isFamily(family)) q.family = family;
  for (const k of ['admin', 'store', 'from', 'to'] as const) {
    const v = params.get(k);
    if (v) q[k] = v;
  }
  return q;
}

// AuditPage is the admin log (the approved mockup), newest first and by
// day, narrowed by kind, admin, store and dates. The filters live in the
// address, so a store's page links to its own history.
export function AuditPage() {
  const { state } = useAdmin();
  const me = state.kind === 'signed-in' ? state.admin.id : '';
  const [params, setParams] = useSearchParams();
  const key = params.toString();
  const [items, setItems] = useState<Entry[]>([]);
  const [features, setFeatures] = useState<Feature[]>([]);
  const [admins, setAdmins] = useState<AdminSummary[]>([]);
  const [cursor, setCursor] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');

  const fetchPage = useCallback((after: string) => {
    const q = queryOf(new URLSearchParams(key));
    if (after) q.cursor = after;
    return api.GET('/admin/v1/audit-log', { params: { query: q } });
  }, [key]);

  const show = useCallback((r: Awaited<ReturnType<typeof fetchPage>>, more: boolean) => {
    setLoaded(true);
    if (r.error) {
      setError(problemMessage(r.error));
      return;
    }
    setError('');
    const page = r.data;
    setItems((prev) => (more ? [...prev, ...page.items] : page.items));
    setCursor(page.next_cursor ?? '');
  }, []);

  useEffect(() => {
    let live = true;
    void Promise.all([api.GET('/admin/v1/features'), api.GET('/admin/v1/admins')]).then(([f, a]) => {
      if (!live) return;
      if (f.data) setFeatures(f.data.items);
      if (a.data) setAdmins(a.data.items);
    });
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    let live = true;
    void fetchPage('').then((r) => {
      if (live) show(r, false);
    });
    return () => {
      live = false;
    };
  }, [fetchPage, show]);

  function set(change: Record<string, string>) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(change)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    setParams(p);
  }

  function narrow(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    set({ admin: field(form, 'admin'), from: field(form, 'from'), to: field(form, 'to') });
  }

  const family = params.get('family') ?? '';
  const store = params.get('store');
  const storeName = store ? items.find((e) => e.store_name)?.store_name : undefined;
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

      <section className="card audit-filters" aria-label="تصفية السجل">
        <div className="chips" role="group" aria-label="النوع">
          {families.map((f) => (
            <button
              key={f.value}
              type="button"
              className={family === f.value ? 'chip on' : 'chip'}
              aria-pressed={family === f.value}
              onClick={() => {
                set({ family: f.value });
              }}
            >
              {f.label}
            </button>
          ))}
        </div>
        <form className="audit-narrow" onSubmit={narrow} key={key}>
          <label>
            <span className="field-label">الأدمن</span>
            <select name="admin" defaultValue={params.get('admin') ?? ''}>
              <option value="">كل الأدمن</option>
              {admins.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.id === me ? `${a.name} (أنت)` : a.name}
                  {a.active ? '' : ' — موقوف'}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="field-label">من</span>
            <input name="from" type="date" defaultValue={params.get('from') ?? ''} />
          </label>
          <label>
            <span className="field-label">إلى</span>
            <input name="to" type="date" defaultValue={params.get('to') ?? ''} />
          </label>
          <button type="submit" className="secondary">
            تصفية
          </button>
          {key && (
            <button
              type="button"
              className="link-button"
              onClick={() => {
                setParams({});
              }}
            >
              مسح التصفية
            </button>
          )}
        </form>
        {store && (
          <p className="audit-store">
            ما فُعل بالمتجر <Link to={`/stores/${store}`}>{storeName ?? 'صفحة المتجر'}</Link> فقط ·{' '}
            <button
              type="button"
              className="link-button"
              onClick={() => {
                set({ store: '' });
              }}
            >
              كل المتاجر
            </button>
          </p>
        )}
      </section>

      {error && <p role="alert">{error}</p>}
      {loaded && items.length === 0 && !error && <p className="empty">{key ? 'لا شيء يطابق هذه التصفية.' : 'لا شيء في السجل بعد.'}</p>}
      {days.map((d) => (
        <section key={d.title} className="audit-day" aria-label={d.title}>
          <h2>{d.title}</h2>
          <ol className="audit-list">
            {d.rows.map((e) => (
              <AuditRow key={e.id} entry={e} me={me} features={features} />
            ))}
          </ol>
        </section>
      ))}
      {cursor && (
        <button type="button" className="secondary audit-more" onClick={() => void fetchPage(cursor).then((r) => { show(r, true); })}>
          عرض الأقدم
        </button>
      )}
    </ConsoleLayout>
  );
}
