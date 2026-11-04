import { useEffect, useState } from 'react';

import { api, type components } from '../api/client';
import { count as name } from '../lib/home';

export type Alert = components['schemas']['AdminAlert'];
export type Alerts = components['schemas']['AdminAlerts'];
export type PushStatus = components['schemas']['AdminPushStatus'];

// The console's alerts (ADR-0080): what the server found, in its words,
// and the mobile notifications of this device.

// actionLabel is the alert's button, by what its page does.
export const actionLabel: Record<Alert['kind'], string> = {
  duplicates: 'راجعها',
  discarded: 'التفاصيل',
  slow: 'التفاصيل',
  redis: 'التفاصيل',
  overdue: 'اعرضها',
  held: 'اعرضها',
  past_due: 'اعرضها',
};

// lasted is a span in the largest whole unit: «25 دقيقة», «ساعتين», «3 أيام».
export function lasted(ms: number): string {
  const minutes = Math.max(1, Math.round(ms / 60_000));
  if (minutes < 60) return name(minutes, 'دقيقة', 'دقيقتين', 'دقائق', 'دقيقة');
  const hours = Math.round(minutes / 60);
  if (hours < 24) return name(hours, 'ساعة', 'ساعتين', 'ساعات', 'ساعة');
  return name(Math.round(hours / 24), 'يوم', 'يومين', 'أيام', 'يومًا');
}

// otherDevices names the admin's devices with notifications on, besides
// this one: none is empty.
export function otherDevices(n: number): string {
  return n > 0 ? `مفعّلة على ${name(n, 'جهاز آخر', 'جهازين آخرين', 'أجهزة أخرى', 'جهازًا آخر')}` : '';
}

// openEvery is how often the bell asks again while the tab is seen.
export const openEvery = 60_000;

// useOpenAlerts is the bell's count: the open alerts of the admin's role,
// read on opening and every minute; none when it cannot be read.
export function useOpenAlerts(): number {
  const [open, setOpen] = useState(0);
  useEffect(() => {
    let live = true;
    const read = async () => {
      const { data } = await api.GET('/admin/v1/alerts');
      if (live && data) setOpen(data.open.length);
    };
    void read();
    const id = window.setInterval(() => {
      if (!document.hidden) void read();
    }, openEvery);
    return () => {
      live = false;
      window.clearInterval(id);
    };
  }, []);
  return open;
}

// The service worker that shows the notifications. It sits at the root so
// its scope can be the whole console (/console and below).
export const workerURL = '/console-sw.js';
export const workerScope = '/console';

// Why this device cannot take notifications, or '' when it can.
export type PushBlock = '' | 'iphone' | 'unsupported' | 'denied';

export function pushBlock(): PushBlock {
  const supported = 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  if (!supported) {
    // iOS gives web pages notifications only once added to the home screen.
    return /iPhone|iPad|iPod/.test(navigator.userAgent) ? 'iphone' : 'unsupported';
  }
  return Notification.permission === 'denied' ? 'denied' : '';
}

// thisDevice is this browser's subscription, if notifications are on here.
export async function thisDevice(): Promise<PushSubscription | null> {
  if (pushBlock() !== '') return null;
  const reg = await navigator.serviceWorker.getRegistration(workerScope);
  return reg ? reg.pushManager.getSubscription() : null;
}

// readPush is the server's key and the admin's device count (null when it
// cannot be read), and this device's subscription.
export async function readPush(): Promise<{ status: PushStatus | null; here: PushSubscription | null }> {
  const [{ data }, here] = await Promise.all([api.GET('/admin/v1/push'), thisDevice().catch(() => null)]);
  return { status: data ?? null, here };
}

// keyBytes is a base64url key as the bytes PushManager wants.
export function keyBytes(key: string): Uint8Array<ArrayBuffer> {
  const b64 = key.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (key.length % 4)) % 4);
  const raw = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

// turnOn asks for permission, subscribes this device with the server's
// key and records it; false when the admin refused the permission.
export async function turnOn(publicKey: string): Promise<boolean> {
  if ((await Notification.requestPermission()) !== 'granted') return false;
  await navigator.serviceWorker.register(workerURL, { scope: workerScope });
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) });
  const { endpoint, keys } = sub.toJSON();
  const { error } = await api.POST('/admin/v1/push/subscriptions', {
    body: { endpoint: endpoint ?? '', keys: { p256dh: keys?.['p256dh'] ?? '', auth: keys?.['auth'] ?? '' } },
  });
  if (error) {
    await sub.unsubscribe();
    throw new Error('الخادم لم يقبل هذا الجهاز');
  }
  return true;
}

// turnOff forgets this device on the server, then in the browser.
export async function turnOff(sub: PushSubscription): Promise<void> {
  const { error } = await api.POST('/admin/v1/push/subscriptions/remove', { body: { endpoint: sub.endpoint } });
  if (error) throw new Error('تعذّر الإيقاف');
  await sub.unsubscribe();
}
