import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { contrast, sameSettings, type ThemeSettings } from '../lib/theme';
import { SessionContext, type Session } from '../session';
import { mockAPI, type Call } from '../test/api';
import { Design } from './Design';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

const live: ThemeSettings = {
  colors: { primary: '#0f2e52', accent: '#15803d', background: '#ffffff', text: '#0f172a' }, font: 'ibm-plex-sans-arabic', radius: 'medium', look: 'classic', low_stock: 5,
};

function session(): Session {
  return {
    state: { kind: 'signed-in', me: { user: { id: 'u', email: 'a@b.c', name: 'منى', email_verified: true, mfa_enabled: false, created_at: '' }, memberships: [{ store_id: store, role: 'owner' }] } },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function routes(draft?: ThemeSettings, over: Record<string, (c: Call) => { status: number; body?: unknown }> = {}) {
  return mockAPI({
    [`GET ${base}`]: () => ({ status: 200, body: { id: store, name: 'متجر منى', slug: 'mona', currency: 'EGP' } }),
    [`GET ${base}/orders/counts`]: () => ({ status: 200, body: { pending: 0 } }),
    [`GET ${base}/storefront/theme`]: () => ({ status: 200, body: { settings: live, version: 3 } }),
    [`GET ${base}/storefront/draft`]: () => ({ status: 200, body: draft ? { theme: draft, updated_at: '' } : {} }),
    [`GET ${base}/storefront/logo`]: () => ({ status: 200, body: {} }),
    [`GET ${base}/storefront/home`]: () => ({
      status: 200,
      body: { kind: 'home', slug: 'home', title: '', description: '', body: '', status: 'published', version: 1, sections: [{ id: 'h', type: 'hero', settings: { heading: 'تشكيلة الشتاء وصلت', button_label: 'تسوّق الآن' } }] },
    }),
    [`GET ${base}/categories`]: () => ({ status: 200, body: { items: [{ id: 'c', name: 'تيشيرتات', slug: 't', position: 0, created_at: '', updated_at: '' }] } }),
    [`GET ${base}/products`]: () => ({ status: 200, body: { items: [] } }),
    [`PUT ${base}/storefront/draft/theme`]: (c) => ({ status: 200, body: { theme: c.body, updated_at: '' } }),
    ...over,
  });
}

function renderDesign() {
  return render(
    <SessionContext.Provider value={session()}>
      <MemoryRouter initialEntries={[`/stores/${store}/design`]}>
        <Routes>
          <Route path="/stores/:storeId/design" element={<Design />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const drafts = (calls: Call[]) => calls.filter((c) => c.method === 'PUT' && c.path.endsWith('/draft/theme'));

describe('Design', () => {
  it('saves edits as a draft, refuses unreadable colours, and publishes over the version read', async () => {
    const calls = routes(undefined, {
      [`POST ${base}/storefront/draft/theme/publish`]: () => ({ status: 200, body: { settings: { ...live, look: 'soft' }, version: 4 } }),
    });
    renderDesign();
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { level: 1, name: 'التصميم' })).toBeInTheDocument();
    expect(screen.queryByText('تغييرات لم تُنشر')).toBeNull();
    for (const b of screen.getAllByRole('button', { name: 'نشر' })) expect(b).toBeDisabled(); // nothing to publish yet (head and phone bar)
    expect(screen.getByText(/النص واضح على الخلفية \(التباين/)).toBeInTheDocument();
    expect(await screen.findByText('تشكيلة الشتاء وصلت')).toBeInTheDocument(); // the preview's hero

    await user.click(screen.getByRole('button', { name: /ناعم/ }));
    await waitFor(() => {
      expect(drafts(calls)).toHaveLength(1);
    }, { timeout: 2000 });
    expect(drafts(calls)[0]?.body).toMatchObject({ look: 'soft' });
    expect(await screen.findByText('حُفظت المسودة')).toBeInTheDocument();
    expect(screen.getByText('تغييرات لم تُنشر')).toBeInTheDocument();

    // text as light as the background: said at once, never sent
    const text = screen.getByLabelText('كود النص');
    await user.clear(text);
    await user.type(text, '#FAFAFA');
    expect(screen.getByRole('alert')).toHaveTextContent('النص غير واضح على الخلفية');
    await new Promise((r) => setTimeout(r, 900));
    expect(drafts(calls)).toHaveLength(1);
    await user.clear(text);
    await user.type(text, '#0F172A');

    const [publish] = screen.getAllByRole('button', { name: 'نشر' });
    if (!publish) throw new Error('no publish button');
    await user.click(publish);
    expect(text).toHaveValue('#0F172A'); // one "#", typed or not
    await waitFor(() => {
      expect(calls.find((c) => c.path.endsWith('/publish'))?.body).toEqual({ version: 3 });
    });
    await waitFor(() => {
      expect(screen.queryByText('تغييرات لم تُنشر')).toBeNull();
    });
  });

  it('starts from the draft, applies a ready palette, previews in a new tab and discards', async () => {
    const draft = { ...live, colors: { ...live.colors, primary: '#7f1d1d' } };
    const calls = routes(draft, {
      [`POST ${base}/storefront/previews`]: () => ({ status: 201, body: { token: 'tok-123', expires_at: '' } }),
      [`DELETE ${base}/storefront/draft/theme`]: () => ({ status: 204 }),
    });
    const tab = { opener: {}, location: { href: '' }, close: vi.fn() };
    const open = vi.spyOn(globalThis, 'open').mockReturnValue(tab as unknown as Window);
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    renderDesign();
    const user = userEvent.setup();
    expect(await screen.findByText('تغييرات لم تُنشر')).toBeInTheDocument();
    expect(screen.getByLabelText('كود اللون الأساسي')).toHaveValue('#7F1D1D');

    await user.click(screen.getByRole('button', { name: 'ألوان جاهزة' }));
    await user.click(within(screen.getByRole('group', { name: 'ألوان جاهزة' })).getByRole('button', { name: /فيروزي/ }));
    expect(screen.getByLabelText('كود اللون الأساسي')).toHaveValue('#0F766E');

    await user.click(screen.getByRole('button', { name: 'معاينة في تبويب جديد' }));
    await waitFor(() => {
      expect(tab.location.href).toBe('http://mona.iftahonline.localhost:18081/?preview=tok-123');
    });
    expect(open).toHaveBeenCalledWith('about:blank', '_blank');
    expect(tab.opener).toBeNull();
    // the preview shows what was on screen: the edit was saved first
    expect(drafts(calls).at(-1)?.body).toMatchObject({ colors: { primary: '#0f766e' } });

    await user.click(screen.getByRole('button', { name: 'تجاهل التغييرات' }));
    await waitFor(() => {
      expect(screen.getByLabelText('كود اللون الأساسي')).toHaveValue('#0F2E52');
    });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
  });

  it('shows the field the server refused', async () => {
    routes(undefined, {
      [`PUT ${base}/storefront/draft/theme`]: () => ({
        status: 422,
        body: { title: 'Unprocessable', status: 422, errors: [{ field: 'contact.whatsapp', code: 'invalid', message: 'not a phone number' }] },
      }),
    });
    renderDesign();
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText('واتساب'), '12');
    expect(await screen.findByText('not a phone number', {}, { timeout: 2000 })).toBeInTheDocument();
    expect(screen.getByText('لم تُحفظ المسودة: راجع الحقول المعلّمة')).toBeInTheDocument();
  });
});

describe('theme helpers', () => {
  it('measures contrast as the storefront does and compares themes as stored', () => {
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 0);
    expect(contrast('#0f172a', '#ffffff')).toBeGreaterThan(17);
    expect(contrast('#fafafa', '#ffffff')).toBeLessThan(1.1);
    expect(contrast('red', '#ffffff')).toBe(0);
    expect(sameSettings(live, { ...live, colors: { ...live.colors, primary: '#0F2E52' } })).toBe(true);
    expect(sameSettings(live, { ...live, look: 'soft' })).toBe(false);
  });
});
