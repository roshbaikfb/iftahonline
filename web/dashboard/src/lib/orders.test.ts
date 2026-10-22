import { describe, expect, it } from 'vitest';

import { money, nextSteps, statuses } from './orders';

describe('orders helpers', () => {
  it('formats money from minor units', () => {
    expect(money(34950, 'EGP')).toBe('349.50 ج.م');
    expect(money(1500000, 'EGP')).toBe('15,000 ج.م');
    expect(money(24900, 'AED')).toBe('249 د.إ');
    expect(money(100, 'XYZ')).toBe('1 XYZ');
  });

  it('knows every status and keeps final states final', () => {
    expect(statuses).toHaveLength(8);
    expect(nextSteps.cancelled).toEqual([]);
    expect(nextSteps.returned).toEqual([]);
    expect(nextSteps.shipped).not.toContain('cancelled');
  });
});
