import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { components } from '../api/client';
import { bpToPercent, percentToBp, toMajor, toMinor } from '../lib/amounts';
import { SessionContext, type Session } from '../session';
import { mockAPI } from '../test/api';
import { Carriers } from './Carriers';
import { OrderDetail } from './OrderDetail';
import { ShipmentDetail } from './ShipmentDetail';
import { Shipments } from './Shipments';
import { Shipping } from './Shipping';

type Shipment = components['schemas']['Shipment'];
type Zone = components['schemas']['ShippingZone'];

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
          <Route path="/stores/:storeId/shipping" element={<Shipping />} />
          <Route path="/stores/:storeId/carriers" element={<Carriers />} />
          <Route path="/stores/:storeId/shipments" element={<Shipments />} />
          <Route path="/stores/:storeId/shipments/:shipmentId" element={<ShipmentDetail />} />
          <Route path="/stores/:storeId/orders/:orderId" element={<OrderDetail />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const storeRoute = { [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى' } }) };

const areas = [
  { code: 'EG-C', name_ar: 'القاهرة', name_en: 'Cairo' },
  { code: 'EG-GZ', name_ar: 'الجيزة', name_en: 'Giza' },
  { code: 'EG-ALX', name_ar: 'الإسكندرية', name_en: 'Alexandria' },
];

const cairo: Zone = {
  id: 'Z1', name: 'القاهرة الكبرى', areas: ['EG-C', 'EG-GZ'], rate_minor: 5000, cities: [{ name: 'المعادي', rate_minor: 4000 }], position: 0, version: 3,
};

function shipment(extra: Partial<Shipment> = {}): Shipment {
  return {
    id: 'S1', order_id: 'O1', connection_id: 'C1', carrier: 'bosta', status: 'created', tracking_number: '7788', cod_minor: 52000, currency: 'EGP',
    label_ready: true, status_at: '2026-10-24T10:00:00Z', created_at: '2026-10-24T10:00:00Z', version: 1, ...extra,
  };
}

describe('amounts', () => {
  it('reads what merchants type as minor units', () => {
    expect(toMinor('45')).toBe(4500);
    expect(toMinor('45.5')).toBe(4550);
    expect(toMinor(' 1,250.75 ')).toBe(125075);
    expect(toMinor('٤٥٫٥')).toBe(4550);
    for (const bad of ['', 'x', '-3', '1.234', '1e3']) expect(toMinor(bad)).toBeUndefined();
    expect(toMajor(4500)).toBe('45');
    expect(toMajor(4550)).toBe('45.5');
    expect(toMajor(125075)).toBe('1250.75');
    expect(percentToBp('1.5')).toBe(150);
    expect(percentToBp('abc')).toBeUndefined();
    expect(bpToPercent(150)).toBe('1.5');
  });
});

describe('Shipping', () => {
  it('saves the settings with the version read', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping`]: () => ({
        status: 200,
        body: { currency: 'EGP', areas, settings: { cod_fee: { fixed_minor: 1000, bp: 0, min_minor: 0, max_minor: 0 }, version: 2 }, zones: [cairo] },
      }),
      [`PUT ${base}/shipping/settings`]: () => ({ status: 200, body: {} }),
    });
    renderAt(`/stores/${store}/shipping`);
    const user = userEvent.setup();
    expect(await screen.findByText('القاهرة الكبرى')).toBeInTheDocument();
    expect(screen.getByText('القاهرة، الجيزة')).toBeInTheDocument();
    await user.type(screen.getByLabelText('شحن مجاني للطلبات من (اتركه فارغًا لعدم وجوده)'), '1000');
    const pct = screen.getAllByLabelText('نسبة من الطلب %')[0];
    if (!pct) throw new Error('no percent field');
    await user.clear(pct);
    await user.type(pct, '1.5');
    await user.click(screen.getByRole('button', { name: 'حفظ الإعدادات' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'PUT')).toBe(true);
    });
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({
      cod_fee: { fixed_minor: 1000, bp: 150, min_minor: 0, max_minor: 0 },
      version: 2,
      free_over_minor: 100000,
    });
  });

  it('adds a zone of the governorates no other zone has, with city rates', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping`]: () => ({
        status: 200,
        body: { currency: 'EGP', areas, settings: { cod_fee: { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 }, version: 0 }, zones: [cairo] },
      }),
      [`POST ${base}/shipping/zones`]: () => ({ status: 201, body: {} }),
    });
    renderAt(`/stores/${store}/shipping`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'منطقة جديدة' }));
    const form = screen.getByRole('heading', { name: 'منطقة جديدة' }).closest('form');
    if (!form) throw new Error('no zone form');
    const f = within(form);
    expect(f.getByRole('checkbox', { name: 'القاهرة' })).toBeDisabled();
    await user.type(f.getByLabelText('اسم المنطقة'), 'الإسكندرية');
    await user.type(f.getByLabelText('سعر الشحن'), '60');
    await user.click(f.getByRole('checkbox', { name: 'الإسكندرية' }));
    await user.click(f.getByRole('button', { name: '+ مدينة' }));
    await user.type(f.getByLabelText('المدينة'), 'سموحة');
    await user.type(f.getByLabelText('سعرها'), '45.5');
    await user.click(f.getByRole('button', { name: 'حفظ المنطقة' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'POST')).toBe(true);
    });
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      name: 'الإسكندرية',
      areas: ['EG-ALX'],
      rate_minor: 6000,
      cities: [{ name: 'سموحة', rate_minor: 4550 }],
    });
  });

  it('refuses an amount that is not one, before sending anything', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping`]: () => ({
        status: 200,
        body: { currency: 'EGP', areas, settings: { cod_fee: { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 }, version: 0 }, zones: [] },
      }),
    });
    renderAt(`/stores/${store}/shipping`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'منطقة جديدة' }));
    await user.type(screen.getByLabelText('اسم المنطقة'), 'x');
    await user.type(screen.getByLabelText('سعر الشحن'), 'خمسين');
    await user.click(screen.getByRole('button', { name: 'حفظ المنطقة' }));
    expect(await screen.findByText('سعر الشحن: اكتب مبلغًا صحيحًا')).toBeInTheDocument();
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it('saves how many days delivery takes, and shows them in the list', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping`]: () => ({
        status: 200,
        body: {
          currency: 'EGP', areas, settings: { cod_fee: { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 }, version: 0 },
          zones: [{ ...cairo, eta_min_days: 2, eta_max_days: 3 }],
        },
      }),
      [`PUT ${base}/shipping/zones/Z1`]: () => ({ status: 200, body: {} }),
    });
    renderAt(`/stores/${store}/shipping`);
    const user = userEvent.setup();
    expect(await screen.findByRole('cell', { name: '2–3 أيام' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'تعديل' }));
    const from = screen.getByLabelText('من (أيام)');
    const to = screen.getByLabelText('إلى (أيام)');
    expect(from).toHaveValue('2');
    await user.clear(from);
    await user.type(from, '1');
    await user.click(screen.getByRole('button', { name: 'حفظ المنطقة' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'PUT')).toBe(true);
    });
    expect(calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ eta_min_days: 1, eta_max_days: 3, version: 3 });
    expect(to).toHaveValue('3');
  });

  it('refuses delivery days that are not a range, before sending anything', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping`]: () => ({
        status: 200,
        body: { currency: 'EGP', areas, settings: { cod_fee: { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 }, version: 0 }, zones: [] },
      }),
    });
    renderAt(`/stores/${store}/shipping`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'منطقة جديدة' }));
    await user.type(screen.getByLabelText('اسم المنطقة'), 'x');
    await user.type(screen.getByLabelText('سعر الشحن'), '50');
    await user.click(screen.getByRole('checkbox', { name: 'الإسكندرية' }));
    for (const [from, to, msg] of [
      ['2', '', 'مدة التوصيل: اكتب أقل وأكثر عدد أيام معًا'],
      ['5', '2', 'مدة التوصيل: الأقل لا يزيد عن الأكثر، من 0 إلى 30 يومًا'],
      ['1', '45', 'مدة التوصيل: الأقل لا يزيد عن الأكثر، من 0 إلى 30 يومًا'],
    ] as const) {
      await user.clear(screen.getByLabelText('من (أيام)'));
      await user.clear(screen.getByLabelText('إلى (أيام)'));
      await user.type(screen.getByLabelText('من (أيام)'), from);
      if (to) await user.type(screen.getByLabelText('إلى (أيام)'), to);
      await user.click(screen.getByRole('button', { name: 'حفظ المنطقة' }));
      expect(await screen.findByText(msg)).toBeInTheDocument();
    }
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });
});

describe('Carriers', () => {
  it('connects an account and shows the webhook secret once', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping/carriers`]: () => ({ status: 200, body: { carriers: ['bosta'], connections: [] } }),
      [`POST ${base}/shipping/carriers`]: () => ({
        status: 201,
        body: {
          connection: { id: 'C1', carrier: 'bosta', status: 'active', created_at: '2026-10-24T10:00:00Z' },
          webhook_url: 'https://app.example.com/v1/webhooks/bosta/x/C1',
          webhook_secret: 'shown-once-secret',
        },
      }),
    });
    renderAt(`/stores/${store}/carriers`);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('مفتاح API من لوحة الشركة'), 'bosta-key');
    await user.click(screen.getByRole('button', { name: 'ربط' }));
    expect(await screen.findByText('shown-once-secret')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ carrier: 'bosta', api_key: 'bosta-key' });
    await user.click(screen.getByRole('button', { name: 'نسخته، إخفاء' }));
    expect(screen.queryByText('shown-once-secret')).not.toBeInTheDocument();
  });

  it('disconnects after asking', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping/carriers`]: () => ({
        status: 200,
        body: { carriers: ['bosta'], connections: [{ id: 'C1', carrier: 'bosta', status: 'active', created_at: '2026-10-24T10:00:00Z' }] },
      }),
      [`DELETE ${base}/shipping/carriers/C1`]: () => ({ status: 204 }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/carriers`);
    await userEvent.setup().click(await screen.findByRole('button', { name: 'فصل' }));
    await waitFor(() => {
      expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
    });
  });
});

