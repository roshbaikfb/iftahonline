import type { ReactNode } from 'react';
import { NavLink } from 'react-router';

import { useAdmin } from './session';

const nav = [
  { to: '/', label: 'الحالة والمتاجر', end: true },
  { to: '/plans', label: 'الباقات والمميزات', end: false },
  { to: '/agencies', label: 'الوكالات', end: false },
  { to: '/audit', label: 'سجل الأدمن', end: false },
];

// ConsoleLayout is the console's frame (the approved mockup): a dark
// sidebar with the "أدمن" badge, so it is never mistaken for a store's
// dashboard.
export function ConsoleLayout({ children }: { children: ReactNode }) {
  const { state, signOut } = useAdmin();
  const admin = state.kind === 'signed-in' ? state.admin : null;
  return (
    <div className="console">
      <nav className="console-side" aria-label="قائمة الكونسول">
        <div className="console-brand">
          <strong>الكونسول</strong> <span className="admin-badge">أدمن</span>
        </div>
        {nav.map((n) => (
          <NavLink key={n.to} to={n.to} end={n.end} className="console-item">
            {n.label}
          </NavLink>
        ))}
        {admin && (
          <div className="console-who">
            <span>{admin.name}</span>
            <small dir="ltr">{admin.email}</small>
            <button type="button" className="link-button" onClick={() => void signOut()}>
              تسجيل الخروج
            </button>
          </div>
        )}
      </nav>
      <main className="console-main">{children}</main>
    </div>
  );
}
