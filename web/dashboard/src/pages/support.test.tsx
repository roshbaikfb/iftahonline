import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { Layout } from '../components/Layout';
import { codeOf } from '../lib/support';
import { SessionContext, type Me, type Session } from '../session';
import { mockAPI } from '../test/api';
import { Impersonate } from './Impersonate';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const code = 'A'.repeat(43);

function signedOut(): Session {
  return { state: { kind: 'signed-out' }, reload: vi.fn(() => Promise.resolve()), signOut: vi.fn(() => Promise.resolve()) };
}

function supportSession(readOnly: boolean): Session {
  const me: Me = {
    user: { id: 'U1', email: 'owner@example.com', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' },
    memberships: [{ store_id: store, role: 'owner' }],
    impersonation: { read_only: readOnly, expires_at: '2026-10-31T13:45:00Z' },
  };
  return { state: { kind: 'signed-in', me }, reload: vi.fn(() => Promise.resolve()), signOut: vi.fn(() => Promise.resolve()) };
}

function renderImpersonate(session: Session) {
  return render(
    <SessionContext.Provider value={session}>
      <MemoryRouter initialEntries={['/impersonate']}>
        <Routes>
          <Route path="/impersonate" element={<Impersonate />} />
          <Route path="/stores/:storeId/home" element={<h1>الرئيسية</h1>} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  window.history.replaceState(null, '', '/');
});

describe('support sessions', () => {
  it('reads the code from the fragment only', () => {
    expect(codeOf(`#code=${code}`)).toBe(code);
    expect(codeOf('#other=1')).toBe('');
    expect(codeOf('')).toBe('');
  });

  it('trades the code once, wipes it from the address, and reloads the session', async () => {
    window.history.replaceState(null, '', `/impersonate#code=${code}`);
    const calls = mockAPI({ 'POST /v1/auth/impersonation': () => ({ status: 204 }) });
    const session = signedOut();
    renderImpersonate(session);
    await waitFor(() => { expect(session.reload).toHaveBeenCalled(); });
    expect(calls.filter((c) => c.path === '/v1/auth/impersonation').map((c) => c.body)).toEqual([{ code }]);
    expect(window.location.hash).toBe('');
  });

  it('says when the link is spent or missing', async () => {
    window.history.replaceState(null, '', `/impersonate#code=${code}`);
    mockAPI({ 'POST /v1/auth/impersonation': () => ({ status: 401, body: { title: 'Unauthorized', status: 401 } }) });
    const { unmount } = renderImpersonate(signedOut());
    expect(await screen.findByRole('alert')).toHaveTextContent('انتهى رابط جلسة الدعم أو استُخدم من قبل');
    unmount();

    const calls = mockAPI({});
    renderImpersonate(signedOut());
    expect(screen.getByRole('alert')).toHaveTextContent('رابط جلسة الدعم ناقص.');
    expect(calls).toHaveLength(0);
  });

  it('shows the support bar on every screen and ends the session from it', async () => {
    mockAPI({
      [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo' } }),
      [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
      [`GET ${base}/messaging/notifications`]: () => ({ status: 200, body: { items: [], unread: 0 } }),
    });
    const session = supportSession(true);
    render(
      <SessionContext.Provider value={session}>
        <MemoryRouter>
          <Layout storeId={store}>
            <h1>الرئيسية</h1>
          </Layout>
        </MemoryRouter>
      </SessionContext.Provider>,
    );
    const bar = screen.getByText((_, el) => el?.classList.contains('support-bar') ?? false);
    expect(bar).toHaveTextContent('للقراءة فقط');
    expect(bar).toHaveTextContent('مسجّل في سجل المتجر');
    await userEvent.setup().click(screen.getByRole('button', { name: 'إنهاء الجلسة' }));
    expect(session.signOut).toHaveBeenCalled();
  });
});
