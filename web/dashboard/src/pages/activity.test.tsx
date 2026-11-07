import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { describe as say, when, who, type AuditEntry } from '../lib/activity';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { ActivityLog } from './ActivityLog';

const store = '01M5STORE0000000000000000A';
const s = `/v1/stores/${store}`;
const mona = '01M5USER00000000000000MONA';
const sara = '01M5USER00000000000000SARA';
const orderID = '01M6S36FCAGK35QTHAN0NBYK2Q';

function entry(action: string, extra: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id: `${action}-${String(Math.random())}`,
    occurred_at: new Date().toISOString(),
    actor_user_id: mona,
    action,
    target_type: '',
    target_id: '',
    ip_prefix: '',
    metadata: {},
    ...extra,
  };
}

function session(role: string): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: mona, email: 'mona@example.com', name: 'منى أحمد', email_verified: true, mfa_enabled: false, created_at: '' },
        memberships: [{ store_id: store, role: role as 'owner' }],
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function renderAt(role = 'owner') {
  render(
    <SessionContext.Provider value={session(role)}>
      <MemoryRouter initialEntries={[`/stores/${store}/activity`]}>
        <Routes>
          <Route path="/stores/:storeId/activity" element={<ActivityLog />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const members = { items: [{ user_id: mona, name: 'منى أحمد', email: 'mona@example.com', role: 'owner', joined_at: '' }] };

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the activity log (D6)', () => {
  it('lists who changed what, newest first, and pages back', async () => {
    const calls = mockAPI({
      [`GET ${s}/members`]: () => ({ status: 200, body: members }),
      [`GET ${s}/audit-log`]: (c: Call) =>
        c.search.get('cursor') === 'next1'
          ? { status: 200, body: { items: [entry('store.created')] } }
          : {
              status: 200,
              body: {
                items: [
                  entry('order.cancelled', { target_id: orderID, actor_user_id: sara }),
                  entry('domain.added', { metadata: { host: 'shop.example.com' } }),
                  entry('something.new'),
                ],
                next_cursor: 'next1',
              },
            },
    });
    renderAt();
    const user = userEvent.setup();
    const table = await screen.findByRole('table');
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent('عضو سابق');
    expect(rows[0]).toHaveTextContent('إلغاء الطلب #N0NBYK2Q');
    const [first] = rows;
    if (!first) throw new Error('no rows');
    expect(within(first).getByRole('link', { name: '#N0NBYK2Q' })).toHaveAttribute('href', `/stores/${store}/orders/${orderID}`);
    expect(rows[1]).toHaveTextContent('منى أحمد');
    expect(rows[1]).toHaveTextContent('إضافة الدومين shop.example.com');
    expect(rows[2]).toHaveTextContent('تغيير في المتجر');

    await user.click(screen.getByRole('button', { name: 'عرض الأقدم' }));
    expect(await screen.findByText('إنشاء المتجر')).toBeInTheDocument();
    expect(within(table).getAllByRole('row')).toHaveLength(5);
    expect(screen.queryByRole('button', { name: 'عرض الأقدم' })).toBeNull();
    expect(calls.filter((c) => c.path === `${s}/audit-log`).map((c) => c.search.get('cursor'))).toEqual([null, 'next1']);
  });

  it('says when there is nothing yet', async () => {
    mockAPI({
      [`GET ${s}/members`]: () => ({ status: 200, body: members }),
      [`GET ${s}/audit-log`]: () => ({ status: 200, body: { items: [] } }),
    });
    renderAt();
    expect(await screen.findByText('لا نشاط بعد.')).toBeInTheDocument();
  });

  it('is for the owner alone', async () => {
    const calls = mockAPI({});
    renderAt('manager');
    expect(screen.getByText('سجل النشاط لصاحب المتجر وحده.')).toBeInTheDocument();
    await waitFor(() => {
      expect(calls.some((c) => c.path === `${s}/audit-log`)).toBe(false);
    });
  });
});

describe('activity sentences (D6)', () => {
  const id = (action: string, extra: Partial<AuditEntry> = {}) => say(entry(action, extra), store);

  it.each([
    ['order.confirmed', 'تأكيد الطلب'],
    ['order.returned', 'تسجيل إرجاع الطلب'],
    ['order.payment_paid', 'تسجيل دفع الطلب'],
    ['order.deposit_paid', 'تسجيل دفع الطلب (رسوم التوصيل المقدمة)'],
    ['order.unknown_step', 'تغيير في المتجر'],
    ['order.placed', 'إدخال طلب'],
    ['product.deleted', 'حذف منتج'],
    ['member.joined', 'انضمام عضو إلى فريق العمل'],
    ['theme.published', 'نشر تصميم المتجر'],
  ])('%s → %s', (action, text) => {
    expect(id(action, { target_id: orderID }).text).toBe(text);
  });

  it('names what metadata holds, left to right', () => {
    expect(id('payment_connection.created', { metadata: { gateway: 'stripe' } })).toEqual({ text: 'ربط بوابة الدفع', ltr: 'Stripe' });
    expect(id('coupon.created', { metadata: { code: 'SUMMER10' } })).toEqual({ text: 'إضافة الكوبون', ltr: 'SUMMER10' });
    expect(id('member.invited', { metadata: { role: 'support' } }).text).toBe('دعوة عضو جديد بدور خدمة العملاء');
    expect(id('member.role_changed', { metadata: { from: 'support', to: 'manager' } }).text).toBe('تغيير دور عضو من خدمة العملاء إلى مدير');
    expect(id('order.exported').link).toBeUndefined();
    expect(id('product.updated', { target_id: 'p1' }).link).toEqual({ to: `/stores/${store}/products/p1`, label: 'عرض المنتج' });
  });

  it('names who did it', () => {
    const names = new Map([[mona, 'منى أحمد']]);
    expect(who(entry('product.created'), names)).toBe('منى أحمد');
    expect(who(entry('product.created', { actor_user_id: sara }), names)).toBe('عضو سابق');
    const automatic = entry('order.cancelled');
    delete automatic.actor_user_id;
    expect(who(automatic, names)).toBe('المتجر تلقائيًا');
    expect(who(entry('store.updated', { metadata: { impersonated_by: 'admin' } }), names)).toBe('فريق دعم المنصة');
    // Platform support's actions on the team (C1), and a link's cancel.
    expect(who(entry('member.disabled', { metadata: { support_admin: 'admin', reason: 'x' } }), names)).toBe('فريق دعم المنصة');
    const byLink = entry('support.mfa_reset_cancelled', { metadata: { by: 'link' } });
    delete byLink.actor_user_id;
    expect(who(byLink, names)).toBe('من رابط الإيميل');
  });

  it('says support’s actions on the team (C1)', () => {
    expect(id('support.mfa_reset_requested').text).toBe('طلب إلغاء التحقق بخطوتين لعضو (يُنفَّذ بعد 24 ساعة)');
    expect(id('support.mfa_reset_done').text).toBe('إلغاء التحقق بخطوتين لعضو');
    expect(id('support.mfa_reset_cancelled', { metadata: { by: 'link' } }).text).toBe('إلغاء طلب الدعم لإزالة التحقق بخطوتين');
    expect(id('support.mfa_reset_cancelled', { metadata: { by: 'console' } }).text).toBe('سحب طلب إلغاء التحقق بخطوتين');
    expect(id('member.disabled').text).toBe('إيقاف حساب عضو');
    expect(id('member.signed_out').text).toBe('إخراج عضو من كل أجهزته');
    expect(id('store.ownership_transferred').text).toBe('نقل ملكية المتجر إلى عضو');
    expect(id('support.ownership_transfer_cancelled', { metadata: { by: 'link' } }).text).toBe('إلغاء طلب الدعم لنقل ملكية المتجر');
    expect(id('support.ownership_transfer_skipped').text).toBe('تجاوز طلب نقل الملكية: تغيّر الفريق قبل موعده');
  });

  it('says when, as the mockup does', () => {
    const now = new Date(2026, 10, 6, 21, 0);
    expect(when(new Date(2026, 10, 6, 14, 14).toISOString(), now)).toMatch(/^اليوم 2:14/);
    expect(when(new Date(2026, 10, 5, 21, 40).toISOString(), now)).toMatch(/^أمس 9:40/);
    expect(when(new Date(2026, 10, 3, 10, 0).toISOString(), now)).toBe('3 نوفمبر');
    expect(when(new Date(2025, 10, 3, 10, 0).toISOString(), now)).toBe('3 نوفمبر 2025');
  });
});
