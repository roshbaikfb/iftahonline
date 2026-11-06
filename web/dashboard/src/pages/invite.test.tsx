import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Invite } from './Invite';

const store = '01M5STORE0000000000000000A';
const token = `${store}.secret`;
const preview = {
  store_name: 'متجر الريم',
  inviter_name: 'منى أحمد',
  role: 'support',
  email: 'sara@example.com',
  expires_at: '2026-11-13T10:00:00Z',
  has_account: false,
};

function signedIn(email: string): Session {
  return {
    state: {
      kind: 'signed-in',
      me: { user: { id: 'u', email, name: 'سارة', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [] },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const signedOut = (): Session => ({ state: { kind: 'signed-out' }, reload: vi.fn(() => Promise.resolve()), signOut: vi.fn(() => Promise.resolve()) });

function renderAt(session: Session, hash = `#${token}`) {
  render(
    <SessionContext.Provider value={session}>
      <MemoryRouter initialEntries={[`/invite${hash}`]}>
        <Routes>
          <Route path="/invite" element={<Invite />} />
          <Route path="/stores/:storeId/home" element={<p>لوحة المتجر</p>} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const sent = (calls: Call[], path: string) => calls.find((c) => c.method === 'POST' && c.path === path)?.body;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the invitation page (D2)', () => {
  it('shows the invitation and creates the account while joining', async () => {
    const calls = mockAPI({
      'POST /v1/invitations/preview': () => ({ status: 200, body: preview }),
      'POST /v1/invitations/accept': () => ({ status: 200, body: { store_id: store, role: 'support', user: { id: 'u', email: preview.email } } }),
    });
    const session = signedOut();
    renderAt(session);
    const user = userEvent.setup();

    expect(await screen.findByRole('heading', { name: 'انضم إلى «متجر الريم»' })).toBeInTheDocument();
    expect(screen.getByText('دعاك منى أحمد للعمل معه على المتجر.')).toBeInTheDocument();
    expect(screen.getByText('خدمة العملاء')).toBeInTheDocument();
    expect(sent(calls, '/v1/invitations/preview')).toEqual({ token });
    // A new email: the new-account form first, its email fixed.
    expect(screen.getByRole('button', { name: 'ليس لدي حساب' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByDisplayValue(preview.email)).toHaveAttribute('readonly');

    await user.type(screen.getByLabelText('اسمك'), 'سارة محمود');
    await user.type(screen.getByLabelText(/كلمة المرور/), 'correct horse battery');
    await user.click(screen.getByRole('button', { name: 'أنشئ حسابي وانضم' }));

    expect(await screen.findByText('لوحة المتجر')).toBeInTheDocument();
    expect(sent(calls, '/v1/invitations/accept')).toEqual({ token, name: 'سارة محمود', password: 'correct horse battery', client: 'web' });
    expect(session.reload).toHaveBeenCalled();
  });

  it('sends an invitee with an account to sign in first', async () => {
    mockAPI({ 'POST /v1/invitations/preview': () => ({ status: 200, body: { ...preview, has_account: true } }) });
    renderAt(signedOut());
    const user = userEvent.setup();
    expect(await screen.findByRole('button', { name: 'لدي حساب' })).toHaveAttribute('aria-pressed', 'true');
    await user.click(screen.getByRole('button', { name: 'ليس لدي حساب' }));
    expect(screen.getByText('لهذا البريد حساب بالفعل. سجّل الدخول به لتنضم.')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'لدي حساب' }));
    await user.click(screen.getByRole('button', { name: 'سجّل الدخول' }));
    expect(screen.getByRole('heading', { name: 'تسجيل الدخول' })).toBeInTheDocument();
  });

  it('joins a signed-in invitee in one click', async () => {
    const calls = mockAPI({
      'POST /v1/invitations/preview': () => ({ status: 200, body: { ...preview, has_account: true } }),
      'POST /v1/invitations/accept': () => ({ status: 200, body: { store_id: store, role: 'support', user: { id: 'u', email: preview.email } } }),
    });
    renderAt(signedIn('Sara@Example.com'));
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'انضم إلى «متجر الريم»' }));
    expect(await screen.findByText('لوحة المتجر')).toBeInTheDocument();
    expect(sent(calls, '/v1/invitations/accept')).toEqual({ token, client: 'web' });
  });

  it('tells someone signed in with another email to sign out', async () => {
    const calls = mockAPI({ 'POST /v1/invitations/preview': () => ({ status: 200, body: preview }) });
    const session = signedIn('owner@example.com');
    renderAt(session);
    const user = userEvent.setup();
    expect(await screen.findByRole('alert')).toHaveTextContent('وأنت داخل بـ owner@example.com');
    expect(screen.queryByRole('button', { name: /انضم/ })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'تسجيل الخروج' }));
    expect(session.signOut).toHaveBeenCalled();
    expect(calls.some((c) => c.path === '/v1/invitations/accept')).toBe(false);
  });

  it('says a used, expired or missing link is no longer valid', async () => {
    mockAPI({ 'POST /v1/invitations/preview': () => ({ status: 404, body: { title: 'Not Found', status: 404, code: 'invitation_not_found' } }) });
    renderAt(signedOut());
    expect(await screen.findByRole('alert')).toHaveTextContent('انتهت صلاحية هذه الدعوة أو استُخدمت');
  });

  it('asks nothing of a link without a token', async () => {
    const calls = mockAPI({});
    renderAt(signedOut(), '');
    expect(await screen.findByRole('heading', { name: 'الدعوة غير صالحة' })).toBeInTheDocument();
    await waitFor(() => {
      expect(calls).toHaveLength(0);
    });
  });
});
