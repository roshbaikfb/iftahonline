import { useEffect, useState, type ReactNode } from 'react';
import { Link, NavLink } from 'react-router';

import { useOpenAlerts } from './alerts';
import { SearchPalette } from './SearchPalette';
import { useAdmin, useCan, type Permission } from './session';

// icons are stroke paths on a 24px grid, one per screen.
const icons = {
  home: 'M3 11l9-8 9 8M5 10v10h14V10',
  alerts: 'M6 8a6 6 0 1 1 12 0c0 7 3 9 3 9H3s3-2 3-9M10 21a2 2 0 0 0 4 0',
  stores: 'M3 9l1.5-5h15L21 9M4 9v11h16V9M3 9h18M9 20v-6h6v6',
  billing: 'M6 3h12v18l-3-2-3 2-3-2-3 2zM9 8h6M9 12h6M9 16h3',
  growth: 'M3 20h18M6 16l4-5 3 3 5-7',
  plans: 'M12 3l9 5-9 5-9-5 9-5zM3 13l9 5 9-5',
  founding: 'M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z',
  agencies: 'M6 8a3 3 0 1 0 6 0a3 3 0 1 0-6 0M3 20c0-3 3-5 6-5s6 2 6 5M16 4a3 3 0 0 1 0 6M21 20c0-2.5-1.5-4.2-3.5-4.8',
  site: 'M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0M3 12h18M12 3a14 14 0 0 1 0 18a14 14 0 0 1 0-18',
  guides: 'M4 19V5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2zM8 7h7',
  ads: 'M3 10v4h4l6 4V6l-6 4zM17 9a4 4 0 0 1 0 6',
  audit: 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6zM9 12l2 2 4-4',
  team: 'M9 7a3 3 0 1 0 6 0a3 3 0 1 0-6 0M4 20c0-3.3 3.6-6 8-6s8 2.7 8 6',
};

const groups: { title: string; items: { to: string; label: string; icon: keyof typeof icons; end?: boolean; need?: Permission }[] }[] = [
  {
    title: 'المنصة',
    items: [
      { to: '/', label: 'الرئيسية', icon: 'home', end: true },
      { to: '/alerts', label: 'التنبيهات', icon: 'alerts' },
      { to: '/stores', label: 'المتاجر', icon: 'stores', need: 'stores.read' },
    ],
  },
  {
    title: 'الإيراد',
    items: [
      { to: '/billing', label: 'الفواتير والمدفوعات', icon: 'billing', need: 'billing.read' },
      { to: '/growth', label: 'النمو', icon: 'growth', need: 'billing.read' },
      { to: '/plans', label: 'الباقات والمميزات', icon: 'plans', need: 'catalog.read' },
      { to: '/founding', label: 'التجار المؤسسون', icon: 'founding', need: 'catalog.read' },
      { to: '/agencies', label: 'الوكالات', icon: 'agencies', need: 'catalog.read' },
    ],
  },
  {
    title: 'المحتوى',
    items: [
      { to: '/site', label: 'الموقع التعريفي', icon: 'site', need: 'content.read' },
      { to: '/guides', label: 'الأدلة', icon: 'guides', need: 'content.read' },
      { to: '/ads', label: 'الإعلانات', icon: 'ads', need: 'content.read' },
    ],
  },
  {
    title: 'الأمان',
    items: [
      { to: '/team', label: 'الفريق', icon: 'team', need: 'team.manage' },
      { to: '/audit', label: 'سجل الأدمن', icon: 'audit', need: 'audit.read' },
    ],
  },
];

function Icon({ d, size = 18 }: { d: string; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={d} />
    </svg>
  );
}

