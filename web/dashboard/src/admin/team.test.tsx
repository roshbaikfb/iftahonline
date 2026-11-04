import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';
import { expiresIn, inviteLink, teamFirst, type Invite, type Member } from './team';

const me = testAdmin({ id: '01M5ADMIN000000000000000AA', name: 'أحمد' });
const sara: Member = { id: '01M5ADMIN000000000000000BB', name: 'سارة', email: 'sara@iftahonline.com', role: 'finance', active: true, resetting: false, created_at: '', last_login_at: '2026-11-03T12:14:00Z' };
const hala: Member = { id: '01M5ADMIN000000000000000CC', name: 'هالة', email: 'hala@iftahonline.com', role: 'operations', active: false, resetting: false, created_at: '' };
const self: Member = { id: me.id, name: 'أحمد', email: me.email, role: 'owner', active: true, resetting: false, created_at: '' };
const nour: Invite = { id: '01M5INVITE00000000000000AA', purpose: 'join', admin_id: '01M5ADMIN000000000000000DD', email: 'nour@iftahonline.com', name: 'نور', role: 'content', created_at: '', expires_at: '2026-11-06T10:00:00Z' };

// rowWith is the table row naming name.
function rowWith(rows: HTMLElement[], name: string): HTMLElement {
  const row = rows.find((r) => r.textContent.includes(name));
  if (!row) throw new Error(`no row for ${name}`);
  return row;
}

function renderAt(path: string, state: AdminState = { kind: 'signed-in', admin: me }) {
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
  return value;
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/team': () => ({ status: 200, body: { admins: [sara, hala, self], invites: [nour] } }),
    ...over,
  });
}

afterEach(() => {
  vi.useRealTimers();
});

describe('team helpers', () => {
  it('words an invite’s time left, sorts the team and builds the link', () => {
    const now = new Date('2026-11-04T10:00:00Z');
    expect(expiresIn('2026-11-06T10:00:00Z', now)).toBe('تنتهي بعد يومين');
    expect(expiresIn('2026-11-04T15:00:00Z', now)).toBe('تنتهي بعد 5 ساعة');
    expect(expiresIn('2026-11-04T09:00:00Z', now)).toBe('انتهت صلاحيتها: أنشئ رابطًا جديدًا');
    expect(teamFirst([hala, sara, self], me.id).map((m) => m.name)).toEqual(['أحمد', 'سارة', 'هالة']);
    expect(inviteLink('TOK', 'https://admin.iftahonline.com')).toBe('https://admin.iftahonline.com/console/invite#TOK');
  });
});

