import { useEffect, useState, type ReactNode } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';

import { api } from '../api/client';
import { navGroups } from '../lib/nav';
import { storefrontURL } from '../lib/storefront';
import { useSession } from '../session';

// Layout is the signed-in frame (the approved mockup): the navy sidebar
// at the start, a top bar with the store, its storefront and the user's
// menu, and the page. On phones the sidebar opens as a drawer.
export function Layout({ storeId, children }: { storeId: string; children: ReactNode }) {
  const { state, signOut } = useSession();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [store, setStore] = useState<{ name: string; slug: string } | null>(null);
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(0);
  const me = state.kind === 'signed-in' ? state.me : null;
  const memberships = me?.memberships ?? [];

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setStore({ name: data.name, slug: data.slug });
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  // The orders waiting to be confirmed, read again on every screen.
  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/orders/counts', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setPending(data.pending);
    });
    return () => {
      live = false;
    };
  }, [storeId, pathname]);

  // The drawer closes on Escape, and when a link in it is followed.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  const name = store?.name ?? '';
  const userName = me?.user.name ?? '';
  return (
    <div className={open ? 'shell open' : 'shell'}>
      <aside className="side" id="side">
        <div className="side-brand">
          <span className="mark" aria-hidden="true">
            ا
          </span>
          <div>
            <strong>افتح أونلاين</strong>
            <small>لوحة التاجر</small>
          </div>
        </div>
        <nav aria-label="القائمة الرئيسية">
          {navGroups(storeId).map((g, i) => (
            <div className="group" key={g.title ?? i}>
              {g.title && <div className="group-title">{g.title}</div>}
              {g.items.map((it) =>
                it.to ? (
                  <Link
                    key={it.label}
                    to={it.to}
                    className="item"
                    aria-current={pathname.startsWith(it.to) ? 'page' : undefined}
                    onClick={() => {
                      setOpen(false);
                    }}
                  >
                    <span>{it.label}</span>
                    {it.pendingBadge && pending > 0 && (
                      <span className="count" aria-label={`${String(pending)} بانتظار التأكيد`}>
                        {pending}
                      </span>
                    )}
                  </Link>
                ) : (
                  <span key={it.label} className="item soon" aria-disabled="true">
                    <span>{it.label}</span>
                    <span className="soon-tag">قريبًا</span>
                  </span>
                ),
              )}
            </div>
          ))}
        </nav>
      </aside>
      {open && (
        <button
          type="button"
          className="backdrop"
          aria-label="إغلاق القائمة"
          onClick={() => {
            setOpen(false);
          }}
        />
      )}
      <div className="frame">
        <header className="top">
          <button
            type="button"
            className="secondary menu-toggle"
            aria-label="القائمة"
            aria-expanded={open}
            aria-controls="side"
            onClick={() => {
              setOpen(!open);
            }}
          >
            <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M4 7h16M4 12h16M4 17h16" />
            </svg>
          </button>
          {memberships.length > 1 ? (
            <label className="store-pick">
              <span>المتجر</span>
              <select value={storeId} onChange={(e) => void navigate(`/stores/${e.target.value}/orders`)}>
                {memberships.map((m) => (
                  <option key={m.store_id} value={m.store_id}>
                    {m.store_id === storeId && name ? name : m.store_id}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <span className="store-name">{name || 'متجري'}</span>
          )}
          <span className="grow" />
          {store && (
            <a className="button" href={storefrontURL(store.slug)} target="_blank" rel="noopener noreferrer">
              عرض المتجر
            </a>
          )}
          <details className="user">
            <summary aria-label={userName ? `حسابي: ${userName}` : 'حسابي'}>{initial(userName)}</summary>
            <div className="menu">
              <strong>{userName}</strong>
              <small dir="ltr">{me?.user.email}</small>
              <button type="button" className="secondary" onClick={() => void signOut()}>
                تسجيل الخروج
              </button>
            </div>
          </details>
        </header>
        <main>{children}</main>
      </div>
    </div>
  );
}

// initial is the first letter of a name, for the avatar (a whole
// grapheme, so no letter is cut).
function initial(name: string): string {
  const first = new Intl.Segmenter('ar', { granularity: 'grapheme' }).segment(name.trim())[Symbol.iterator]().next();
  return first.done ? '؟' : first.value.segment;
}