describe('Shipments', () => {
  it('lists shipments and filters by status through the URL', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping/shipments`]: (c) => ({
        status: 200,
        body: { items: c.search.get('status') === 'delivered' ? [] : [shipment()] },
      }),
    });
    renderAt(`/stores/${store}/shipments`);
    const user = userEvent.setup();
    expect(await screen.findByRole('link', { name: '7788' })).toHaveAttribute('href', `/stores/${store}/shipments/S1`);
    expect(screen.getByText('بوسطة')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'الكل' })).toHaveAttribute('aria-selected', 'true');
    await user.click(screen.getByRole('tab', { name: 'سُلّمت' }));
    expect(await screen.findByText('لا توجد شحنات بهذه الحالة.')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'سُلّمت' })).toHaveAttribute('aria-selected', 'true');
    expect(calls.at(-1)?.search.get('status')).toBe('delivered');
  });

  it('cancels a shipment the carrier has not picked up', async () => {
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/shipping/shipments/S1`]: () => ({
        status: 200,
        body: { shipment: shipment(), events: [{ id: 'E1', status: 'created', source: 'api', note: '', at: '2026-10-24T10:00:00Z' }] },
      }),
      [`POST ${base}/shipping/shipments/S1/cancel`]: () => ({ status: 200, body: shipment({ cancel_requested_at: '2026-10-24T11:00:00Z' }) }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderAt(`/stores/${store}/shipments/S1`);
    expect(await screen.findByRole('button', { name: 'طباعة البوليصة' })).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole('button', { name: 'إلغاء الشحنة' }));
    await waitFor(() => {
      expect(calls.some((c) => c.path.endsWith('/cancel'))).toBe(true);
    });
  });
});

