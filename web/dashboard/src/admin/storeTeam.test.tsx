import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { hoursUntil } from '../lib/supportRequests';
import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { AdminSessionContext, type Admin, type AdminSession } from './session';
import { StoreTeam } from './StoreTeam';
import { memberRole, requester, seatsLine, type StoreMember, type SupportRequest } from './storeTeam';

const store = '01M5STORE000000000000000AA';
const base = `/admin/v1/stores/${store}/team`;
const mona: StoreMember = {
  user_id: '01M5USER0000000000000000AA',
  name: 'منى أحمد',
  email: 'mona@example.com',
  phone: '+9665•••••412',
  role: 'owner',
  mfa_enabled: true,
  disabled: false,
  joined_at: '2026-10-01T10:00:00Z',
  last_seen_at: '2026-11-02T10:00:00Z',
};
const karim: StoreMember = {
  user_id: '01M5USER0000000000000000BB',
  name: 'كريم سامي',
  email: 'karim@example.com',
  role: 'manager',
  mfa_enabled: true,
  disabled: false,
  joined_at: '2026-10-02T10:00:00Z',
};
const noura: StoreMember = {
  user_id: '01M5USER0000000000000000CC',
  name: 'نورة خالد',
  role: 'support',
  mfa_enabled: false,
  disabled: true,
  joined_at: '2026-10-03T10:00:00Z',
};
const waitingReq: SupportRequest = {
  id: '01M5REQ00000000000000000AA',
  kind: 'mfa_reset',
  user_id: mona.user_id,
  requested_by: '01M5ADMIN000000000000000BB',
  requested_by_name: 'سارة',
  requested_by_role: 'operations',
  reason: 'تذكرة 4821',
  created_at: '2026-11-07T12:30:00Z',
  effective_at: '2026-11-08T12:30:00Z',
  state: 'open',
};

function team(over: Partial<{ members: StoreMember[]; requests: SupportRequest[] }> = {}) {
  return {
    members: over.members ?? [mona, karim, noura],
    invites: [{ email: 'ship@example.com', role: 'shipping' as const, expires_at: '2099-01-01T00:00:00Z' }],
    requests: over.requests ?? [waitingReq],
  };
}

