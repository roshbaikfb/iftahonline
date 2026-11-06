import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { EmailBanner } from '../components/EmailBanner';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { ForgotPassword } from './ForgotPassword';
import { Login } from './Login';
import { ResetPassword } from './ResetPassword';
import { VerifyEmail } from './VerifyEmail';

const email = 'mona@example.com';
const token = 'secret-link';

function signedIn(verified: boolean): Session {
  return {
    state: {
      kind: 'signed-in',
      me: { user: { id: 'u', email, name: 'منى', email_verified: verified, mfa_enabled: false, created_at: '' }, memberships: [] },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

const signedOut = (): Session => ({ state: { kind: 'signed-out' }, reload: vi.fn(() => Promise.resolve()), signOut: vi.fn(() => Promise.resolve()) });

function renderAt(session: Session, entry: string) {
  render(
    <SessionContext.Provider value={session}>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/forgot-password" element={<ForgotPassword />} />
          <Route path="/reset-password" element={<ResetPassword />} />
          <Route path="/verify-email" element={<VerifyEmail />} />
          <Route path="/banner" element={<EmailBanner />} />
          <Route path="/" element={<p>البداية</p>} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const sent = (calls: Call[], path: string) => calls.filter((c) => c.method === 'POST' && c.path === path).map((c) => c.body);
const gone = () => ({ status: 404, body: { title: 'Not Found', status: 404, code: 'link_not_found' } });

afterEach(() => {
  vi.restoreAllMocks();
});

describe('a forgotten password (D3)', () => {
  it('links to it from the login page', async () => {
    mockAPI({});
    renderAt(signedOut(), '/login');
    const user = userEvent.setup();
    await user.click(screen.getByRole('link', { name: 'نسيت كلمة المرور؟' }));
    expect(screen.getByRole('heading', { name: 'نسيت كلمة المرور' })).toBeInTheDocument();
  });

  it('sends a link and answers the same for any email', async () => {
    const calls = mockAPI({ 'POST /v1/auth/password/forgot': () => ({ status: 202 }) });
    renderAt(signedOut(), '/forgot-password');
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('البريد الإلكتروني'), ` ${email} `);
    await user.click(screen.getByRole('button', { name: 'أرسل الرابط' }));
    expect(await screen.findByRole('status')).toHaveTextContent('إذا كان لهذا البريد حساب، فقد أرسلنا إليه رابطًا');
    expect(sent(calls, '/v1/auth/password/forgot')).toEqual([{ email }]);
  });

  it('says to wait after too many requests', async () => {
    mockAPI({ 'POST /v1/auth/password/forgot': () => ({ status: 429, body: { title: 'Too Many Requests', status: 429, code: 'rate_limited' } }) });
    renderAt(signedOut(), '/forgot-password');
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('البريد الإلكتروني'), email);
    await user.click(screen.getByRole('button', { name: 'أرسل الرابط' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('طلبات كثيرة. انتظر دقيقة');
  });
});

describe('the reset page (D3)', () => {
  it('shows the account, checks the two passwords match, then signs in', async () => {
    const calls = mockAPI({
      'POST /v1/auth/password/reset/check': () => ({ status: 200, body: { email } }),
      'POST /v1/auth/password/reset': () => ({ status: 200, body: { user: { id: 'u', email } } }),
    });
    const session = signedOut();
    renderAt(session, `/reset-password#${token}`);
    const user = userEvent.setup();
    expect(await screen.findByText(email)).toBeInTheDocument();
    expect(sent(calls, '/v1/auth/password/reset/check')).toEqual([{ token }]);

    await user.type(screen.getByLabelText(/كلمة المرور الجديدة/), 'a brand new password');
    await user.type(screen.getByLabelText('أعد كتابتها'), 'a different password');
    await user.click(screen.getByRole('button', { name: 'احفظ وادخل' }));
    expect(screen.getByRole('alert')).toHaveTextContent('كلمتا المرور غير متطابقتين.');
    expect(sent(calls, '/v1/auth/password/reset')).toHaveLength(0);

    await user.clear(screen.getByLabelText('أعد كتابتها'));
    await user.type(screen.getByLabelText('أعد كتابتها'), 'a brand new password');
    await user.click(screen.getByRole('button', { name: 'احفظ وادخل' }));
    expect(await screen.findByText('البداية')).toBeInTheDocument();
    expect(sent(calls, '/v1/auth/password/reset')).toEqual([{ token, password: 'a brand new password', client: 'web' }]);
    expect(session.reload).toHaveBeenCalled();
  });

  it('says why a password is refused, in Arabic', async () => {
    mockAPI({
      'POST /v1/auth/password/reset/check': () => ({ status: 200, body: { email } }),
      'POST /v1/auth/password/reset': () => ({
        status: 422,
        body: {
          title: 'Unprocessable',
          status: 422,
          code: 'validation_failed',
          errors: [{ field: 'password', code: 'too_weak', message: 'password is too common' }],
        },
      }),
    });
    renderAt(signedOut(), `/reset-password#${token}`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/كلمة المرور الجديدة/), 'password12');
    await user.type(screen.getByLabelText('أعد كتابتها'), 'password12');
    await user.click(screen.getByRole('button', { name: 'احفظ وادخل' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('كلمة المرور سهلة التخمين');
  });

  it('asks for the second factor when the account has one', async () => {
    const calls = mockAPI({
      'POST /v1/auth/password/reset/check': () => ({ status: 200, body: { email } }),
      'POST /v1/auth/password/reset': () => ({ status: 202, body: { challenge_id: 'ch1', methods: ['totp'] } }),
      'POST /v1/auth/mfa': () => ({ status: 200, body: { user: { id: 'u', email } } }),
    });
    renderAt(signedOut(), `/reset-password#${token}`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/كلمة المرور الجديدة/), 'a brand new password');
    await user.type(screen.getByLabelText('أعد كتابتها'), 'a brand new password');
    await user.click(screen.getByRole('button', { name: 'احفظ وادخل' }));
    await user.type(await screen.findByLabelText('رمز التحقق'), '123456');
    await user.click(screen.getByRole('button', { name: 'تأكيد' }));
    expect(await screen.findByText('البداية')).toBeInTheDocument();
    expect(sent(calls, '/v1/auth/mfa')).toEqual([{ challenge_id: 'ch1', code: '123456', client: 'web' }]);
  });

  it('offers a new link when this one is used or expired', async () => {
    mockAPI({ 'POST /v1/auth/password/reset/check': gone });
    renderAt(signedOut(), `/reset-password#${token}`);
    expect(await screen.findByRole('alert')).toHaveTextContent('انتهت صلاحية هذا الرابط أو استُخدم.');
    expect(screen.getByRole('link', { name: 'اطلب رابطًا جديدًا' })).toHaveAttribute('href', '/forgot-password');
  });
});

describe('confirming the email (D3)', () => {
  it('confirms from the link and refreshes a signed-in session', async () => {
    const calls = mockAPI({ 'POST /v1/auth/email/verify': () => ({ status: 200, body: { email } }) });
    const session = signedIn(false);
    renderAt(session, `/verify-email#${token}`);
    expect(await screen.findByRole('heading', { name: 'تم تأكيد بريدك' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'إلى لوحة التحكم' })).toHaveAttribute('href', '/');
    expect(sent(calls, '/v1/auth/email/verify')).toEqual([{ token }]);
    expect(session.reload).toHaveBeenCalled();
  });

  it('works signed out, in another browser', async () => {
    mockAPI({ 'POST /v1/auth/email/verify': () => ({ status: 200, body: { email } }) });
    renderAt(signedOut(), `/verify-email#${token}`);
    expect(await screen.findByRole('link', { name: 'إلى تسجيل الدخول' })).toBeInTheDocument();
  });

  it('says an expired link is gone', async () => {
    mockAPI({ 'POST /v1/auth/email/verify': gone });
    renderAt(signedIn(false), `/verify-email#${token}`);
    expect(await screen.findByRole('heading', { name: 'انتهت صلاحية الرابط' })).toBeInTheDocument();
  });

  it('shows the banner until the email is confirmed, and resends', async () => {
    const calls = mockAPI({ 'POST /v1/me/email/verification': () => ({ status: 202 }) });
    renderAt(signedIn(false), '/banner');
    const user = userEvent.setup();
    expect(screen.getByRole('status')).toHaveTextContent(`أكّد بريدك ${email}`);
    await user.click(screen.getByRole('button', { name: 'أعد إرسال الرابط' }));
    expect(await screen.findByText('أرسلنا رابطًا جديدًا إلى بريدك.', { exact: false })).toBeInTheDocument();
    expect(sent(calls, '/v1/me/email/verification')).toHaveLength(1);
  });

  it('shows no banner once confirmed', () => {
    mockAPI({});
    renderAt(signedIn(true), '/banner');
    expect(screen.queryByRole('status')).toBeNull();
  });
});
