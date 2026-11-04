import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { Gate } from './Console';
import { groupsOf, type Result } from './search';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const store = '01M5STORE0000000000000000A';
const invoice = '01M5INV00000000000000000AA';
const empty: Result = { stores: [], owner_stores: [], invoices: [], payments: [], agencies: [], admins: [], failed: [] };
const nile = {
  store: { id: store, slug: 'nile', name: 'عطور النيل', status: 'active', plan: 'pro', currency: 'EGP', locale: 'ar', timezone: 'Africa/Cairo', created_at: '', updated_at: '' },
  primary_host: 'nile.iftahonline.com',
} as const;
const found: Result = {
  ...empty,
  stores: [nile],
  agencies: [{ id: 'AG1', name: 'وكالة النيل', currency: 'EGP', plan_code: 'agency', stores: 12, created_at: '' }],
  invoices: [
    {
      id: invoice,
      number: 'INV-2026-000183',
      account_id: 'A1',
      account_kind: 'agency',
      account_name: 'وكالة النيل',
      period: '2026-11-01',
      currency: 'EGP',
      subtotal_minor: 399900,
      tax_bps: 1400,
      tax_minor: 55986,
      total_minor: 455886,
      status: 'open',
      overdue: false,
      issued_at: '',
      due_at: '',
      attempts: 0,
    },
  ],
  admins: [{ id: 'M1', name: 'سارة النيل', email: 'sara@iftahonline.com', role: 'finance', active: true, resetting: false, created_at: '' }],
};

function renderAt(path: string, state: AdminState = { kind: 'signed-in', admin: testAdmin() }) {
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function routes(search: (c: Call) => Result, over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/search': (c) => ({ status: 200, body: search(c) }),
    'GET /admin/v1/team': () => ({ status: 200, body: { admins: [], invites: [] } }),
    [`GET /admin/v1/invoices/${invoice}`]: () => ({ status: 404, body: { title: 'Not Found', status: 404 } }),
    ...over,
  });
}

describe('search helpers', () => {
  it('groups the result in the mockup’s order and numbers the rows across groups', () => {
    const groups = groupsOf({ ...found, owner_stores: [nile] });
    expect(groups.map((g) => g.title)).toEqual(['متاجر صاحب هذا الرقم أو الإيميل', 'المتاجر', 'الوكالات', 'الفواتير', 'الأدمن']);
    expect(groups.flatMap((g) => g.hits.map((h) => h.index))).toEqual([0, 1, 2, 3, 4]);
    expect(groups[3]?.hits[0]).toMatchObject({ to: `/billing/invoices/${invoice}`, title: 'INV-2026-000183', sub: 'وكالة النيل · 4,558.86 ج.م' });
    expect(groupsOf(empty)).toEqual([]);
  });
});

describe('the search palette', () => {
  it('opens with Ctrl+K, asks as you type, and opens a row with the keyboard', async () => {
    const calls = routes(() => found, { 'GET /admin/v1/health': () => ({ status: 503, body: { title: 'x', status: 503 } }) });
    renderAt('/audit');
    const user = userEvent.setup();
    await screen.findByRole('heading', { level: 1 });
    await user.keyboard('{Control>}k{/Control}');
    const dialog = screen.getByRole('dialog', { name: 'البحث الشامل' });
    const box = within(dialog).getByRole('combobox', { name: 'ابحث' });
    expect(box).toHaveFocus();
    await user.type(box, 'ن');
    expect(within(dialog).getByText('اكتب حرفين على الأقل.')).toBeInTheDocument();
    await user.type(box, 'يل');
    const list = await within(dialog).findByRole('listbox', { name: 'النتائج' });
    expect(await within(list).findByRole('group', { name: 'المتاجر' })).toHaveTextContent('عطور النيل');
    expect(within(list).getAllByRole('option')).toHaveLength(4);
    expect(within(list).getAllByRole('option')[0]).toHaveAttribute('aria-selected', 'true');
    expect(calls.filter((c) => c.path === '/admin/v1/search').map((c) => c.search.get('q'))).toEqual(['نيل']);

    await user.keyboard('{ArrowUp}');
    expect(within(list).getAllByRole('option')[3]).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{Enter}');
    expect(await screen.findByRole('heading', { name: 'الفريق' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'البحث الشامل' })).not.toBeInTheDocument();
  });

  it('jumps to what an exact invoice number names', async () => {
    routes(() => ({ ...found, jump: `/billing/invoices/${invoice}` }));
    renderAt('/audit');
    const user = userEvent.setup();
    const nav = await screen.findByRole('navigation', { name: 'قائمة الكونسول' });
    await user.click(within(nav).getByRole('button', { name: /ابحث في كل شيء/ }));
    await user.type(screen.getByRole('combobox', { name: 'ابحث' }), 'INV-2026-000183');
    await screen.findByRole('group', { name: 'الفواتير' });
    await user.keyboard('{Enter}');
    expect(await screen.findByText('لا فاتورة بهذا الرقم.')).toBeInTheDocument();
  });

  it('says when nothing matches, notes an owner lookup, and closes with Escape', async () => {
    routes((c) => (c.search.get('q') === 'zzz' ? empty : { ...empty, owner_stores: [nile] }));
    renderAt('/audit');
    const user = userEvent.setup();
    await screen.findByRole('heading', { level: 1 });
    await user.keyboard('/');
    const box = screen.getByRole('combobox', { name: 'ابحث' });
    await user.type(box, 'zzz');
    expect(await screen.findByRole('status')).toHaveTextContent('لا نتائج لـ «zzz»');
    await user.clear(box);
    await user.type(box, '01012345678');
    expect(await screen.findByRole('group', { name: 'متاجر صاحب هذا الرقم أو الإيميل' })).toBeInTheDocument();
    expect(screen.getByText(/يُسجَّل في سجل الأدمن مخفيًا جزئيًا/)).toBeInTheDocument();
    await user.keyboard('{Escape}');
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: 'البحث الشامل' })).not.toBeInTheDocument();
    });
  });

  it('does not open on "/" typed in a field', async () => {
    routes(() => empty);
    renderAt('/audit');
    const user = userEvent.setup();
    const from = await screen.findByLabelText('من');
    await user.click(screen.getByLabelText('الأدمن'));
    from.focus();
    await user.keyboard('/');
    expect(screen.queryByRole('dialog', { name: 'البحث الشامل' })).not.toBeInTheDocument();
  });
});
