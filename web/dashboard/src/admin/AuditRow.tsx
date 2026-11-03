import { Link } from 'react-router';

import { describe, type Entry, type Family } from './audit';
import type { Feature } from './plans';

const glyph: Record<Family, string> = { store: 'م', billing: 'ب', content: 'ح', security: 'د' };
const time = (iso: string) => new Date(iso).toLocaleTimeString('ar-EG-u-nu-latn', { hour: 'numeric', minute: '2-digit' });

// AuditRow is one admin action as the log shows it (the approved
// mockup): what was done, to what, why, the details, when and by whom.
export function AuditRow({ entry: e, me, features, withDate = false }: { entry: Entry; me: string; features: Feature[]; withDate?: boolean }) {
  const x = describe(e, me, features);
  const when = withDate
    ? new Date(e.occurred_at).toLocaleString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', hour: 'numeric', minute: '2-digit' })
    : time(e.occurred_at);
  return (
    <li>
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
        <time dateTime={e.occurred_at}>{when}</time>
        <span>{x.who}</span>
        {e.ip_prefix && <span className="ltr">{e.ip_prefix}</span>}
      </div>
    </li>
  );
}
