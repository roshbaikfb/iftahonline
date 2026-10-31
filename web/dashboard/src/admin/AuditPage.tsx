import { useCallback, useEffect, useState } from 'react';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { ConsoleLayout } from './ConsoleLayout';
import { dateTimeAr, details } from './lib';

type Entry = components['schemas']['AdminAuditEntry'];

// fetchPage reads a page of the log after cursor.
function fetchPage(cursor: string) {
  return api.GET('/admin/v1/audit-log', { params: { query: cursor ? { limit: 50, cursor } : { limit: 50 } } });
}

// AuditPage is the admin log, newest first: who did what, to what, and
// why. It is append-only.
export function AuditPage() {
  const [items, setItems] = useState<Entry[]>([]);
  const [cursor, setCursor] = useState('');
  const [error, setError] = useState('');

  const show = useCallback((r: Awaited<ReturnType<typeof fetchPage>>, more: boolean) => {
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
    void fetchPage('').then((r) => {
      if (live) show(r, false);
    });
    return () => {
      live = false;
    };
  }, [show]);

  async function more() {
    show(await fetchPage(cursor), true);
  }

  return (
    <ConsoleLayout>
      <h1>سجل الأدمن</h1>
      {error && <p role="alert">{error}</p>}
      <section className="table-card" aria-label="سجل الأدمن">
        <div className="scroll">
          <table>
            <thead>
              <tr>
                <th scope="col">الوقت</th>
                <th scope="col">الإجراء</th>
                <th scope="col">على</th>
                <th scope="col">التفاصيل</th>
                <th scope="col">الشبكة</th>
              </tr>
            </thead>
            <tbody>
              {items.map((e) => (
                <tr key={e.id}>
                  <td className="muted">{dateTimeAr(e.occurred_at)}</td>
                  <td className="ltr">{e.action}</td>
                  <td className="ltr">
                    {e.target_type} {e.target_id}
                  </td>
                  <td className="wrap-cell">{details(e.metadata)}</td>
                  <td className="ltr muted">{e.ip_prefix}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {items.length === 0 && <p className="empty flat">لا شيء بعد.</p>}
        {cursor && (
          <p className="foot">
            <button type="button" className="button" onClick={() => void more()}>
              المزيد
            </button>
          </p>
        )}
      </section>
    </ConsoleLayout>
  );
}
