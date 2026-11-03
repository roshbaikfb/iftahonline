import type { ReactNode } from 'react';

import { ConsoleLayout } from './ConsoleLayout';
import { useCan, type Permission } from './session';

// Need shows a page only to a role that may read it (ADR-0077).
export function Need({ p, children }: { p: Permission; children: ReactNode }) {
  const can = useCan();
  if (can(p)) return children;
  return (
    <ConsoleLayout>
      <div className="page-head">
        <h1>لا تملك صلاحية هذه الصفحة</h1>
      </div>
      <p className="muted">دورك لا يشمل هذا القسم. إن احتجته فاطلبه من مالك الكونسول.</p>
    </ConsoleLayout>
  );
}

// Editable lets a role that may change what it holds use its forms; for
// any other role they show as they are, disabled, under a note.
export function Editable({ p, children }: { p: Permission; children: ReactNode }) {
  const can = useCan();
  if (can(p)) return children;
  return (
    <>
      <p className="notice" role="note">
        عرض فقط: دورك لا يسمح بالتعديل هنا.
      </p>
      <fieldset className="read-only" disabled>
        {children}
      </fieldset>
    </>
  );
}