function renderTeam(admin: Admin = testAdmin(), seatLimit: number | null = 5) {
  const value: AdminSession = { state: { kind: 'signed-in', admin }, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  const changed = vi.fn();
  render(
    <AdminSessionContext.Provider value={value}>
      <StoreTeam storeId={store} seatLimit={seatLimit} onChanged={changed} />
    </AdminSessionContext.Provider>,
  );
  return changed;
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({ [`GET ${base}`]: () => ({ status: 200, body: team() }), ...over });
}

const rowOf = async (name: string) => {
  const rows = await screen.findAllByRole('row');
  const row = rows.find((r) => r.textContent.includes(name));
  if (!row) throw new Error(`no row for ${name}`);
  return row;
};

describe('store team helpers', () => {
  it('words roles, seats, requesters and the time left', () => {
    expect(memberRole('owner')).toBe('صاحب المتجر');
    expect(memberRole('support')).toBe('خدمة العملاء');
    expect(seatsLine(team(), 5)).toBe('4 مقاعد من 5 في الباقة.');
    expect(seatsLine(team(), null)).toBe('4 مقاعد، والباقة بلا حد.');
    expect(seatsLine(team({ members: [mona], requests: [] }), 1)).toBe('مقعدان من 1 في الباقة.');
    expect(seatsLine({ ...team({ members: [mona] }), invites: [] }, 1)).toBe('مقعد واحد من 1 في الباقة.');
    expect(requester(waitingReq)).toBe('سارة (العمليات والدعم)');
    expect(requester({ ...waitingReq, requested_by_name: '' })).toBe('أدمن سابق');
    const now = new Date('2026-11-07T13:00:00Z');
    expect(hoursUntil('2026-11-08T12:30:00Z', now)).toBe('بعد 23 ساعة');
    expect(hoursUntil('2026-11-07T15:30:00Z', now)).toBe('بعد ساعتين');
    expect(hoursUntil('2026-11-07T18:30:00Z', now)).toBe('بعد 5 ساعات');
    expect(hoursUntil('2026-11-07T14:10:00Z', now)).toBe('بعد ساعة');
    expect(hoursUntil('2026-11-07T13:20:00Z', now)).toBe('بعد أقل من ساعة');
  });
});

describe('store team card', () => {
  it('lists members, the invitation and the waiting request', async () => {
    routes();
    renderTeam();
    const card = await screen.findByRole('region', { name: 'فريق المتجر' });
    expect(card).toHaveTextContent('4 مقاعد من 5 في الباقة.');
    const banner = within(card)
      .getByText(/طلب إلغاء التحقق بخطوتين لـمنى أحمد/)
      .closest('[role="status"]');
    expect(banner).toHaveTextContent('طلبه سارة (العمليات والدعم) · تذكرة 4821');
    const monaRow = await rowOf('منى أحمد');
    expect(monaRow).toHaveTextContent('صاحب المتجر');
    expect(monaRow).toHaveTextContent('إلغاء معلّق');
    expect(monaRow).toHaveTextContent('+9665•••••412');
    expect(await rowOf('نورة خالد')).toHaveTextContent('موقوف من المنصة');
    expect(await rowOf('دعوة معلّقة')).toHaveTextContent('ship@example.com');
  });

  it('asks for the two-step removal only after the three checks and a reason', async () => {
    const calls = routes({
      [`POST ${base}/${karim.user_id}/mfa-reset`]: () => ({ status: 202, body: { ...waitingReq, user_id: karim.user_id } }),
    });
    const changed = renderTeam();
    const user = userEvent.setup();
    await user.click(within(await rowOf('كريم سامي')).getByRole('button', { name: 'إجراءات على كريم سامي' }));
    await user.click(screen.getByRole('menuitem', { name: 'اطلب إلغاء التحقق بخطوتين' }));
    const dialog = screen.getByRole('dialog', { name: 'إلغاء التحقق بخطوتين لـكريم سامي' });
    const send = within(dialog).getByRole('button', { name: 'أرسل الطلب' });
    expect(send).toBeDisabled();
    for (const box of within(dialog).getAllByRole('checkbox')) await user.click(box);
    expect(send).toBeDisabled();
    await user.type(within(dialog).getByLabelText('السبب'), 'تذكرة 4900');
    expect(send).toBeEnabled();
    await user.click(send);
    await waitFor(() => {
      expect(changed).toHaveBeenCalled();
    });
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ reason: 'تذكرة 4900' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('offers no two-step request when one waits, and warns about other stores before disabling', async () => {
    const calls = routes({
      [`GET ${base}/${mona.user_id}/stores`]: () => ({ status: 200, body: { items: [{ store_id: 'X', name: 'بيت منى', role: 'manager' }] } }),
      [`POST ${base}/${mona.user_id}/disable`]: () => ({ status: 204 }),
    });
    renderTeam();
    const user = userEvent.setup();
    await user.click(within(await rowOf('منى أحمد')).getByRole('button', { name: 'إجراءات على منى أحمد' }));
    expect(screen.queryByRole('menuitem', { name: 'اطلب إلغاء التحقق بخطوتين' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('menuitem', { name: 'أوقف الحساب' }));
    const dialog = screen.getByRole('dialog', { name: 'إيقاف حساب منى أحمد' });
    expect(await within(dialog).findByText('الحساب عضو في متجر آخر')).toBeInTheDocument();
    expect(dialog).toHaveTextContent('بيت منى (مدير)');
    expect(dialog).toHaveTextContent('الإيقاف يشمله أيضًا.');
    await user.type(within(dialog).getByLabelText('السبب'), 'حساب مسروق');
    await user.click(within(dialog).getByRole('button', { name: 'أوقف الحساب' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'POST' && c.path.endsWith('/disable'))).toBe(true);
    });
    expect(await screen.findByText('أُوقف حساب منى أحمد وخرج من كل أجهزته.')).toBeInTheDocument();
  });

  it('enables a disabled account and cancels a waiting request', async () => {
    const calls = routes({
      [`POST ${base}/${noura.user_id}/enable`]: () => ({ status: 204 }),
      [`POST /admin/v1/stores/${store}/support-requests/${waitingReq.id}/cancel`]: () => ({ status: 204 }),
    });
    renderTeam();
    const user = userEvent.setup();
    await user.click(within(await rowOf('نورة خالد')).getByRole('button', { name: 'إجراءات على نورة خالد' }));
    await user.click(screen.getByRole('menuitem', { name: 'أعد تفعيل الحساب' }));
    let dialog = screen.getByRole('dialog', { name: 'إعادة تفعيل حساب نورة خالد' });
    await user.type(within(dialog).getByLabelText('السبب'), 'تم التحقق');
    await user.click(within(dialog).getByRole('button', { name: 'أعد تفعيل الحساب' }));
    expect(await screen.findByText('أُعيد تفعيل حساب نورة خالد.')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'ألغِ الطلب' }));
    dialog = screen.getByRole('dialog', { name: 'سحب طلب إلغاء التحقق بخطوتين لـمنى أحمد' });
    await user.type(within(dialog).getByLabelText('السبب'), 'العضو وجد موبايله');
    await user.click(within(dialog).getByRole('button', { name: 'اسحب الطلب' }));
    expect(await screen.findByText('سُحب طلب إلغاء التحقق بخطوتين لـمنى أحمد.')).toBeInTheDocument();
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(2);
  });

  it('hides actions without stores.manage and shows a refusal in the dialog', async () => {
    routes({ [`POST ${base}/${karim.user_id}/sign-out`]: () => ({ status: 404, body: { title: 'Not Found', status: 404, code: 'member_not_found' } }) });
    renderTeam(testAdmin({}, 'finance'));
    await rowOf('كريم سامي');
    expect(screen.queryByRole('button', { name: /إجراءات على/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'ألغِ الطلب' })).not.toBeInTheDocument();
  });

  it('closes the menu on Escape', async () => {
    routes();
    renderTeam();
    const user = userEvent.setup();
    const toggle = within(await rowOf('كريم سامي')).getByRole('button', { name: 'إجراءات على كريم سامي' });
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard('{Escape}');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });
});
