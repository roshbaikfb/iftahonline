import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { BuildInfo } from './BuildInfo';

function mockFetch(status: number, body: unknown, contentType = 'application/json') {
  return vi.spyOn(globalThis, 'fetch').mockResolvedValue(
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': contentType } }),
  );
}

describe('BuildInfo', () => {
  it('shows build info from the API', async () => {
    const fetchSpy = mockFetch(200, { version: 'v1.2.3', commit: 'abc', go_version: 'go1.27.2', role: 'api' });
    render(<BuildInfo />);
    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(await screen.findByText('v1.2.3')).toBeInTheDocument();
    expect(screen.getByText('go1.27.2')).toBeInTheDocument();
    const request = fetchSpy.mock.calls[0]?.[0];
    expect(request instanceof Request ? new URL(request.url).pathname : '').toBe('/v1/meta/version');
  });

  it('shows the problem detail on error', async () => {
    mockFetch(
      503,
      { type: 'urn:iftahonline:problem:unavailable', title: 'Service Unavailable', status: 503, detail: 'try later' },
      'application/problem+json',
    );
    render(<BuildInfo />);
    expect(await screen.findByRole('alert')).toHaveTextContent('try later');
  });

  it('shows a network failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('network down'));
    render(<BuildInfo />);
    expect(await screen.findByRole('alert')).toHaveTextContent('network down');
  });
});
