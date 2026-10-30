import { useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router';

import { api } from '../api/client';
import { ago, canReadMessages, keepNotices, noticeText, noticeTone, orderCode, recentNotices, type Notice } from '../lib/messages';
import { useRole } from '../lib/settings';

const pageSize = 10;

const icons = {
  ok: <path d="M20 6 9 17l-5-5" />,
  no: <path d="M18 6 6 18M6 6l12 12" />,
  warn: (
    <>
      <path d="M12 9v4M12 17h.01" />
      <path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
    </>
  ),
};

// NotificationBell is the top bar's bell (6g, the approved mockup): the
// unread count, and the store's notices in a panel (a full page on a
// phone). Reading them is up to the merchant: a notice's link marks it
// read, and so does "mark all read".
export function NotificationBell({ storeId }: { storeId: string }) {
  const role = useRole(storeId);
  const { pathname } = useLocation();
  // A read kept from the last minute fills the bell at once (any screen).
  const kept = recentNotices(storeId);
  const [items, setItems] = useState<Notice[]>(kept?.items ?? []);
  const [unread, setUnread] = useState(kept?.unread ?? 0);
  const [next, setNext] = useState(kept?.next ?? '');
  // The panel is open on the page it was opened on: following a link
  // closes it.
  const [openOn, setOpenOn] = useState<string | null>(null);
  const open = openOn === pathname;
  const allowed = canReadMessages(role);

  useEffect(() => {
    if (!allowed || recentNotices(storeId)) return;
    let live = true;
    void api
      .GET('/v1/stores/{store_id}/messaging/notifications', { params: { path: { store_id: storeId }, query: { limit: pageSize } } })
      .then(({ data }) => {
        if (!data) return;
        keepNotices(storeId, { at: Date.now(), items: data.items, unread: data.unread, next: data.next_cursor ?? '' });
        if (!live) return;
        setItems(data.items);
        setUnread(data.unread);
        setNext(data.next_cursor ?? '');
      });
    return () => {
      live = false;
    };
  }, [storeId, pathname, allowed]);

  if (!allowed) return null;

  async function markRead(ids: string[]) {
    if (ids.length === 0) return;
    const { error } = await api.POST('/v1/stores/{store_id}/messaging/notifications/read', { params: { path: { store_id: storeId } }, body: { ids } });
    if (error) return;
    const nextItems = items.map((n) => (ids.includes(n.id) ? { ...n, read: true } : n));
    const nextUnread = Math.max(0, unread - ids.length);
    setItems(nextItems);
    setUnread(nextUnread);
    keepNotices(storeId, { at: recentNotices(storeId)?.at ?? Date.now(), items: nextItems, unread: nextUnread, next });
  }

  async function more() {
    const { data } = await api.GET('/v1/stores/{store_id}/messaging/notifications', {
      params: { path: { store_id: storeId }, query: { limit: pageSize, cursor: next } },
    });
    if (!data) return;
    const all = [...items, ...data.items];
    setItems(all);
    setNext(data.next_cursor ?? '');
    keepNotices(storeId, { at: recentNotices(storeId)?.at ?? Date.now(), items: all, unread, next: data.next_cursor ?? '' });
  }

  const label = unread > 0 ? `الإشعارات: ${String(unread)} غير مقروءة` : 'الإشعارات';
  return (
    <details
      className="bell"
      open={open}
      onToggle={(e) => {
        setOpenOn(e.currentTarget.open ? pathname : null);
      }}
    >
      <summary aria-label={label}>
        <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9" />
          <path d="M10.3 21a1.94 1.94 0 0 0 3.4 0" />
        </svg>
        {unread > 0 && (
          <span className="badge" aria-hidden="true">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </summary>
      <div className="notif" role="region" aria-label="الإشعارات">
        <div className="notif-head">
          <button
            type="button"
            className="back"
            aria-label="رجوع"
            onClick={() => {
              setOpenOn(null);
            }}
          >
            <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M9 18l6-6-6-6" />
            </svg>
          </button>
          <strong>الإشعارات</strong>
          {unread > 0 && (
            <button type="button" className="link small" onClick={() => void markRead(items.filter((n) => !n.read).map((n) => n.id))}>
              تحديد الكل كمقروء
            </button>
          )}
        </div>
        {items.length === 0 ? (
          <p className="empty flat">لا إشعارات بعد.</p>
        ) : (
          <ul>
            {items.map((n) => {
              const t = noticeText(n);
              const tone = noticeTone(n.kind);
              const ref = n.order_ref ?? (n.order_id ? orderCode(n.order_id) : '');
              const read = () => {
                if (!n.read) void markRead([n.id]);
              };
              return (
                <li key={n.id} className={n.read ? undefined : 'unread'}>
                  <span className={`ic ${tone}`} aria-hidden="true">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                      {icons[tone]}
                    </svg>
                  </span>
                  <div>
                    <span className="txt">
                      {t.before}
                      {n.order_id && (
                        <>
                          {' '}
                          <Link to={`/stores/${storeId}/orders/${n.order_id}`} dir="ltr" onClick={read}>
                            #{ref}
                          </Link>
                        </>
                      )}
                      {t.after}
                      {(n.kind === 'quota_nearly' || n.kind === 'quota_reached') && (
                        <>
                          {' '}
                          <Link to={`/stores/${storeId}/messages`} onClick={read}>
                            الرصيد
                          </Link>
                        </>
                      )}
                      {!n.read && <span className="visually-hidden"> (غير مقروء)</span>}
                    </span>
                    <small>
                      <time dateTime={n.created_at}>{ago(n.created_at)}</time>
                    </small>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
        {next && (
          <div className="notif-foot">
            <button type="button" className="secondary small" onClick={() => void more()}>
              المزيد
            </button>
          </div>
        )}
      </div>
    </details>
  );
}