describe('OrderShipping', () => {
  it('hands a ready order to the connected carrier', async () => {
    const order = {
      id: 'O1', code: 'P0R1S2T3', status: 'ready_to_ship', version: 4, source: 'storefront', customer_id: 'CU', name: 'منى أحمد',
      phone: '+201012345678', governorate: 'EG-C', city: 'مدينة نصر', address: '12 شارع', notes: '', currency: 'EGP', subtotal_minor: 45000,
      shipping_minor: 5000, cod_fee_minor: 2000, payment_method: 'cod', payment_status: 'unpaid', total_minor: 52000, item_count: 1,
      created_at: '2026-10-24T10:00:00Z', updated_at: '2026-10-24T10:00:00Z', status_changed_at: '2026-10-24T10:00:00Z',
    };
    let shipped = false;
    const calls = mockAPI({
      ...storeRoute,
      [`GET ${base}/orders/O1`]: () => ({ status: 200, body: { order, lines: [], events: [] } }),
      [`GET ${base}/shipping/shipments`]: () => ({ status: 200, body: { items: shipped ? [shipment({ status: 'requested' })] : [] } }),
      [`GET ${base}/shipping/carriers`]: () => ({
        status: 200,
        body: { carriers: ['bosta'], connections: [{ id: 'C1', carrier: 'bosta', status: 'active', created_at: '2026-10-24T10:00:00Z' }] },
      }),
      [`POST ${base}/shipping/shipments`]: () => {
        shipped = true;
        return { status: 200, body: { results: [{ order_id: 'O1', shipment: shipment({ status: 'requested' }) }] } };
      },
    });
    renderAt(`/stores/${store}/orders/O1`);
    await userEvent.setup().click(await screen.findByRole('button', { name: 'إنشاء شحنة مع بوسطة' }));
    expect(await screen.findByText('قيد الإنشاء')).toBeInTheDocument();
    expect(calls.find((c) => c.method === 'POST' && c.path.endsWith('/shipments'))?.body).toEqual({ connection_id: 'C1', order_ids: ['O1'] });
    expect(screen.queryByRole('button', { name: /إنشاء شحنة/ })).not.toBeInTheDocument();
  });
});
