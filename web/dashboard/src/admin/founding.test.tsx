import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI } from '../test/api';
import { Gate } from './Console';
import { memberState, percent, seatsLeft, toBps, type FoundingMember } from './founding';
import { AdminSessionContext, type AdminSession } from './session';

const admin = testAdmin({ id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' });

function renderFounding() {
  const state = { kind: 'signed-in', admin } as const;
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={['/founding']}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function member(over: Partial<FoundingMember> = {}): FoundingMember {
  return {
    store_id: '01M9STORE00000000000000001',
    seat: 1,
    discount_bps: 2000,
    currency: 'EGP',
    plan_code: 'merchant',
    cycle: 'monthly',
    locked_minor: 29900,
    joined_at: '2026-11-03T10:00:00Z',
    ...over,
  };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('the founding program in the console', () => {
  it('shows the seats and members, and saves the settings with a reason', async () => {
    const calls = mockAPI({
      'GET /admin/v1/founding': () => ({
        status: 200,
        body: {
          program: { enabled: false, seats: 100, discount_bps: 2000 },
          members: [member(), member({ store_id: '01M9STORE00000000000000002', seat: 2, lost_at: '2026-12-10T00:00:00Z', lost_reason: 'unpaid' })],
        },
      }),
      'PUT /admin/v1/founding': () => ({ status: 200, body: { enabled: true, seats: 100, discount_bps: 2500 } }),
    });
    renderFounding();
    const user = userEvent.setup();

    expect(await screen.findByText(/2 من 100 مقعد محجوز · باقي 98/)).toBeInTheDocument();
    const rows = screen.getAllByRole('row').slice(1);
    expect(within(rows[0] ?? document.body).getByText('299 ج.م')).toBeInTheDocument();
    expect(within(rows[1] ?? document.body).getByText('خرج: لم يدفع التجديد 30 يومًا')).toBeInTheDocument();
    expect(within(rows[0] ?? document.body).getByRole('link', { name: 'فتح المتجر' })).toHaveAttribute('href', '/stores/01M9STORE00000000000000001');

    await user.click(screen.getByLabelText('البرنامج مفعّل'));
    const discount = screen.getByLabelText('الخصم (%)');
    await user.clear(discount);
    await user.type(discount, '25');
    await user.type(screen.getByLabelText('السبب (إجباري)'), 'إطلاق البرنامج');
    await user.click(screen.getByRole('button', { name: 'حفظ' }));
    expect(await screen.findByRole('status')).toHaveTextContent('حُفظت إعدادات البرنامج.');
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ enabled: true, seats: 100, discount_bps: 2500, reason: 'إطلاق البرنامج' });
  });
});

describe('founding helpers', () => {
  it('formats and reads the discount, and counts the seats left', () => {
    expect(percent(2000)).toBe('20%');
    expect(percent(1250)).toBe('12.5%');
    expect(toBps('12.5')).toBe(1250);
    expect(seatsLeft({ enabled: true, seats: 2, discount_bps: 2000 }, [member(), member(), member()])).toBe(0);
    expect(memberState(member())).toBe('عضو');
    expect(memberState(member({ lost_reason: 'cancelled' }))).toBe('خرج: ألغى الاشتراك');
  });
});
