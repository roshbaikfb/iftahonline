import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { transferMessage } from '../lib/ownership';
import type { Role } from '../lib/settings';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Team } from './Team';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;
const owner = '01M5USER000000000000000001';
const karim = '01M5USER000000000000000002';

function session(role: Role, mfa = false): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: owner, email: 'mona@example.com', name: 'منى', email_verified: true, mfa_enabled: mfa, created_at: '' },
        memberships: [{ store_id: store, role }],
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const members = [
  { user_id: owner, name: 'منى', email: 'mona@example.com', role: 'owner', joined_at: '' },
  { user_id: karim, name: 'كريم سامي', email: 'karim@example.com', role: 'manager', joined_at: '' },
];

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}, items: unknown[] = members) {
  return mockAPI({
    [`GET ${base}`]: () => ({
      status: 200,
      body: {
        id: store,
        name: 'متجر منى',
        slug: 'mona',
        status: 'active',
        plan: 'free',
        currency: 'EGP',
        locale: 'ar',
        timezone: 'Africa/Cairo',
        created_at: '',
        updated_at: '',
      },
    }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/members`]: () => ({ status: 200, body: { items } }),
    [`GET ${base}/invitations`]: () => ({ status: 200, body: { items: [] } }),
    ...over,
  });
}

function renderTeam(s: Session) {
  render(
    <SessionContext.Provider value={s}>
      <MemoryRouter initialEntries={[`/stores/${store}/team`]}>
        <Routes>
          <Route path="/stores/:storeId/team" element={<Team />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('moving the store’s ownership (C2)', () => {
  it('words the refusals', () => {
    expect(transferMessage({ status: 422, errors: [{ field: 'password', code: 'wrong' }] })).toBe('كلمة المرور غير صحيحة.');
    expect(transferMessage({ status: 401 })).toMatch(/^كود التحقق غير صحيح/);
    expect(transferMessage({ status: 409, code: 'member_disabled' })).toBe('حساب هذا العضو موقوف، فلا تُنقل إليه الملكية.');
  });

  it('moves it with the password, and the page shows the new roles', async () => {
    const calls = routes({ [`POST ${base}/ownership`]: () => ({ status: 204 }) });
    const s = session('owner');
    renderTeam(s);
    const user = userEvent.setup();
    const section = await screen.findByRole('region', { name: 'ملكية المتجر' });
    await user.click(within(section).getByRole('button', { name: 'انقل الملكية' }));
    const dialog = screen.getByRole('dialog', { name: 'نقل ملكية المتجر' });
    expect(dialog).toHaveTextContent('يصبح كريم سامي صاحب المتجر');
    expect(within(dialog).queryByLabelText(/^كود التحقق بخطوتين/)).not.toBeInTheDocument();
    const go = within(dialog).getByRole('button', { name: 'انقل الملكية إلى كريم سامي' });
    expect(go).toBeDisabled();
    await user.type(within(dialog).getByLabelText('كلمة المرور'), 'my password');
    await user.click(go);
    expect(await screen.findByText('صار كريم سامي صاحب المتجر، وصرت أنت «مديرًا».')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ user_id: karim, password: 'my password' });
    await waitFor(() => {
      expect(s.reload).toHaveBeenCalled();
    });
  });

  it('asks for the code when two-step verification is on, and shows a refusal', async () => {
    const calls = routes({ [`POST ${base}/ownership`]: () => ({ status: 401, body: { title: 'Unauthorized', status: 401 } }) });
    renderTeam(session('owner', true));
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'انقل الملكية' }));
    const dialog = screen.getByRole('dialog', { name: 'نقل ملكية المتجر' });
    await user.type(within(dialog).getByLabelText('كلمة المرور'), 'my password');
    await user.type(within(dialog).getByLabelText(/^كود التحقق بخطوتين/), '123456');
    await user.click(within(dialog).getByRole('button', { name: 'انقل الملكية إلى كريم سامي' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('كود التحقق غير صحيح');
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ user_id: karim, password: 'my password', code: '123456' });
  });

  it('is the owner’s alone, and needs another member', async () => {
    routes();
    renderTeam(session('manager'));
    await screen.findByRole('heading', { name: 'الأعضاء' });
    expect(screen.queryByRole('region', { name: 'ملكية المتجر' })).not.toBeInTheDocument();
  });

  it('cannot move it without another member', async () => {
    routes({}, [members[0]]);
    renderTeam(session('owner'));
    const section = await screen.findByRole('region', { name: 'ملكية المتجر' });
    expect(within(section).getByRole('button', { name: 'انقل الملكية' })).toBeDisabled();
    expect(section).toHaveTextContent('ادعُ من ستنقل إليه الملكية أولًا');
  });
});
