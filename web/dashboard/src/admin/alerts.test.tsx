import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { testAdmin } from '../test/admin';
import { mockAPI, type Call } from '../test/api';
import { keyBytes, lasted, otherDevices, type Alert } from './alerts';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession, type AdminState } from './session';

const admin = testAdmin({ email: 'owner@iftahonline.com' });
const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString();

const duplicates: Alert = {
  id: '01M5ALERT00000000000000001',
  kind: 'duplicates',
  severity: 'red',
  value: 1,
  title: 'دفعات زائدة تحتاج استردادًا (1)',
  body: 'تجار دفعوا فاتورة مدفوعة بالفعل.',
  path: '/billing?tab=payments&outcome=duplicate',
  opened_at: minutesAgo(25),
  last_seen_at: minutesAgo(1),
};
const overdue: Alert = { ...duplicates, id: '01M5ALERT00000000000000002', kind: 'overdue', severity: 'amber', value: 6, title: 'فواتير متأخرة (6)', path: '/billing?status=overdue', opened_at: minutesAgo(2 * 24 * 60) };
const slow: Alert = { ...duplicates, id: '01M5ALERT00000000000000003', kind: 'slow', title: 'طابور المهام متأخر', opened_at: minutesAgo(200), resolved_at: minutesAgo(180) };

function renderAt(path = '/alerts') {
  const state: AdminState = { kind: 'signed-in', admin };
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={[path]}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function routes(over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    'GET /admin/v1/alerts': () => ({ status: 200, body: { open: [duplicates, overdue], resolved: [slow], email: true } }),
    'GET /admin/v1/push': () => ({ status: 200, body: { public_key: 'BPub_-', devices: 2 } }),
    'PUT /admin/v1/alerts/email': () => ({ status: 204 }),
    'POST /admin/v1/push/subscriptions': () => ({ status: 204 }),
    'POST /admin/v1/push/subscriptions/remove': () => ({ status: 204 }),
    ...over,
  });
}

// browserPush gives jsdom the Push API: a service worker whose push
// manager hands out one subscription, and the permission the admin gives.
function browserPush(permission: NotificationPermission = 'default', answer: NotificationPermission = 'granted') {
  let current: PushSubscription | null = null;
  const unsubscribe = vi.fn(() => {
    current = null;
    return Promise.resolve(true);
  });
  const sub = {
    endpoint: 'https://fcm.googleapis.com/fcm/send/abc',
    toJSON: () => ({ endpoint: 'https://fcm.googleapis.com/fcm/send/abc', keys: { p256dh: 'P256', auth: 'AUTH' } }),
    unsubscribe,
  } as unknown as PushSubscription;
  const pushManager = {
    getSubscription: () => Promise.resolve(current),
    subscribe: vi.fn(() => {
      current = sub;
      return Promise.resolve(sub);
    }),
  };
  const reg = { pushManager };
  const register = vi.fn(() => Promise.resolve(reg));
  vi.stubGlobal('PushManager', vi.fn());
  vi.stubGlobal('Notification', { permission, requestPermission: vi.fn(() => Promise.resolve(answer)) });
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: { register, ready: Promise.resolve(reg), getRegistration: () => Promise.resolve(current ? reg : undefined) },
  });
  return { register, pushManager, sub, unsubscribe };
}

afterEach(() => {
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, 'serviceWorker');
});

describe('alert helpers', () => {
  it('names spans and devices the Arabic way', () => {
    expect(lasted(25 * 60_000)).toBe('25 دقيقة');
    expect(lasted(2 * 3600_000)).toBe('ساعتين');
    expect(lasted(3 * 86400_000)).toBe('3 أيام');
    expect(lasted(10_000)).toBe('دقيقة');
    expect(otherDevices(0)).toBe('');
    expect(otherDevices(2)).toBe('مفعّلة على جهازين آخرين');
  });

  it('reads a base64url key into bytes', () => {
    expect([...keyBytes('AQID_-8')]).toEqual([1, 2, 3, 255, 239]);
  });
});

