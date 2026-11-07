import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { SupportRequest } from './SupportRequest';

const signedOut = (): Session => ({ state: { kind: 'signed-out' }, reload: vi.fn(() => Promise.resolve()), signOut: vi.fn(() => Promise.resolve()) });
const gone = () => ({ status: 404, body: { title: 'Not Found', status: 404, code: 'link_not_found' } });
const open = {
  kind: 'mfa_reset',
  email: 'k•••m@example.com',
  member_name: 'كريم سامي',
  for_member: true,
  created_at: '2026-11-07T11:30:00Z',
  effective_at: '2099-11-08T11:30:00Z',
};

function renderAt(entry: string) {
  render(
    <SessionContext.Provider value={signedOut()}>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route path="/support-request" element={<SupportRequest />} />
          <Route path="/" element={<p>تسجيل الدخول</p>} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const sent = (calls: Call[], path: string) => calls.filter((c) => c.method === 'POST' && c.path === path).map((c) => c.body);

afterEach(() => {
  vi.restoreAllMocks();
});

describe('a support request link (C1)', () => {
  it('shows the request and cancels it without signing in', async () => {
    const calls = mockAPI({
      'POST /v1/support-requests/check': () => ({ status: 200, body: open }),
      'POST /v1/support-requests/cancel': () => ({ status: 204 }),
    });
    renderAt('/support-request#the-link');
    expect(await screen.findByRole('heading', { name: 'طلب إلغاء التحقق بخطوتين' })).toBeInTheDocument();
    expect(screen.getByText('k•••m@example.com')).toBeInTheDocument();
    expect(screen.getByText(/^يُنفَّذ$/).nextElementSibling).toHaveTextContent(/ساعة\)$/);
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'ألغِ الطلب، لم أطلبه' }));
    expect(await screen.findByText('ألغينا الطلب. التحقق بخطوتين باقٍ على حسابك كما هو.')).toBeInTheDocument();
    expect(sent(calls, '/v1/support-requests/cancel')).toEqual([{ token: 'the-link' }]);
    await user.click(screen.getByRole('link', { name: 'ادخل إلى حسابك' }));
    expect(screen.getByText('تسجيل الدخول')).toBeInTheDocument();
  });

  it('names the member on the store owner’s copy', async () => {
    mockAPI({
      'POST /v1/support-requests/check': () => ({ status: 200, body: { ...open, email: 'm•••a@example.com', for_member: false } }),
      'POST /v1/support-requests/cancel': () => ({ status: 204 }),
    });
    renderAt('/support-request#owner-link');
    expect(await screen.findByText('كريم سامي، عضو في متجرك')).toBeInTheDocument();
    expect(screen.queryByText('m•••a@example.com')).not.toBeInTheDocument();
    expect(screen.getByText('وصلك هذا لأنك صاحب المتجر. ألغِه إن لم يطلبه كريم سامي.')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'ألغِ الطلب' }));
    expect(await screen.findByText('ألغينا الطلب. التحقق بخطوتين باقٍ على حساب كريم سامي كما هو.')).toBeInTheDocument();
    expect(screen.getByText('إن لم يكن كريم سامي من تواصل مع الدعم، فنبّهه ليغيّر كلمة المرور.')).toBeInTheDocument();
  });

  it('says an old link’s request is gone', async () => {
    mockAPI({ 'POST /v1/support-requests/check': gone });
    renderAt('/support-request#old');
    expect(await screen.findByRole('heading', { name: 'الطلب لم يعد قائمًا' })).toBeInTheDocument();
  });

  it('says gone when the request closed before the click, and without a token', async () => {
    mockAPI({ 'POST /v1/support-requests/check': () => ({ status: 200, body: open }), 'POST /v1/support-requests/cancel': gone });
    renderAt('/support-request#late');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'ألغِ الطلب، لم أطلبه' }));
    expect(await screen.findByRole('heading', { name: 'الطلب لم يعد قائمًا' })).toBeInTheDocument();
  });

  it('needs a token', () => {
    mockAPI({});
    renderAt('/support-request');
    expect(screen.getByRole('heading', { name: 'الطلب لم يعد قائمًا' })).toBeInTheDocument();
  });
});
