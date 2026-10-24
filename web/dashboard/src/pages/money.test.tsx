import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { components } from '../api/client';
import { SessionContext, type Session } from '../session';
import { mockAPI } from '../test/api';
import { COD } from './COD';
import { Gateways } from './Gateways';
import { OrderDetail } from './OrderDetail';
import { Payments } from './Payments';

type Payment = components['schemas']['Payment'];

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

const session: Session = {
  state: {
    kind: 'signed-in',
    me: {
      user: { id: 'u', email: 'a@b.c', name: 'A', email_verified: true, mfa_enabled: false, created_at: '' },
      memberships: [{ store_id: store, role: 'owner' }],
    },
  },
  reload: vi.fn(() => Promise.resolve()),
  signOut: vi.fn(() => Promise.resolve()),
};

function renderAt(path: string) {
  return render(
    <SessionContext.Provider value={session}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/stores/:storeId/cod" element={<COD />} />
          <Route path="/stores/:storeId/gateways" element={<Gateways />} />
          <Route path="/stores/:storeId/payments" element={<Payments />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<OrderDetail />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const storeRoute = { [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى' } }) };

function payment(extra: Partial<Payment> = {}): Payment {
  return {
    id: 'P1',
    order_id: 'O1',
    connection_id: 'G1',
    gateway: 'paymob',
    status: 'paid',
    amount_minor: 52000,
    refunded_minor: 0,
    currency: 'EGP',
    transaction_id: 'TX-9',
    status_at: '2026-10-24T10:00:00Z',
    created_at: '2026-10-24T10:00:00Z',
    ...extra,
  };
}

describe('COD', () => {
  it('shows what the carrier holds, aged, and imports a statement', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping/cod`]: () => ({
        status: 200,
        body: {
          from: '2026-09-24T00:00:00Z',
          to: '2026-10-24T00:00:00Z',
          unmatched_lines: 1,
          unmatched_minor: 9900,
          carriers: [
            {
              connection_id: 'C1',
              carrier: 'bosta',
              currency: 'EGP',
              delivered: 3,
              expected_minor: 156000,
              settled_minor: 52000,
              held_minor: 104000,
              held_0_7_minor: 52000,
              held_8_14_minor: 0,
              held_15_plus_minor: 52000,
              over_settled: 0,
            },
          ],
        },
      }),
      [`GET ${base}/shipping/cod/held`]: () => ({
        status: 200,
        body: {
          items: [
            {
              shipment_id: 'S1',
              order_id: 'O1',
              carrier: 'bosta',
              tracking_number: '7788',
              currency: 'EGP',
              expected_minor: 52000,
              settled_minor: 0,
              held_minor: 52000,
              delivered_at: '2026-10-01T10:00:00Z',
            },
          ],
        },
      }),
      [`GET ${base}/shipping/carriers`]: () => ({
        status: 200,
        body: { carriers: ['bosta'], connections: [{ id: 'C1', carrier: 'bosta', status: 'active', created_at: '2026-10-01T10:00:00Z' }] },
      }),
      [`POST ${base}/shipping/cod/statements`]: () => ({
        status: 200,
        body: { lines: 2, imported: 1, duplicates: 1, matched: 1, errors: [] },
      }),
    });
    renderAt(`/stores/${store}/cod`);
    const user = userEvent.setup();
    expect(await screen.findByText('1,040 ج.م')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '7788' })).toHaveAttribute('href', `/stores/${store}/shipments/S1`);
    expect(screen.getByText(/سطر في الكشوف لا يطابق/)).toBeInTheDocument();
    const csv = new File(['tracking_number,amount\n7788,520\n'], 'statement.csv', { type: 'text/csv' });
    await user.upload(screen.getByLabelText('الملف'), csv);
    await user.click(screen.getByRole('button', { name: 'استيراد' }));
    expect(await screen.findByText(/أُضيف 1/)).toBeInTheDocument();
    const post = calls.find((c) => c.method === 'POST');
    expect(post?.search.get('connection_id')).toBe('C1');
    expect(post?.body).toBe('tracking_number,amount\n7788,520\n');
  });

  it('asks for the range through the URL', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping/cod`]: () => ({
        status: 200,
        body: { from: '2026-10-01T00:00:00Z', to: '2026-10-02T00:00:00Z', unmatched_lines: 0, unmatched_minor: 0, carriers: [] },
      }),
      [`GET ${base}/shipping/cod/held`]: () => ({ status: 200, body: { items: [] } }),
      [`GET ${base}/shipping/carriers`]: () => ({ status: 200, body: { carriers: [], connections: [] } }),
    });
    renderAt(`/stores/${store}/cod?from=2026-10-01&to=2026-10-01`);
    expect(await screen.findByText(/لا توجد طرود مسلّمة/)).toBeInTheDocument();
    const q = calls.find((c) => c.path === `${base}/shipping/cod`)?.search;
    expect(q?.get('from')).toBeTruthy();
    expect(new Date(q?.get('to') ?? '').getTime() - new Date(q?.get('from') ?? '').getTime()).toBe(24 * 3600 * 1000);
  });
});

describe('Gateways', () => {
  it('connects Paymob with every key it needs', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/payments/gateways`]: () => ({ status: 200, body: { gateways: ['paymob'], connections: [] } }),
      [`POST ${base}/payments/gateways`]: () => ({
        status: 201,
        body: { id: 'G1', gateway: 'paymob', status: 'active', created_at: '2026-10-24T10:00:00Z' },
      }),
    });
    renderAt(`/stores/${store}/gateways`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Secret key'), 'sk_test');
    await user.type(screen.getByLabelText('Public key'), 'pk_test');
    await user.type(screen.getByLabelText('API key'), 'api_test');
    await user.type(screen.getByLabelText('HMAC secret'), 'hmac_test');
    await user.type(screen.getByLabelText('أرقام تكامل البطاقات (Integration IDs)'), '123, 456');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    expect(await screen.findByText(/تم الربط/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      gateway: 'paymob',
      api_key: 'api_test',
      webhook_secret: 'hmac_test',
      secret_key: 'sk_test',
      public_key: 'pk_test',
      integration_ids: [123, 456],
    });
  });

  it('refuses integration IDs that are not numbers', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/payments/gateways`]: () => ({ status: 200, body: { gateways: ['paymob'], connections: [] } }),
    });
    renderAt(`/stores/${store}/gateways`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('Secret key'), 's');
    await user.type(screen.getByLabelText('Public key'), 'p');
    await user.type(screen.getByLabelText('API key'), 'a');
    await user.type(screen.getByLabelText('HMAC secret'), 'h');
    await user.type(screen.getByLabelText('أرقام تكامل البطاقات (Integration IDs)'), 'abc');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    expect(await screen.findByText(/أرقام التكامل/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });
});

