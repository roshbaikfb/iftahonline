import type { components } from '../api/client';

export type AdminHealth = components['schemas']['AdminHealth'];
export type StoreStatus = components['schemas']['Store']['status'];

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

const count = (n: number) => n.toLocaleString('en-US');

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

