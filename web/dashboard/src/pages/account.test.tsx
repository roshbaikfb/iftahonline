import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { deviceName, groupKey, maskPhone, phoneRegion } from '../lib/account';
import { SessionContext, type Me, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Account } from './Account';
import { Login } from './Login';

const store = '01M5STORE0000000000000000A';
const email = 'mona@example.com';

function session(user: Partial<Me['user']> = {}, extra: Partial<Me> = {}): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: 'u', email, name: 'منى أحمد', email_verified: true, mfa_enabled: false, created_at: '', ...user },
        memberships: [{ store_id: store, role: 'owner' }],
        ...extra,
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const setup = { secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/x', qr_svg: '<svg xmlns="http://www.w3.org/2000/svg"></svg>' };
const sessions = {
  items: [
    {
      id: 's1',
      user_agent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36',
      ip_prefix: '10.0.0.0/24',
      created_at: new Date().toISOString(),
      last_seen_at: new Date().toISOString(),
      expires_at: '',
      current: true,
    },
    {
      id: 's2',
      user_agent: 'Dart/3.9 (dart:io) Android',
      ip_prefix: '10.0.1.0/24',
      created_at: new Date().toISOString(),
      last_seen_at: new Date().toISOString(),
      expires_at: '',
      current: false,
    },
  ],
};

function api(extra: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /v1/me/sessions': () => ({ status: 200, body: sessions }),
    'POST /v1/me/mfa/totp': () => ({ status: 200, body: setup }),
    [`GET /v1/stores/${store}`]: () => ({ status: 200, body: { id: store, name: 'متجر الريم', slug: 'reem', country: 'SA' } }),
    ...extra,
  });
}

