import type { components } from '../api/client';
import { money } from '../lib/orders';
import { invoiceStatusLabel, outcomeLabel, stateOf } from './billing';
import { initial, storeStatusLabel } from './lib';
import { roleLabel } from './team';

export type Result = components['schemas']['AdminSearchResult'];

// Hit is one row of the results: where it opens and what it shows.
export interface Hit {
  key: string;
  to: string;
  mark: string;
  title: string;
  sub: string;
  pill?: { text: string; cls: string } | undefined;
  ltr?: boolean;
}

// groupsOf turns a result into the palette's groups, in the mockup's
// order; an empty kind is left out. Each hit carries its place in the
// whole list, for the keyboard.
export function groupsOf(r: Result): { title: string; hits: (Hit & { index: number })[] }[] {
  const store = (s: Result['stores'][number]): Hit => ({
    key: `s${s.store.id}`,
    to: `/stores/${s.store.id}`,
    mark: initial(s.store.name),
    title: s.store.name,
    sub: s.primary_host,
    pill: { text: storeStatusLabel[s.store.status], cls: `store-${s.store.status}` },
  });
  const groups = [
    { title: 'متاجر صاحب هذا الرقم أو الإيميل', hits: r.owner_stores.map(store) },
    { title: 'المتاجر', hits: r.stores.map(store) },
    {
      title: 'الوكالات',
      hits: r.agencies.map((a) => ({ key: `a${a.id}`, to: `/agencies/${a.id}`, mark: initial(a.name), title: a.name, sub: `${String(a.stores)} متجر · ${a.currency}` })),
    },
    {
      title: 'الفواتير',
      hits: r.invoices.map((i) => {
        const st = stateOf(i);
        return {
          key: `i${i.id}`,
          to: `/billing/invoices/${i.id}`,
          mark: '#',
          title: i.number,
          sub: `${i.account_name} · ${money(i.total_minor, i.currency)}`,
          pill: { text: invoiceStatusLabel[st], cls: `inv-${st}` },
          ltr: true,
        };
      }),
    },
    {
      title: 'حركات الدفع',
      hits: r.payments.map((p) => ({
        key: `p${p.id}`,
        to: `/billing/invoices/${p.invoice_id}`,
        mark: '↯',
        title: `${p.reference ?? ''} · ${p.invoice_number}`,
        sub: `${p.account_name} · ${money(p.amount_minor, p.currency)}`,
        pill: { text: outcomeLabel[p.outcome], cls: `pay-${p.outcome}` },
      })),
    },
    {
      title: 'الأدمن',
      hits: r.admins.map((m) => ({ key: `m${m.id}`, to: '/team', mark: initial(m.name), title: m.name, sub: `${m.email} · ${roleLabel(m.role)}` })),
    },
  ];
  let index = 0;
  return groups
    .filter((g) => g.hits.length > 0)
    .map((g) => ({ title: g.title, hits: g.hits.map((h) => ({ ...h, index: index++ })) }));
}

// longEnough is a query the server takes: two characters or more.
export const longEnough = (q: string) => q.trim().length >= 2;

