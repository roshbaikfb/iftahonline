import { describe, expect, it } from 'vitest';

import { isPlanLimit, lockedFor, planLimitMessage } from './plan';
import { problemMessage } from './problem';

describe("the plan's refusals (D1)", () => {
  it('says them in Arabic, by feature, from the English the server sends', () => {
    const limit = { status: 403, code: 'plan_limit', detail: "the store's plan allows 50 products; upgrade the plan to add more" };
    const flag = { status: 403, code: 'plan_limit', detail: "the store's plan does not include coupons; upgrade the plan to use it" };
    expect(isPlanLimit(limit)).toBe(true);
    expect(isPlanLimit({ code: 'forbidden' })).toBe(false);
    expect(planLimitMessage(limit)).toBe('وصلت إلى حد باقتك في المنتجات (50). رقِّ باقتك من «الفوترة والباقة» لتضيف المزيد.');
    expect(problemMessage(flag)).toBe('باقتك الحالية لا تشمل الكوبونات. رقِّ باقتك من «الفوترة والباقة» لتستخدمها.');
    // A feature this dashboard does not know yet is still said in Arabic.
    expect(problemMessage({ code: 'plan_limit', detail: 'the store’s plan does not include sms_bot; …' })).toBe(
      'باقتك الحالية لا تشمل هذه الميزة. رقِّ باقتك من «الفوترة والباقة» لتستخدمها.',
    );
  });

  it('locks a flag the plan lacks and a count with no room, and nothing unknown', () => {
    const pf = { included: ['coupons' as const], full: ['staff' as const] };
    expect(lockedFor(pf, 'coupons')).toBe(false);
    expect(lockedFor(pf, 'pixels')).toBe(true);
    expect(lockedFor(pf, 'staff')).toBe(true);
    expect(lockedFor(pf, 'products')).toBe(false);
    expect(lockedFor(null, 'pixels')).toBe(false);
  });
});
