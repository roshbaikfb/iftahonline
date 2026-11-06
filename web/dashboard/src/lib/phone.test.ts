import { describe, expect, it } from 'vitest';

import { phoneExample } from './orders';

describe("the phone search's hint (D1)", () => {
  it("follows the store's country", () => {
    expect(phoneExample('EG')).toBe('01012345678');
    expect(phoneExample('SA')).toBe('0501234567');
    expect(phoneExample('AE')).toBe('0501234567');
    expect(phoneExample(undefined)).toBe('01012345678');
  });
});
