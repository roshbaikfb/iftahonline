import type { components } from '../api/client';
import type { Order } from './orders';

export type Customer = components['schemas']['Customer'];

// initials are a name's first letters (two words at most), for its
// round badge.
export function initials(name: string): string {
  return name
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w.charAt(0))
    .join('');
}

// telLink and waLink call or message a customer's number (E.164).
export const telLink = (phone: string) => `tel:${phone}`;
export const waLink = (phone: string) => `https://wa.me/${phone.replace(/^\+/, '')}`;

// ordersRead is how many of a customer's orders the page reads to sum
// them up (the approved plan: the latest 100).
export const ordersRead = 100;

export interface OrdersSummary {
  delivered: number;
  failed: number; // refused or returned
  deliveredMinor: number; // what the delivered orders were paid
}

// summarize sums up a customer's orders: those delivered and what they
// were paid, those that came back.
export function summarize(orders: Order[]): OrdersSummary {
  const out: OrdersSummary = { delivered: 0, failed: 0, deliveredMinor: 0 };
  for (const o of orders) {
    if (o.status === 'delivered') {
      out.delivered++;
      out.deliveredMinor += o.total_minor;
    } else if (o.status === 'returned' || o.status === 'failed_delivery') {
      out.failed++;
    }
  }
  return out;
}