describe('Payments', () => {
  it('lists card payments with a link to their order', async () => {
    mockAPI({
      ...storeRoute,
      [`GET ${base}/payments`]: () => ({
        status: 200,
        body: { items: [payment({ status: 'partially_refunded', refunded_minor: 10000 })] },
      }),
    });
    renderAt(`/stores/${store}/payments`);
    expect(await screen.findByText('مسترد جزئيًا')).toBeInTheDocument();
    expect(screen.getByText('100 ج.م')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'الطلب' })).toHaveAttribute('href', `/stores/${store}/orders/O1`);
  });
});

describe('OrderPayments', () => {
  const order = {
    id: 'O1',
    code: 'P0R1S2T3',
    status: 'confirmed',
    version: 2,
    source: 'storefront',
    customer_id: 'CU',
    name: 'منى أحمد',
    phone: '+201012345678',
    governorate: 'EG-C',
    city: 'مدينة نصر',
    address: '12 شارع',
    notes: '',
    currency: 'EGP',
    subtotal_minor: 47000,
    shipping_minor: 5000,
    cod_fee_minor: 0,
    payment_method: 'card',
    payment_status: 'paid',
    total_minor: 52000,
    item_count: 1,
    created_at: '2026-10-24T10:00:00Z',
    updated_at: '2026-10-24T10:00:00Z',
    status_changed_at: '2026-10-24T10:00:00Z',
  };

  it('refunds part of a paid order, never more than is left', async () => {
    const refunds: components['schemas']['PaymentRefund'][] = [];
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/orders/O1`]: () => ({ status: 200, body: { order, lines: [], events: [] } }),
      [`GET ${base}/shipping/shipments`]: () => ({ status: 200, body: { items: [] } }),
      [`GET ${base}/shipping/carriers`]: () => ({ status: 200, body: { carriers: [], connections: [] } }),
      [`GET ${base}/payments`]: () => ({ status: 200, body: { items: [payment()] } }),
      [`GET ${base}/payments/P1`]: () => ({ status: 200, body: { payment: payment(), refunds } }),
      [`POST ${base}/payments/P1/refunds`]: (c) => {
        const r = {
          id: 'R1',
          payment_id: 'P1',
          amount_minor: (c.body as { amount_minor: number }).amount_minor,
          currency: 'EGP',
          status: 'requested' as const,
          reason: 'مرتجع',
          created_at: '2026-10-24T11:00:00Z',
        };
        refunds.push(r);
        return { status: 202, body: r };
      },
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/orders/O1`);
    const user = userEvent.setup();
    const amount = await screen.findByLabelText('مبلغ الاسترداد');
    expect(amount).toHaveValue('520');
    expect(screen.getByText('TX-9')).toBeInTheDocument();
    await user.clear(amount);
    await user.type(amount, '600');
    await user.click(screen.getByRole('button', { name: 'استرداد' }));
    expect(await screen.findByText(/المبلغ: من 0.01 إلى/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);

    await user.clear(amount);
    await user.type(amount, '150');
    await user.type(screen.getByLabelText('السبب (اختياري)'), 'مرتجع');
    await user.click(screen.getByRole('button', { name: 'استرداد' }));
    expect(await screen.findByText(/طُلب الاسترداد/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ amount_minor: 15000, reason: 'مرتجع' });
    // What is in flight is not offered again.
    await waitFor(() => {
      expect(screen.getByLabelText('مبلغ الاسترداد')).toHaveValue('370');
    });
  });

  it('shows nothing for a cash-on-delivery order', async () => {
    mockAPI({
      ...storeRoute,
      [`GET ${base}/orders/O1`]: () => ({
        status: 200,
        body: { order: { ...order, payment_method: 'cod', payment_status: 'unpaid' }, lines: [], events: [] },
      }),
      [`GET ${base}/shipping/shipments`]: () => ({ status: 200, body: { items: [] } }),
      [`GET ${base}/shipping/carriers`]: () => ({ status: 200, body: { carriers: [], connections: [] } }),
    });
    renderAt(`/stores/${store}/orders/O1`);
    expect(await screen.findByText('منى أحمد')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'الدفع بالبطاقة' })).not.toBeInTheDocument();
  });
});