function renderAccount(s: Session) {
  render(
    <SessionContext.Provider value={s}>
      <MemoryRouter initialEntries={[`/stores/${store}/account`]}>
        <Routes>
          <Route path="/stores/:storeId/account" element={<Account />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const sent = (calls: Call[], method: string, path: string) => calls.filter((c) => c.method === method && c.path === path).map((c) => c.body);
const section = (name: string) => screen.getByRole('region', { name });

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the account page (D4)', () => {
  it('renames the user', async () => {
    const calls = api({ 'PATCH /v1/me': () => ({ status: 200, body: { id: 'u', email, name: 'منى' } }) });
    const s = session();
    renderAccount(s);
    const user = userEvent.setup();
    const part = section('بياناتك');
    expect(within(part).getByText(email)).toBeInTheDocument();
    expect(within(part).getByText('مؤكَّد')).toBeInTheDocument();
    await user.clear(within(part).getByLabelText('الاسم'));
    await user.type(within(part).getByLabelText('الاسم'), ' منى ');
    await user.click(within(part).getByRole('button', { name: 'احفظ الاسم' }));
    expect(await within(part).findByText('حُفظ الاسم.')).toBeInTheDocument();
    expect(sent(calls, 'PATCH', '/v1/me')).toEqual([{ name: 'منى' }]);
    expect(s.reload).toHaveBeenCalled();
  });

  it('changes the password and says a wrong current one in Arabic', async () => {
    let tries = 0;
    const calls = api({
      'POST /v1/me/password': () =>
        ++tries === 1
          ? {
              status: 422,
              body: { title: 'x', status: 422, code: 'validation_failed', errors: [{ field: 'current_password', code: 'wrong', message: 'x' }] },
            }
          : { status: 204 },
    });
    renderAccount(session());
    const user = userEvent.setup();
    const part = section('كلمة المرور');
    await user.type(within(part).getByLabelText('الحالية'), 'wrong one!');
    await user.type(within(part).getByLabelText(/الجديدة/), 'a brand new password');
    await user.click(within(part).getByRole('button', { name: 'غيّر كلمة المرور' }));
    expect(await within(part).findByRole('alert')).toHaveTextContent('كلمة المرور الحالية غير صحيحة.');

    await user.clear(within(part).getByLabelText('الحالية'));
    await user.type(within(part).getByLabelText('الحالية'), 'correct horse battery');
    await user.click(within(part).getByRole('button', { name: 'غيّر كلمة المرور' }));
    expect(await within(part).findByText('تغيّرت كلمة المرور، وخرجنا من كل الأجهزة الأخرى.')).toBeInTheDocument();
    expect(sent(calls, 'POST', '/v1/me/password')[1]).toEqual({ current_password: 'correct horse battery', new_password: 'a brand new password' });
    expect(within(part).getByLabelText('الحالية')).toHaveValue('');
  });

  it('confirms a phone number, read in the store country', async () => {
    const calls = api({
      'POST /v1/me/phone': () => ({ status: 202, body: { challenge_id: 'ch1', expires_at: '' } }),
      'POST /v1/me/phone/verify': () => ({ status: 204 }),
    });
    const s = session();
    renderAccount(s);
    const user = userEvent.setup();
    const part = section('رقم الموبايل');
    await waitFor(() => {
      expect(sent(calls, 'GET', `/v1/stores/${store}`).length).toBeGreaterThan(0);
    });
    await user.type(within(part).getByLabelText('الرقم'), '0501234567');
    await user.click(within(part).getByRole('button', { name: 'أرسل كود التأكيد' }));
    await user.type(await within(part).findByLabelText('الكود الذي وصلك'), '123456');
    await user.click(within(part).getByRole('button', { name: 'أكّد الرقم' }));
    expect(await within(part).findByText('تأكّد الرقم.')).toBeInTheDocument();
    expect(sent(calls, 'POST', '/v1/me/phone')).toEqual([{ phone: '0501234567', region: 'SA' }]);
    expect(sent(calls, 'POST', '/v1/me/phone/verify')).toEqual([{ challenge_id: 'ch1', code: '123456' }]);
    expect(s.reload).toHaveBeenCalled();
  });

  it('turns two-step verification on and shows the recovery codes once', async () => {
    const codes = ['k3j5-a9xq-2mzt-7c4v', 'p8wd-41nz-ke2q-x7rm'];
    const calls = api({ 'POST /v1/me/mfa/totp/confirm': () => ({ status: 200, body: { codes } }) });
    renderAccount(session());
    const user = userEvent.setup();
    const part = section('التحقق بخطوتين');
    expect(within(part).getByText('غير مفعّل')).toBeInTheDocument();
    expect(await within(part).findByRole('img', { name: 'رمز QR لإضافة الحساب إلى تطبيق التحقق' })).toBeInTheDocument();
    expect(within(part).getByText('JBSW Y3DP EHPK 3PXP')).toBeInTheDocument();
    await user.type(within(part).getByLabelText(/اكتب الكود/), '123456');
    await user.click(within(part).getByRole('button', { name: 'فعّل التحقق بخطوتين' }));
    expect(await within(part).findByText(codes[0] ?? '')).toBeInTheDocument();
    expect(within(part).getByRole('link', { name: 'نزّلها ملفًا' })).toHaveAttribute('download', 'iftahonline-recovery-codes.txt');
    expect(sent(calls, 'POST', '/v1/me/mfa/totp/confirm')).toEqual([{ code: '123456' }]);
  });

  it('turns it off with the password and a code', async () => {
    const calls = api({ 'POST /v1/me/mfa/totp/disable': () => ({ status: 204 }) });
    renderAccount(session({ mfa_enabled: true }));
    const user = userEvent.setup();
    const part = section('التحقق بخطوتين');
    expect(within(part).getByText('مفعّل')).toBeInTheDocument();
    await user.click(within(part).getByRole('button', { name: 'أوقف التحقق بخطوتين' }));
    await user.type(within(part).getByLabelText('كلمة المرور'), 'correct horse battery');
    await user.type(within(part).getByLabelText(/كود من التطبيق/), '123456');
    await user.click(within(part).getByRole('button', { name: 'أوقف التحقق بخطوتين' }));
    expect(await within(part).findByText('أُوقف التحقق بخطوتين.')).toBeInTheDocument();
    expect(sent(calls, 'POST', '/v1/me/mfa/totp/disable')).toEqual([{ password: 'correct horse battery', code: '123456' }]);
    // An enabled account is never given a new seed.
    expect(sent(calls, 'POST', '/v1/me/mfa/totp')).toHaveLength(0);
  });

  it('lists the devices and signs another one out', async () => {
    const calls = api({ 'DELETE /v1/me/sessions/s2': () => ({ status: 204 }) });
    renderAccount(session());
    const user = userEvent.setup();
    const part = section('الأجهزة المسجّل دخولها');
    expect(await within(part).findByText('Chrome على Windows')).toBeInTheDocument();
    expect(within(part).getByText('تطبيق التاجر على Android')).toBeInTheDocument();
    expect(within(part).getByText('هذا الجهاز')).toBeInTheDocument();
    await user.click(within(part).getByRole('button', { name: 'اخرج من هذا الجهاز' }));
    await waitFor(() => {
      expect(sent(calls, 'GET', '/v1/me/sessions')).toHaveLength(2);
    });
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/me/sessions/s2')).toBe(true);
  });

  it('asks a support session for no new seed', async () => {
    const calls = api();
    renderAccount(session({}, { impersonation: { read_only: true, expires_at: '' } }));
    expect(screen.getByText('هذه جلسة دعم: تُعرض بيانات الحساب ولا تتغير منها.')).toBeInTheDocument();
    await waitFor(() => {
      expect(sent(calls, 'GET', '/v1/me/sessions')).toHaveLength(1);
    });
    expect(sent(calls, 'POST', '/v1/me/mfa/totp')).toHaveLength(0);
  });
});

describe('signing in with a phone code (D4)', () => {
  function renderLogin(s: Session) {
    render(
      <SessionContext.Provider value={s}>
        <MemoryRouter>
          <Login />
        </MemoryRouter>
      </SessionContext.Provider>,
    );
  }
  const out = (): Session => ({ state: { kind: 'signed-out' }, reload: vi.fn(() => Promise.resolve()), signOut: vi.fn(() => Promise.resolve()) });

  it('sends a code, then signs in with it', async () => {
    const calls = mockAPI({
      'POST /v1/auth/otp/start': () => ({ status: 202, body: { challenge_id: 'ch1', expires_at: '' } }),
      'POST /v1/auth/otp/verify': () => ({ status: 200, body: { user: { id: 'u', email } } }),
    });
    const s = out();
    renderLogin(s);
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: 'كود على الموبايل' }));
    await user.type(screen.getByLabelText(/رقم الموبايل/), '01012345678');
    await user.click(screen.getByRole('button', { name: 'أرسل الكود' }));
    expect(await screen.findByText('010••••5678')).toBeInTheDocument();
    expect(screen.getByText(/يمكنك طلب كود جديد بعد/)).toBeInTheDocument();
    await user.type(screen.getByLabelText('الكود'), '123456');
    await user.click(screen.getByRole('button', { name: 'دخول' }));
    await waitFor(() => {
      expect(s.reload).toHaveBeenCalled();
    });
    expect((sent(calls, 'POST', '/v1/auth/otp/start')[0] as { phone: string }).phone).toBe('01012345678');
    expect(sent(calls, 'POST', '/v1/auth/otp/verify')).toEqual([{ challenge_id: 'ch1', code: '123456', client: 'web' }]);
  });

  it('asks for the second factor after the code', async () => {
    mockAPI({
      'POST /v1/auth/otp/start': () => ({ status: 202, body: { challenge_id: 'ch1', expires_at: '' } }),
      'POST /v1/auth/otp/verify': () => ({ status: 202, body: { mfa_required: true, challenge_id: 'mfa1', expires_at: '' } }),
    });
    renderLogin(out());
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: 'كود على الموبايل' }));
    await user.type(screen.getByLabelText(/رقم الموبايل/), '01012345678');
    await user.click(screen.getByRole('button', { name: 'أرسل الكود' }));
    await user.type(await screen.findByLabelText('الكود'), '123456');
    await user.click(screen.getByRole('button', { name: 'دخول' }));
    expect(await screen.findByRole('heading', { name: 'رمز التحقق' })).toBeInTheDocument();
  });

  it('says when codes cannot be sent', async () => {
    mockAPI({ 'POST /v1/auth/otp/start': () => ({ status: 503, body: { title: 'x', status: 503, code: 'unavailable' } }) });
    renderLogin(out());
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: 'كود على الموبايل' }));
    await user.type(screen.getByLabelText(/رقم الموبايل/), '01012345678');
    await user.click(screen.getByRole('button', { name: 'أرسل الكود' }));
    expect(await screen.findByText('إرسال الأكواد غير متاح الآن. جرّب بعد قليل.')).toHaveAttribute('role', 'alert');
  });
});

describe('account helpers (D4)', () => {
  it.each([
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36', 'Chrome على Windows'],
    ['Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/140.0 Safari/537.36 Edg/140.0', 'Edge على Windows'],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1', 'Safari على iPhone'],
    ['Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0', 'Firefox على Linux'],
    ['Dart/3.9 (dart:io) Android', 'تطبيق التاجر على Android'],
    ['curl/8.0', 'جهاز غير معروف'],
  ])('names %s', (ua, want) => {
    expect(deviceName(ua)).toBe(want);
  });

  it('reads numbers in the store country, else the time zone, else Egypt', () => {
    expect(phoneRegion('SA', 'Africa/Cairo')).toBe('SA');
    expect(phoneRegion('US', 'Asia/Dubai')).toBe('AE');
    expect(phoneRegion(undefined, 'Europe/London')).toBe('EG');
  });

  it('masks a number and groups a key', () => {
    expect(maskPhone('01012345678')).toBe('010••••5678');
    expect(maskPhone('+966501234567')).toBe('+96••••••4567');
    expect(groupKey('JBSWY3DPEHPK3PXP')).toBe('JBSW Y3DP EHPK 3PXP');
  });
});
