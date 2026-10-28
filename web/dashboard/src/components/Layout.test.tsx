import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { storefrontURL } from '../lib/storefront';
import { SessionContext, type Session } from '../session';
import { mockAPI } from '../test/api';
import { Layout } from './Layout';

const a = '01M5STORE0000000000000000A';
const b = '01M5STORE0000000000000000B';

function session(stores: string[]): Session {
  return {
    state: {
      kind: 'signed-in',
      me: {
        user: { id: 'u', email: 'mona@example.com', name: 'منى أحمد', email_verified: true, mfa_enabled: false, created_at: '' },
        memberships: stores.map((s) => ({ store_id: s, role: 'owner' as const })),
      },
    },
    reload: vi.fn(() => Promise.resolve()),
    signOut: vi.fn(() => Promise.resolve()),
  };
}

function Where() {
  return <p data-testid="where">{useLocation().pathname}</p>;
}

function renderAt(path: string, s: Session) {
  mockAPI({
    [`GET /v1/stores/${a}`]: () => ({ status: 200, body: { id: a, name: 'متجر منى', slug: 'mona' } }),
    [`GET /v1/stores/${b}`]: () => ({ status: 200, body: { id: b, name: 'متجر آخر', slug: 'other' } }),
  });
  return render(
    <SessionContext.Provider value={s}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route
            path="/stores/:storeId/*"
            element={
              <Layout storeId={path.split('/')[2] ?? a}>
                <Where />
              </Layout>
            }
          />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

describe('Layout', () => {
  it('groups the sidebar as the mockup, marks the current screen and greys what is to come', async () => {
    renderAt(`/stores/${a}/orders/01M5ORDER000000000000000001`, session([a]));
    const nav = screen.getByRole('navigation', { name: 'القائمة الرئيسية' });
    const titles = [...nav.querySelectorAll('.group-title')].map((el) => el.textContent);
    expect(titles).toEqual(['المبيعات', 'المنتجات', 'الشحن', 'المتجر', 'الإعدادات']);
    // an order's page belongs to the orders screen
    expect(within(nav).getByRole('link', { name: 'الطلبات' })).toHaveAttribute('aria-current', 'page');
    expect(within(nav).getByRole('link', { name: 'الشحنات' })).not.toHaveAttribute('aria-current');
    expect(within(nav).getByRole('link', { name: 'الدفع بالبطاقة' })).toHaveAttribute('href', `/stores/${a}/gateways`);
    // a screen not built yet is not a link, and says so
    expect(within(nav).queryByRole('link', { name: /الدومينات/ })).toBeNull();
    expect(within(nav).getByRole('link', { name: 'التصميم' })).toHaveAttribute('href', `/stores/${a}/design`);
    expect(within(nav).getByRole('link', { name: 'الصفحة الرئيسية' })).toHaveAttribute('href', `/stores/${a}/home-page`);
    expect(within(nav).getByRole('link', { name: 'الصفحات' })).toHaveAttribute('href', `/stores/${a}/pages`);
    expect(within(nav).getByRole('link', { name: 'التصنيفات' })).toHaveAttribute('href', `/stores/${a}/categories`);
    expect(within(nav).getByRole('link', { name: 'المنتجات' })).toHaveAttribute('href', `/stores/${a}/products`);
    const soon = within(nav).getByText('الدومينات').closest('.item');
    expect(soon).toHaveAttribute('aria-disabled', 'true');
    expect(soon).toHaveTextContent('قريبًا');
    expect(await screen.findByText('متجر منى')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'عرض المتجر' })).toHaveAttribute('href', 'http://mona.iftahonline.localhost:18081');
  });

  it('marks only the screen whose address it is, not one that shares its start', () => {
    renderAt(`/stores/${a}/home-page`, session([a]));
    const nav = screen.getByRole('navigation', { name: 'القائمة الرئيسية' });
    expect(within(nav).getByRole('link', { name: 'الصفحة الرئيسية' })).toHaveAttribute('aria-current', 'page');
    expect(within(nav).getByRole('link', { name: 'الرئيسية' })).not.toHaveAttribute('aria-current');
  });

  it('switches stores and signs out from the user menu', async () => {
    const s = session([a, b]);
    renderAt(`/stores/${a}/payments`, s);
    const user = userEvent.setup();
    await user.selectOptions(await screen.findByLabelText('المتجر'), b);
    expect(screen.getByTestId('where')).toHaveTextContent(`/stores/${b}/home`);
    await user.click(screen.getByLabelText('حسابي: منى أحمد'));
    expect(screen.getByText('mona@example.com')).toBeVisible();
    await user.click(screen.getByRole('button', { name: 'تسجيل الخروج' }));
    expect(s.signOut).toHaveBeenCalledOnce();
  });

  it('opens the sidebar as a drawer and closes it by Escape, the backdrop or a link', async () => {
    renderAt(`/stores/${a}/orders`, session([a]));
    const user = userEvent.setup();
    const toggle = screen.getByRole('button', { name: 'القائمة' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await user.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await user.keyboard('{Escape}');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await user.click(toggle);
    await user.click(screen.getByRole('button', { name: 'إغلاق القائمة' }));
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await user.click(toggle);
    await user.click(screen.getByRole('link', { name: 'الشحنات' }));
    await waitFor(() => {
      expect(toggle).toHaveAttribute('aria-expanded', 'false');
    });
    expect(screen.getByTestId('where')).toHaveTextContent(`/stores/${a}/shipments`);
  });
});

describe('storefrontURL', () => {
  it('puts the store slug in the address', () => {
    expect(storefrontURL('mona', 'https://{slug}.iftahonline.com')).toBe('https://mona.iftahonline.com');
    expect(storefrontURL('a b', 'https://{slug}.x')).toBe('https://a%20b.x');
  });
});
