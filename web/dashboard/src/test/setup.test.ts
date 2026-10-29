import { describe, expect, it } from 'vitest';

describe('test setup', () => {
  // A screen still loading when its test ends sends requests after the
  // test's fetch mock is restored: they must never reach the network.
  it('keeps a fetch outside any mock from leaving the process', async () => {
    const outcome = await Promise.race([
      fetch('/v1/stores').then(
        () => 'answered',
        () => 'failed',
      ),
      new Promise((r) => {
        setTimeout(() => {
          r('pending');
        }, 200);
      }),
    ]);
    expect(outcome).toBe('pending');
  });
});
