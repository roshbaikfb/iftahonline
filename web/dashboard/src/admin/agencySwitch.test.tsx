import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI } from '../test/api';
import { Gate } from './Console';
import { AdminSessionContext, type Admin, type AdminSession } from './session';

const agency = '01M9AGENCY0000000000000001';
const stores = ['01M9STORE00000000000000001', '01M9STORE00000000000000002', '01M9STORE00000000000000003'];
const names = ['متجر نور للعطور', 'بيت القهوة', 'ركن الأطفال'];

function renderAgency(role: Admin['role']) {
  const value: AdminSession = { state: { kind: 'signed-in', admin: testAdmin({}, role) }, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[`/agencies/${agency}`]}>
        <Gate state={value.state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

const detail = (ids: string[], open: boolean) => ({
  agency: { id: agency, name: 'وكالة مدار الرقمية', currency: 'EGP', plan_code: 'agency', stores: ids.length, created_at: '2026-10-01T00:00:00Z' },
  store_ids: ids,
  ...(open
    ? {
        open_invoices: [
          {
            id: '01M9INV0000000000000000001',
            number: 'INV-2026-000398',
            period: '2026-11-01',
            currency: 'EGP',
            total_minor: 228000,
            due_at: '2026-11-04T00:00:00Z',
            attempts: 2,
          },
        ],
      }
    : {}),
});

function routes() {
  const storeRoutes = Object.fromEntries(
    stores.map((id, i) => [`GET /admin/v1/stores/${id}`, () => ({ status: 200, body: { store: { id, name: names[i] } } })]),
  );
  return mockAPI({
    [`GET /admin/v1/agencies/${agency}`]: () => ({ status: 200, body: detail(stores, true) }),
    [`POST /admin/v1/agencies/${agency}/switch-to-pays`]: () => ({ status: 200, body: detail([stores[2] ?? ''], true) }),
    ...storeRoutes,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('switching an unpaid agency’s stores (ADR-0087)', () => {
  it('moves the chosen stores with a reason', async () => {
    const calls = routes();
    renderAgency('finance');
    const user = userEvent.setup();

    const form = await screen.findByRole('form', { name: 'تحويل متاجر إلى «ادفع حسب مبيعاتك»' });
    expect(form).toHaveTextContent('INV-2026-000398');
    expect(within(form).getByRole('button', { name: 'اختر متجرًا على الأقل' })).toBeDisabled();
    await user.click(within(form).getByLabelText(names[0] ?? ''));
    await user.click(within(form).getByLabelText(names[1] ?? ''));
    expect(form).toHaveTextContent('«ركن الأطفال»فاتورة جديدة بسعر الوكالة، وتستمر المهلة');
    await user.selectOptions(within(form).getByLabelText('طريقة التحصيل للمتاجر المحوَّلة'), 'card');
    await user.type(within(form).getByLabelText('ملاحظة الاتفاق (إجباري)'), 'اتفاق هاتفي مع أ. سامي');
    await user.click(within(form).getByRole('button', { name: 'حوّل 2 متاجر وألغِ الفاتورة' }));

    expect(await screen.findByRole('status')).toHaveTextContent('تحوّل 2 متاجر إلى «ادفع حسب مبيعاتك»، وأُلغيت الفاتورة.');
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ store_ids: [stores[0], stores[1]], collection: 'card', reason: 'اتفاق هاتفي مع أ. سامي' });
  });

  it('is not offered to a role that may not change billing', async () => {
    routes();
    renderAgency('viewer');
    expect(await screen.findByRole('heading', { name: 'وكالة مدار الرقمية' })).toBeInTheDocument();
    expect(screen.queryByRole('form', { name: 'تحويل متاجر إلى «ادفع حسب مبيعاتك»' })).not.toBeInTheDocument();
  });
});