// ConsoleLayout is the console's frame (the approved mockup): a dark
// sidebar in titled groups with the "أدمن" badge, so it is never mistaken
// for a store's dashboard. On phones a top bar opens it as a drawer.
export function ConsoleLayout({ children }: { children: ReactNode }) {
  const { state, signOut } = useAdmin();
  const can = useCan();
  // Each admin sees only the sections the role opens (ADR-0077).
  const shown = groups.map((g) => ({ ...g, items: g.items.filter((n) => !n.need || can(n.need)) })).filter((g) => g.items.length > 0);
  const admin = state.kind === 'signed-in' ? state.admin : null;
  const [open, setOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  // The bell's count: the open alerts of the admin's role (ADR-0080).
  const alerts = useOpenAlerts();
  const alertsLabel = alerts > 0 ? `التنبيهات، ${String(alerts)} مفتوحة` : 'التنبيهات';

  // Ctrl+K (⌘K) or "/" opens the search anywhere in the console, unless
  // the admin is typing in a field (ADR-0079).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && (e.target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName));
      if ((e.key.toLowerCase() === 'k' && (e.ctrlKey || e.metaKey)) || (e.key === '/' && !typing && !e.ctrlKey && !e.metaKey)) {
        e.preventDefault();
        setOpen(false);
        setSearching(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
    };
  }, []);
  const search = () => {
    setOpen(false);
    setSearching(true);
  };

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

  const close = () => {
    setOpen(false);
  };
  const brand = (
    <>
      <strong>الكونسول</strong> <span className="admin-badge">أدمن</span>
    </>
  );
  return (
    <div className={open ? 'console open' : 'console'}>
      <header className="console-top">
        <span className="console-brand">{brand}</span>
        <Link to="/alerts" className="console-toggle console-bell" aria-label={alertsLabel}>
          <Icon d={icons.alerts} size={20} />
          {alerts > 0 && (
            <span className="alert-count" aria-hidden="true">
              {alerts}
            </span>
          )}
        </Link>
        <button type="button" className="console-toggle" aria-label="ابحث في كل شيء" onClick={search}>
          <Icon d="M11 18a7 7 0 1 0 0-14a7 7 0 0 0 0 14zM21 21l-4.3-4.3" size={20} />
        </button>
        <button
          type="button"
          className="console-toggle"
          aria-label="القائمة"
          aria-expanded={open}
          aria-controls="console-side"
          onClick={() => {
            setOpen(!open);
          }}
        >
          <Icon d="M4 6h16M4 12h16M4 18h16" size={20} />
        </button>
      </header>
      <nav className="console-side" id="console-side" aria-label="قائمة الكونسول">
        <div className="console-brand">
          <span className="console-mark" aria-hidden="true">
            ا
          </span>
          {brand}
          <button type="button" className="console-close" aria-label="إغلاق القائمة" onClick={close}>
            <Icon d="M6 6l12 12M18 6L6 18" size={20} />
          </button>
        </div>
        <button type="button" className="console-search" onClick={search}>
          <Icon d="M11 18a7 7 0 1 0 0-14a7 7 0 0 0 0 14zM21 21l-4.3-4.3" size={16} />
          <span>ابحث في كل شيء</span>
          <kbd dir="ltr">Ctrl K</kbd>
        </button>
        {shown.map((g) => (
          <div key={g.title} className="console-group">
            <div className="console-group-title">{g.title}</div>
            {g.items.map((n) => (
              <NavLink key={n.to} to={n.to} end={n.end ?? false} className="console-item" onClick={close}>
                <Icon d={icons[n.icon]} />
                {n.label}
                {n.icon === 'alerts' && alerts > 0 && (
                  <span className="alert-count" aria-label={`${String(alerts)} مفتوحة`}>
                    {alerts}
                  </span>
                )}
              </NavLink>
            ))}
          </div>
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
      {open && <button type="button" className="console-backdrop" aria-hidden="true" tabIndex={-1} onClick={close} />}
      <main className="console-main">{children}</main>
      {searching && (
        <SearchPalette
          onClose={() => {
            setSearching(false);
          }}
        />
      )}
    </div>
  );
}
