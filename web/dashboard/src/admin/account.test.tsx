import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const admin = testAdmin({ id: '01M5ADMIN000000000000000AA', email: 'sara@iftahonline.com', name: 'سارة علي' }, 'operations');
const sessions = [
  {
    id: '01M5SESS000000000000000AA',
    user_agent: 'Mozilla/5.0 (Windows NT 10.0) Chrome/130',
    ip_prefix: '10.0.0.0/24',
    created_at: '2026-11-07T07:00:00Z',
    last_seen_at: '2026-11-07T10:00:00Z',
    current: true,
  },
  {
    id: '01M5SESS000000000000000BB',
    user_agent: 'Mozilla/5.0 (iPhone) Safari/605',
    ip_prefix: '10.1.0.0/24',
    created_at: '2026-11-04T07:00:00Z',
    last_seen_at: '2026-11-06T20:00:00Z',
    current: false,
  },
];

function renderAccount(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  const calls = mockAPI({
    'GET /admin/v1/alerts': () => ({ status: 200, body: { open: [], resolved: [], email: true } }),
    'GET /admin/v1/me/sessions': () => ({ status: 200, body: { items: sessions } }),
    ...over,
  });
  const state: AdminState = { kind: 'signed-in', admin };
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={['/account']}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
  return { calls, value };
}

const posted = (calls: Call[], path: string) => calls.filter((c) => c.method !== 'GET' && c.path === path).map((c) => c.body);

describe('the admin’s own account (C3)', () => {
  it('shows the admin, the role and the devices, linked from the menu', async () => {
    renderAccount();
    expect(await screen.findByRole('heading', { name: 'حسابي' })).toBeInTheDocument();
    expect(screen.getByLabelText('الاسم')).toHaveValue('سارة علي');
    expect(screen.getByText('العمليات والدعم')).toBeInTheDocument();
    const devices = screen.getByRole('region', { name: 'الأجهزة المسجّل دخولها' });
    expect(await within(devices).findByText('هذا الجهاز')).toBeInTheDocument();
    expect(within(devices).getByRole('button', { name: 'اخرج من هذا الجهاز' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'حسابي' })).toHaveAttribute('href', '/account');
  });

  it('renames the admin and updates the session', async () => {
    const { calls, value } = renderAccount({ 'PATCH /admin/v1/me': () => ({ status: 200, body: { ...admin, name: 'سارة' } }) });
    const user = userEvent.setup();
    const field = await screen.findByLabelText('الاسم');
    await user.clear(field);
    await user.type(field, 'سارة');
    await user.click(screen.getByRole('button', { name: 'احفظ الاسم' }));
    expect(await screen.findByText('حُفظ الاسم.')).toBeInTheDocument();
    expect(posted(calls, '/admin/v1/me')).toEqual([{ name: 'سارة' }]);
    expect(value.signedIn).toHaveBeenCalledWith(expect.objectContaining({ name: 'سارة' }));
  });

  it('changes the password and says a wrong current one', async () => {
    let attempt = 0;
    const { calls } = renderAccount({
      'POST /admin/v1/me/password': () =>
        ++attempt === 1
          ? { status: 422, body: { title: 'Invalid', status: 422, errors: [{ field: 'current_password', code: 'wrong', message: '' }] } }
          : { status: 204 },
    });
    const user = userEvent.setup();
    const part = await screen.findByRole('region', { name: 'كلمة المرور' });
    await user.type(within(part).getByLabelText('الحالية'), 'old pass');
    await user.type(within(part).getByLabelText(/^الجديدة/), 'a brand new admin passphrase');
    await user.click(within(part).getByRole('button', { name: 'غيّر كلمة المرور' }));
    expect(await within(part).findByRole('alert')).toHaveTextContent('كلمة المرور الحالية غير صحيحة.');
    await user.click(within(part).getByRole('button', { name: 'غيّر كلمة المرور' }));
    expect(await within(part).findByText(/^تغيّرت كلمة المرور/)).toBeInTheDocument();
    expect(posted(calls, '/admin/v1/me/password')[1]).toEqual({ current_password: 'old pass', new_password: 'a brand new admin passphrase' });
  });

  it('moves two-step verification to a new phone in two steps', async () => {
    const { calls } = renderAccount({
      'POST /admin/v1/me/phone/start': () => ({ status: 200, body: { secret: 'ABCDEFGHABCDEFGH', uri: 'otpauth://x', qr_svg: '<svg/>' } }),
      'POST /admin/v1/me/phone/confirm': () => ({ status: 204 }),
    });
    const user = userEvent.setup();
    const part = await screen.findByRole('region', { name: 'جهاز التحقق بخطوتين' });
    await user.type(within(part).getByLabelText(/الموبايل القديم/), '482913');
    await user.click(within(part).getByRole('button', { name: 'تابع' }));
    expect(await within(part).findByRole('img', { name: 'رمز QR للموبايل الجديد' })).toBeInTheDocument();
    expect(part).toHaveTextContent('ABCD EFGH ABCD EFGH');
    await user.type(within(part).getByLabelText(/الموبايل الجديد/), '135790');
    await user.click(within(part).getByRole('button', { name: 'انقل إلى الموبايل الجديد' }));
    expect(await within(part).findByText(/^انتقل التحقق بخطوتين إلى الموبايل الجديد/)).toBeInTheDocument();
    expect(posted(calls, '/admin/v1/me/phone/start')).toEqual([{ code: '482913' }]);
    expect(posted(calls, '/admin/v1/me/phone/confirm')).toEqual([{ code: '135790' }]);
  });

  it('signs out of another device and of all the others', async () => {
    const { calls } = renderAccount({
      'POST /admin/v1/me/sessions/01M5SESS000000000000000BB/revoke': () => ({ status: 204 }),
      'POST /admin/v1/me/sessions/revoke-others': () => ({ status: 204 }),
    });
    const user = userEvent.setup();
    const devices = await screen.findByRole('region', { name: 'الأجهزة المسجّل دخولها' });
    await user.click(await within(devices).findByRole('button', { name: 'اخرج من هذا الجهاز' }));
    expect(await within(devices).findByText('خرجنا من الجهاز.')).toBeInTheDocument();
    await user.click(within(devices).getByRole('button', { name: 'اخرج من باقي الأجهزة' }));
    await waitFor(() => {
      expect(calls.some((c) => c.path === '/admin/v1/me/sessions/revoke-others')).toBe(true);
    });
  });
});