describe('console alerts', () => {
  it('lists the open alerts with their pages, and those resolved lately', async () => {
    routes();
    renderAt();

    const open = await screen.findByRole('region', { name: /مفتوحة الآن/ });
    const cards = within(open).getAllByRole('listitem');
    expect(cards).toHaveLength(2);
    expect(cards[0]).toHaveClass('red');
    expect(cards[0]).toHaveTextContent('مفتوح منذ 25 دقيقة');
    expect(within(open).getByRole('link', { name: 'راجعها' })).toHaveAttribute('href', '/billing?tab=payments&outcome=duplicate');
    expect(within(open).getByRole('link', { name: 'اعرضها' })).toHaveAttribute('href', '/billing?status=overdue');
    expect(cards[1]).toHaveTextContent('مفتوح منذ يومين');
    expect(screen.getByRole('region', { name: 'حُلّت آخر 30 يومًا' })).toHaveTextContent(/طابور المهام متأخر.*استمر 20 دقيقة/);
  });

  it('shows the open count on the bell, in the menu and the top bar', async () => {
    routes();
    renderAt('/');

    expect(await screen.findByRole('link', { name: 'التنبيهات، 2 مفتوحة' })).toHaveAttribute('href', '/alerts');
    const menu = screen.getByRole('navigation', { name: 'قائمة الكونسول' });
    expect(within(menu).getByRole('link', { name: /التنبيهات/ })).toHaveTextContent('2');
  });

  it('says all is well with nothing open, and the bell shows no count', async () => {
    routes({ 'GET /admin/v1/alerts': () => ({ status: 200, body: { open: [], resolved: [], email: true } }) });
    renderAt();

    expect(await screen.findByText('لا تنبيهات مفتوحة. كل شيء يعمل.')).toBeInTheDocument();
    expect(screen.getByText('لا شيء حُلّ في آخر 30 يومًا.')).toBeInTheDocument();
    expect(screen.getAllByRole('link', { name: 'التنبيهات' })).toHaveLength(2);
    expect(screen.queryByRole('link', { name: /مفتوحة/ })).not.toBeInTheDocument();
  });

  it('turns the alert emails off, and back when the server refuses', async () => {
    let refuse = false;
    const calls = routes({ 'PUT /admin/v1/alerts/email': () => (refuse ? { status: 500, body: { title: 'boom', status: 500 } } : { status: 204 }) });
    renderAt();

    const box = await screen.findByRole('checkbox', { name: /بالإيميل/ });
    expect(box).toHaveAccessibleName(/owner@iftahonline.com/);
    await waitFor(() => {
      expect(box).toBeChecked();
    });
    await userEvent.click(box);
    expect(box).not.toBeChecked();
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ enabled: false });

    refuse = true;
    await userEvent.click(box);
    await waitFor(() => {
      expect(box).not.toBeChecked();
    });
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('turns notifications on in this device with the server key, then off', async () => {
    const calls = routes();
    const { register, pushManager, sub, unsubscribe } = browserPush();
    renderAt();

    const on = await screen.findByRole('button', { name: 'فعّل على هذا الجهاز' });
    expect(screen.getByText('مفعّلة على جهازين آخرين')).toBeInTheDocument();
    await userEvent.click(on);

    expect(await screen.findByRole('button', { name: 'أوقف على هذا الجهاز' })).toBeInTheDocument();
    expect(register).toHaveBeenCalledWith('/console-sw.js', { scope: '/console' });
    expect(pushManager.subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: keyBytes('BPub_-') });
    expect(calls.find((c) => c.path === '/admin/v1/push/subscriptions')?.body).toEqual({
      endpoint: 'https://fcm.googleapis.com/fcm/send/abc',
      keys: { p256dh: 'P256', auth: 'AUTH' },
    });
    expect(screen.getByText('مفعّلة على هذا الجهاز')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'أوقف على هذا الجهاز' }));
    expect(await screen.findByRole('button', { name: 'فعّل على هذا الجهاز' })).toBeInTheDocument();
    expect(calls.find((c) => c.path === '/admin/v1/push/subscriptions/remove')?.body).toEqual({ endpoint: sub.endpoint });
    expect(unsubscribe).toHaveBeenCalled();
  });

  it('subscribes nothing when the admin refuses the permission', async () => {
    const calls = routes();
    const { pushManager } = browserPush('default', 'denied');
    renderAt();

    await userEvent.click(await screen.findByRole('button', { name: 'فعّل على هذا الجهاز' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('محظورة');
    expect(pushManager.subscribe).not.toHaveBeenCalled();
    expect(calls.some((c) => c.path === '/admin/v1/push/subscriptions')).toBe(false);
  });

  it.each<[string, () => void, RegExp]>([
    ['the server has no keys', () => browserPush(), /غير مفعّلة على الخادم/],
    ['the browser blocked them', () => browserPush('denied'), /محظورة لهذا الموقع/],
    ['an iPhone outside the home screen', () => vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (iPhone; CPU iPhone OS 17_4)'), /الشاشة الرئيسية»/],
    ['a browser without push', () => undefined, /لا يدعم الإشعارات/],
  ])('offers no button when %s', async (name, setup, text) => {
    routes(name === 'the server has no keys' ? { 'GET /admin/v1/push': () => ({ status: 200, body: { devices: 0 } }) } : {});
    setup();
    renderAt();

    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'فعّل على هذا الجهاز' })).not.toBeInTheDocument();
  });
});