describe('team page', () => {
  it('lists the team and the invites, and offers nothing on your own row', async () => {
    routes();
    renderAt('/team');
    const table = await screen.findByRole('region', { name: 'أعضاء الفريق' });
    const rows = await within(table).findAllByRole('row');
    expect(rows[1]).toHaveTextContent('أحمد (أنت)');
    expect(within(rowWith(rows, 'أحمد (أنت)')).queryByRole('button')).not.toBeInTheDocument();
    const saraRow = rowWith(rows, 'سارة');
    expect(within(saraRow).getByText('المالية')).toBeInTheDocument();
    for (const name of ['تغيير الدور', 'إعادة ضبط المصادقة', 'إيقاف']) expect(within(saraRow).getByRole('button', { name })).toBeInTheDocument();
    const halaRow = rowWith(rows, 'هالة');
    expect(within(halaRow).getByText('موقوف')).toBeInTheDocument();
    expect(within(halaRow).getByRole('button', { name: 'إعادة التفعيل' })).toBeInTheDocument();
    const nourRow = rowWith(rows, 'نور');
    expect(within(nourRow).getByText('دعوة لم تُقبل')).toBeInTheDocument();
    expect(within(nourRow).getByRole('button', { name: 'رابط جديد' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'قائمة الكونسول' })).toHaveTextContent('الفريق');
  });

  it('invites an admin and shows the link once', async () => {
    const calls = routes({
      'POST /admin/v1/team/invites': () => ({ status: 201, body: { invite: { ...nour, id: 'NEW' }, token: 'TOKEN123' } }),
    });
    renderAt('/team');
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'دعوة أدمن' }));
    const dialog = screen.getByRole('dialog', { name: 'دعوة أدمن' });
    await user.type(within(dialog).getByLabelText('الاسم'), 'نور');
    await user.type(within(dialog).getByLabelText('الإيميل'), 'nour@iftahonline.com');
    await user.click(within(dialog).getByRole('radio', { name: /المحتوى/ }));
    await user.type(within(dialog).getByLabelText('السبب'), 'تنضم لفريق المحتوى');
    await user.click(within(dialog).getByRole('button', { name: 'أنشئ رابط الدعوة' }));
    const link = await screen.findByRole('dialog', { name: 'رابط الدعوة جاهز' });
    expect(within(link).getByRole('textbox', { name: 'رابط الدعوة' })).toHaveValue(inviteLink('TOKEN123'));
    expect(link).toHaveTextContent('لن يظهر هذا الرابط مرة أخرى.');
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ email: 'nour@iftahonline.com', name: 'نور', role: 'content', reason: 'تنضم لفريق المحتوى' });
    await user.click(within(link).getByRole('button', { name: 'تم' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('changes a role, disables, resets and cancels an invite, each with a reason', async () => {
    const calls = routes({
      [`PUT /admin/v1/team/admins/${sara.id}/role`]: () => ({ status: 204 }),
      [`POST /admin/v1/team/admins/${sara.id}/disable`]: () => ({ status: 204 }),
      [`POST /admin/v1/team/admins/${sara.id}/reset`]: () => ({ status: 200, body: { invite: { ...nour, purpose: 'reset' }, token: 'RESET1' } }),
      [`POST /admin/v1/team/invites/${nour.id}/cancel`]: () => ({ status: 204 }),
    });
    renderAt('/team');
    const user = userEvent.setup();
    const table = await screen.findByRole('region', { name: 'أعضاء الفريق' });
    await within(table).findByText('سارة');
    const row = (name: string) => rowWith(within(table).getAllByRole('row'), name);

    await user.click(within(row('سارة')).getByRole('button', { name: 'تغيير الدور' }));
    let dialog = screen.getByRole('dialog', { name: 'دور سارة' });
    expect(within(dialog).getByRole('radio', { name: /المالية/ })).toBeChecked();
    await user.click(within(dialog).getByRole('radio', { name: /مشاهدة فقط/ }));
    await user.type(within(dialog).getByLabelText('السبب'), 'للقراءة فقط');
    await user.click(within(dialog).getByRole('button', { name: 'غيّر الدور' }));
    expect(await screen.findByRole('status')).toHaveTextContent('صار دور سارة: مشاهدة فقط.');

    await user.click(within(row('سارة')).getByRole('button', { name: 'إيقاف' }));
    dialog = screen.getByRole('dialog', { name: 'إيقاف سارة' });
    await user.type(within(dialog).getByLabelText('السبب'), 'غادرت الشركة');
    await user.click(within(dialog).getByRole('button', { name: 'أوقفه' }));
    expect(await screen.findByRole('status')).toHaveTextContent('أُوقف سارة وخرج من كل جلساته.');

    await user.click(within(row('سارة')).getByRole('button', { name: 'إعادة ضبط المصادقة' }));
    dialog = screen.getByRole('dialog', { name: 'إعادة ضبط مصادقة سارة' });
    await user.type(within(dialog).getByLabelText('السبب'), 'فقدت هاتفها');
    await user.click(within(dialog).getByRole('button', { name: 'أعد الضبط وأنشئ الرابط' }));
    const link = await screen.findByRole('dialog', { name: 'رابط إعادة الضبط جاهز' });
    expect(within(link).getByRole('textbox', { name: 'رابط الدعوة' })).toHaveValue(inviteLink('RESET1'));
    await user.click(within(link).getByRole('button', { name: 'تم' }));

    await user.click(within(row('نور')).getByRole('button', { name: 'إلغاء الدعوة' }));
    dialog = screen.getByRole('dialog', { name: 'إلغاء دعوة نور' });
    await user.type(within(dialog).getByLabelText('السبب'), 'لم تعد ستنضم');
    await user.click(within(dialog).getByRole('button', { name: 'ألغِ الدعوة' }));
    expect(await screen.findByRole('status')).toHaveTextContent('أُلغيت دعوة نور.');

    const bodies = calls.filter((c) => c.method !== 'GET').map((c) => [c.path, c.body]);
    expect(bodies).toEqual([
      [`/admin/v1/team/admins/${sara.id}/role`, { role: 'viewer', reason: 'للقراءة فقط' }],
      [`/admin/v1/team/admins/${sara.id}/disable`, { reason: 'غادرت الشركة' }],
      [`/admin/v1/team/admins/${sara.id}/reset`, { reason: 'فقدت هاتفها' }],
      [`/admin/v1/team/invites/${nour.id}/cancel`, { reason: 'لم تعد ستنضم' }],
    ]);
  });

  it('shows a refused change in its dialog', async () => {
    routes({ [`PUT /admin/v1/team/admins/${sara.id}/role`]: () => ({ status: 409, body: { title: 'Conflict', status: 409, detail: 'the console must keep at least one active owner' } }) });
    renderAt('/team');
    const user = userEvent.setup();
    const table = await screen.findByRole('region', { name: 'أعضاء الفريق' });
    await within(table).findByText('سارة');
    await user.click(within(rowWith(within(table).getAllByRole('row'), 'سارة')).getByRole('button', { name: 'تغيير الدور' }));
    const dialog = screen.getByRole('dialog', { name: 'دور سارة' });
    await user.type(within(dialog).getByLabelText('السبب'), 'سبب');
    await user.click(within(dialog).getByRole('button', { name: 'غيّر الدور' }));
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('at least one active owner');
  });

  it('is for owners only', async () => {
    routes();
    renderAt('/team', { kind: 'signed-in', admin: testAdmin({}, 'finance') });
    expect(await screen.findByRole('heading', { name: 'لا تملك صلاحية هذه الصفحة' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'قائمة الكونسول' })).not.toHaveTextContent('الفريق');
  });
});

describe('accepting an invite', () => {
  const view = {
    purpose: 'join',
    name: 'نور',
    email: 'nour@iftahonline.com',
    role: 'content',
    invited_by_name: 'أحمد',
    expires_at: '2026-11-06T10:00:00Z',
    totp_secret: 'AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH',
    totp_uri: 'otpauth://totp/x',
    totp_qr_svg: '<svg xmlns="http://www.w3.org/2000/svg"></svg>',
  };

  it('reads the token from the fragment, sets a password, confirms a code and signs in', async () => {
    globalThis.history.replaceState(null, '', '/console/invite#TOKEN123');
    const calls = mockAPI({
      'POST /admin/v1/invites/lookup': () => ({ status: 200, body: view }),
      'POST /admin/v1/invites/accept': () => ({ status: 200, body: { admin: testAdmin({ name: 'نور' }, 'content'), expires_at: '' } }),
    });
    const session = renderAt('/invite', { kind: 'signed-out' });
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('أهلًا نور، دعاك أحمد إلى الكونسول');
    expect(screen.getByRole('img', { name: /رمز QR/ })).toHaveAttribute('src', expect.stringMatching(/^data:image\/svg\+xml;utf8,/));
    expect(screen.getByText('AAAA BBBB CCCC DDDD EEEE FFFF GGGG HHHH')).toBeInTheDocument();
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('كلمة السر'), 'a long new passphrase');
    await user.type(screen.getByLabelText('أعدها'), 'a long new passphrase');
    await user.type(screen.getByLabelText(/الرمز المكوّن/), '482 913');
    await user.click(screen.getByRole('button', { name: 'فعّل حسابي وادخل' }));
    await waitFor(() => {
      expect(session.signedIn).toHaveBeenCalled();
    });
    expect(calls.find((c) => c.path === '/admin/v1/invites/lookup')?.body).toEqual({ token: 'TOKEN123' });
    expect(calls.find((c) => c.path === '/admin/v1/invites/accept')?.body).toEqual({ token: 'TOKEN123', password: 'a long new passphrase', code: '482913' });
  });

  it('refuses mismatched passwords before asking the server', async () => {
    globalThis.history.replaceState(null, '', '/console/invite#TOKEN123');
    const calls = mockAPI({ 'POST /admin/v1/invites/lookup': () => ({ status: 200, body: view }) });
    renderAt('/invite', { kind: 'signed-out' });
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('كلمة السر'), 'a long new passphrase');
    await user.type(screen.getByLabelText('أعدها'), 'another long passphrase');
    await user.type(screen.getByLabelText(/الرمز المكوّن/), '123456');
    await user.click(screen.getByRole('button', { name: 'فعّل حسابي وادخل' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('كلمتا السر غير متطابقتين.');
    expect(calls.some((c) => c.path === '/admin/v1/invites/accept')).toBe(false);
  });

  it('says only that a bad link no longer works', async () => {
    globalThis.history.replaceState(null, '', '/console/invite#OLD');
    mockAPI({ 'POST /admin/v1/invites/lookup': () => ({ status: 404, body: { title: 'Not Found', status: 404 } }) });
    renderAt('/invite', { kind: 'signed-out' });
    expect(await screen.findByRole('heading', { name: 'انتهت صلاحية هذه الدعوة' })).toBeInTheDocument();
    expect(screen.queryByLabelText('كلمة السر')).not.toBeInTheDocument();
  });
});
