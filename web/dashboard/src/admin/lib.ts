import { api, type components } from '../api/client';

export type AdminHealth = components['schemas']['AdminHealth'];
export type StoreStatus = components['schemas']['Store']['status'];
export type Duplicate = components['schemas']['AdminDuplicatePayment'];

// waiting are the duplicate payments not refunded yet.
export const waiting = (items: Duplicate[]) => items.filter((d) => !d.refunded_at);

export const storeStatusLabel: Record<StoreStatus, string> = {
  active: 'نشط',
  suspended: 'موقوف من المنصة',
  closed: 'مغلق',
};

export const sectionLabel: Record<AdminHealth['errors'][number], string> = {
  database: 'قاعدة البيانات',
  redis: 'Redis',
  stores: 'المتاجر',
  billing: 'الاشتراكات والفواتير',
  jobs: 'طابور المهام',
};

export const count = (n: number) => n.toLocaleString('en-US');

// healthGroups are the health page's counted sections, in the mockup's
// order; a section the server could not read is absent.
export function healthGroups(h: AdminHealth): { title: string; items: { label: string; value: string; warn?: boolean }[] }[] {
  const out = [];
  if (h.stores) {
    const s = h.stores;
    out.push({
      title: 'المتاجر',
      items: [
        { label: 'نشطة', value: count(s.active) },
        { label: 'موقوفة لفاتورة', value: count(s.held), warn: s.held > 0 },
        { label: 'موقوفة من المنصة', value: count(s.suspended) },
        { label: 'مغلقة', value: count(s.closed) },
        { label: 'الإجمالي', value: count(s.total) },
      ],
    });
  }
  if (h.billing) {
    const b = h.billing;
    out.push({
      title: 'الاشتراكات والفواتير',
      items: [
        { label: 'في التجربة', value: count(b.trialing) },
        { label: 'نشطة', value: count(b.active) },
        { label: 'متأخرة الدفع', value: count(b.past_due), warn: b.past_due > 0 },
        { label: 'فواتير مفتوحة', value: count(b.invoices_open) },
        { label: 'فواتير متأخرة', value: count(b.invoices_overdue), warn: b.invoices_overdue > 0 },
      ],
    });
  }
  if (h.jobs) {
    const j = h.jobs;
    out.push({
      title: 'طابور المهام',
      items: [
        { label: 'تنتظر', value: count(j.available) },
        { label: 'تعمل الآن', value: count(j.running) },
        { label: 'تُعاد', value: count(j.retryable), warn: j.retryable > 0 },
        { label: 'رُفضت آخر 24 ساعة', value: count(j.discarded_24h), warn: j.discarded_24h > 0 },
        { label: 'أقدم مهمة تنتظر', value: waited(j.oldest_available_seconds), warn: j.oldest_available_seconds > 300 },
      ],
    });
  }
  return out;
}

// waited is a short duration: 12 ث, 4 د, 2 س.
export function waited(seconds: number): string {
  if (seconds < 60) return `${String(seconds)} ث`;
  if (seconds < 3600) return `${String(Math.round(seconds / 60))} د`;
  return `${String(Math.round(seconds / 3600))} س`;
}

export function dateTimeAr(iso: string): string {
  return new Date(iso).toLocaleString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', hour: 'numeric', minute: '2-digit' });
}

// details are an admin log entry's metadata as one line, its reason first.
export function details(meta: Record<string, unknown>): string {
  const entries = Object.entries(meta);
  entries.sort(([a], [b]) => (a === 'reason' ? -1 : b === 'reason' ? 1 : a.localeCompare(b)));
  return entries.map(([k, v]) => `${k}: ${typeof v === 'string' ? v : JSON.stringify(v)}`).join(' · ');
}


export interface StoreFilter {
  query: string;
  status: StoreStatus | '';
}

