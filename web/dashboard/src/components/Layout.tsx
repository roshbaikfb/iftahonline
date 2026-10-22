import { useEffect, useState, type ReactNode } from 'react';
import { Link, useNavigate } from 'react-router';

import { api } from '../api/client';
import { useSession } from '../session';

// Layout is the signed-in frame: the store's name, a store switcher when
// the merchant has several, and sign out.
export function Layout({ storeId, children }: { storeId: string; children: ReactNode }) {
  const { state, signOut } = useSession();
  const navigate = useNavigate();
  const [name, setName] = useState('');
  const memberships = state.kind === 'signed-in' ? state.me.memberships : [];

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setName(data.name);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  return (
    <div className="shell">
      <header className="bar">
        <Link to={`/stores/${storeId}/orders`} className="brand">
          {name || 'متجري'}
        </Link>
        <nav>
          <Link to={`/stores/${storeId}/orders`}>الطلبات</Link>
        </nav>
        {memberships.length > 1 && (
          <select
            aria-label="المتجر"
            value={storeId}
            onChange={(e) => void navigate(`/stores/${e.target.value}/orders`)}
          >
            {memberships.map((m) => (
              <option key={m.store_id} value={m.store_id}>
                {m.store_id === storeId && name ? name : m.store_id}
              </option>
            ))}
          </select>
        )}
        <button type="button" className="link" onClick={() => void signOut()}>
          تسجيل الخروج
        </button>
      </header>
      <main>{children}</main>
    </div>
  );
}
