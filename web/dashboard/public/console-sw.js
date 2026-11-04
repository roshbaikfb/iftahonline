// The console's service worker (ADR-0080): it shows the alerts the server
// pushes, and a click opens the console on the alert's page. It sits at the
// root so its scope can be the whole console (/console and below).

self.addEventListener('install', () => {
  void self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

// A page path inside the console, never another site.
const pageOf = (path) => (typeof path === 'string' && path.startsWith('/') && !path.startsWith('//') ? path : '/alerts');

self.addEventListener('push', (event) => {
  let note;
  try {
    note = event.data ? event.data.json() : {};
  } catch {
    note = {};
  }
  event.waitUntil(
    self.registration.showNotification(typeof note.title === 'string' ? note.title : 'الكونسول', {
      body: typeof note.body === 'string' ? note.body : '',
      tag: typeof note.tag === 'string' ? note.tag : undefined,
      lang: 'ar',
      dir: 'rtl',
      icon: '/console-icon-192.png',
      data: { path: pageOf(note.path) },
    }),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = new URL('/console' + pageOf(event.notification.data && event.notification.data.path), self.location.origin).href;
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((windows) => {
      const open = windows.find((w) => new URL(w.url).pathname.startsWith('/console'));
      if (open) return open.navigate(url).then((w) => (w || open).focus());
      return self.clients.openWindow(url);
    }),
  );
});