// fetchStores reads a page of the store directory, newest first.
export function fetchStores(f: StoreFilter, after = '', limit?: number) {
  const query: { query?: string; status?: StoreStatus; cursor?: string; limit?: number } = {};
  if (f.query) query.query = f.query;
  if (f.status) query.status = f.status;
  if (after) query.cursor = after;
  if (limit) query.limit = limit;
  return api.GET('/admin/v1/stores', { params: { query } });
}

// Attention is one problem on the console's home, in words, with where to
// act when the console has a place for it.
export interface Attention {
  key: string;
  tone: 'red' | 'amber';
  title: string;
  body: string;
  link?: { href: string; label: string };
}

// attention turns the health check and the payments waiting for a
// refund into the home's «يحتاج انتباهك» cards, the urgent first. None
// means everything works.
export function attention(h: AdminHealth, duplicates: number): Attention[] {
  const out: Attention[] = [];
  const red = (key: string, title: string, body: string, link?: Attention['link']) => out.push({ key, tone: 'red', title, body, ...(link ? { link } : {}) });
  const amber = (key: string, title: string, body: string, link?: Attention['link']) => out.push({ key, tone: 'amber', title, body, ...(link ? { link } : {}) });
  if (!h.database) red('database', 'قاعدة البيانات لا ترد', 'الكونسول لا يصل إليها، وأرقام هذه الصفحة ناقصة. إن استمر ذلك فالمتاجر لا تستقبل الطلبات أيضًا.');
  if (!h.redis) red('redis', 'Redis لا يرد', 'السلات وحدود الطلبات تعتمد عليه، فقد تتعطل الإضافة إلى السلة في المتاجر حتى يعود.');
  if (duplicates > 0)
    red('duplicates', `دفعات زائدة (${count(duplicates)})`, 'تجار دفعوا فاتورة مدفوعة بالفعل. استردّ المبلغ من بوابة الدفع ثم سجّله.', { href: '/billing?tab=payments&outcome=duplicate', label: 'راجعها' });
  const j = h.jobs;
  if (j && j.discarded_24h > 0) red('discarded', `مهام فشلت نهائيًا (${count(j.discarded_24h)})`, 'فشلت خلال آخر 24 ساعة ولن تُعاد. راجع سجلات العامل (worker).');
  const unread = h.errors.filter((e) => e !== 'database' && e !== 'redis');
  if (unread.length > 0) amber('unread', `تعذّرت قراءة: ${unread.map((e) => sectionLabel[e]).join('، ')}`, 'باقي الصفحة يعمل. حدّثها بعد قليل.');
  if (h.stores && h.stores.held > 0) amber('held', `متاجر موقوفة لفاتورة (${count(h.stores.held)})`, 'فاتورة متأخرة أوقفتها. إن وصلك تحويل من أحدها فسوِّه من صفحة المتجر.');
  const b = h.billing;
  if (b && b.invoices_overdue > 0) amber('overdue', `فواتير متأخرة (${count(b.invoices_overdue)})`, 'تجاوزت موعد الدفع. يتوقف المتجر إن انتهت فترة السماح دون دفع.', {
      href: '/billing?status=overdue',
      label: 'اعرضها',
    });
  if (b && b.past_due > 0) amber('past_due', `اشتراكات متأخرة الدفع (${count(b.past_due)})`, 'في فترة السماح الآن، ولم تُدفع فاتورتها بعد.');
  if (j && j.oldest_available_seconds > 300) amber('slow', 'طابور المهام متأخر', `أقدم مهمة تنتظر منذ ${waited(j.oldest_available_seconds)}. تأكد أن العامل (worker) يعمل.`);
  if (j && j.retryable > 0) amber('retryable', `مهام تُعاد (${count(j.retryable)})`, 'فشلت مؤقتًا وتُعاد تلقائيًا، وغالبًا خدمة خارجية لا ترد.');
  return out;
}

// greeting is the home's hello by the hour.
export function greeting(now: Date): string {
  return now.getHours() < 12 ? 'صباح الخير' : 'مساء الخير';
}

// initial is a store's first letter, for its mark.
export function initial(name: string): string {
  return name.trim().charAt(0);
}
