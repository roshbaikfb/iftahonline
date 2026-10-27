import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { SamplesBanner } from '../components/SamplesBanner';
import type { Activity, Look, Onboarding } from '../lib/onboarding';
import { SessionContext, type Session } from '../session';
import { mockAPI } from '../test/api';
import { Setup } from './Setup';
import { Signup } from './Signup';

const store = '01M5STORE0000000000000000A';
const base = `/v1/stores/${store}`;

function signedOut(reload = vi.fn(() => Promise.resolve())): Session {
  return { state: { kind: 'signed-out' }, reload, signOut: vi.fn(() => Promise.resolve()) };
}

function Where() {
  return <p data-testid="where">{useLocation().pathname}</p>;
}

function renderAt(path: string, s: Session = signedOut()) {
  return render(
    <SessionContext.Provider value={s}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/signup" element={<Signup />} />
          <Route path="/stores/:storeId/setup" element={<Setup />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </SessionContext.Provider>,
  );
}

const shippingCfg = {
  currency: 'EGP',
  areas: [
    { code: 'EG-C', name_ar: 'القاهرة', name_en: 'Cairo' },
    { code: 'EG-GZ', name_ar: 'الجيزة', name_en: 'Giza' },
  ],
  settings: { cod_fee: { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 }, version: 2 },
  zones: [],
};

describe('Signup', () => {
  it('checks the address, and creates the account and store in the chosen country', async () => {
    const reload = vi.fn(() => Promise.resolve());
    const calls = mockAPI({
      'GET /v1/auth/slug-availability': (c) => {
        const slug = c.search.get('slug') ?? '';
        if (slug === 'admin') return { status: 200, body: { slug, available: false, reason: 'reserved' } };
        return { status: 200, body: { slug, available: true } };
      },
      'POST /v1/auth/signup': () => ({ status: 201, body: { user: { id: 'u' }, store_id: store, expires_at: '' } }),
    });
    renderAt('/signup', signedOut(reload));
    const user = userEvent.setup();
    expect(screen.getByRole('heading', { name: 'أنشئ متجرك في دقائق' })).toBeInTheDocument();
    expect(screen.getByText('الخطوة 1 من 6')).toBeInTheDocument();
    await user.type(screen.getByLabelText('اسمك'), 'منى أحمد');
    await user.type(screen.getByLabelText('البريد الإلكتروني'), 'mona@example.com');
    await user.type(screen.getByLabelText(/كلمة المرور/), 'correct horse battery');
    await user.click(screen.getByRole('button', { name: 'متابعة' }));

    expect(screen.getByText('الخطوة 2 من 6')).toBeInTheDocument();
    await user.type(screen.getByLabelText('اسم المتجر'), 'بيت منى');
    const slug = screen.getByLabelText(/^رابط المتجر/);
    await user.type(slug, 'admin');
    expect(await screen.findByText('هذا الرابط محجوز، اختر غيره.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'إنشاء المتجر' })).toBeDisabled();
    await user.clear(slug);
    await user.type(slug, 'Beit Mona');
    expect(await screen.findByText('الرابط متاح')).toBeInTheDocument();
    expect(slug).toHaveValue('beit-mona');
    await user.click(screen.getByRole('button', { name: /السعودية/ }));
    expect(screen.getByRole('button', { name: /السعودية/ })).toHaveAttribute('aria-pressed', 'true');
    await user.click(screen.getByRole('button', { name: 'إنشاء المتجر' }));

    await waitFor(() => {
      expect(reload).toHaveBeenCalled();
    });
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({
      name: 'منى أحمد',
      email: 'mona@example.com',
      password: 'correct horse battery',
      client: 'web',
      store: { name: 'بيت منى', slug: 'beit-mona', currency: 'SAR', locale: 'ar', timezone: 'Asia/Riyadh' },
    });
  });

  it('sends the merchant back to the account step when the email has an account', async () => {
    mockAPI({
      'GET /v1/auth/slug-availability': (c) => ({ status: 200, body: { slug: c.search.get('slug'), available: true } }),
      'POST /v1/auth/signup': () => ({
        status: 422,
        body: { title: 'Unprocessable', status: 422, errors: [{ field: 'email', code: 'taken', message: 'an account with this email already exists' }] },
      }),
    });
    renderAt('/signup');
    const user = userEvent.setup();
    await user.type(screen.getByLabelText('اسمك'), 'منى');
    await user.type(screen.getByLabelText('البريد الإلكتروني'), 'mona@example.com');
    await user.type(screen.getByLabelText(/كلمة المرور/), 'correct horse battery');
    await user.click(screen.getByRole('button', { name: 'متابعة' }));
    await user.type(screen.getByLabelText('اسم المتجر'), 'بيت منى');
    await user.type(screen.getByLabelText(/^رابط المتجر/), 'beit-mona');
    await screen.findByText('الرابط متاح');
    await user.click(screen.getByRole('button', { name: 'إنشاء المتجر' }));
    expect(await screen.findByText('يوجد حساب بهذا البريد. سجّل الدخول بدلًا من ذلك.')).toBeInTheDocument();
    expect(screen.getByText('الخطوة 1 من 6')).toBeInTheDocument();
    expect(screen.getByLabelText('البريد الإلكتروني')).toHaveValue('mona@example.com');
  });
});

describe('Setup', () => {
  it('takes the activity, look and colour, skips shipping, and shows the store ready', async () => {
    let state: Onboarding = { status: 'in_progress', template: { status: 'none' } };
    let polls = 0;
    const calls = mockAPI({
      [`GET ${base}/onboarding`]: () => {
        if (state.template.status === 'preparing' && ++polls >= 1) state = { ...state, template: { status: 'ready', version: 1 } };
        return { status: 200, body: state };
      },
      [`GET ${base}/shipping`]: () => ({ status: 200, body: shippingCfg }),
      [`GET ${base}`]: () => ({ status: 200, body: { id: store, slug: 'beit-mona', name: 'بيت منى' } }),
      [`PUT ${base}/onboarding/template`]: (c) => {
        const b = c.body as { activity: Activity; look: Look; color: string };
        state = { status: 'in_progress', activity: b.activity, look: b.look, color: b.color, template: { status: 'preparing' } };
        return { status: 202, body: state };
      },
      [`POST ${base}/onboarding/complete`]: () => ({ status: 200, body: { status: 'completed', template: { status: 'ready' } } }),
    });
    renderAt(`/stores/${store}/setup`);
    const user = userEvent.setup();

    expect(await screen.findByRole('heading', { name: 'ماذا تبيع؟' })).toBeInTheDocument();
    expect(screen.getByText('الخطوة 3 من 6')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'عطور' }));
    await user.click(screen.getByRole('button', { name: 'متابعة' }));

    expect(screen.getByRole('heading', { name: 'شكل متجرك' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /ناعم/ }));
    expect(screen.getByRole('button', { name: 'لون النشاط المقترح' })).toHaveAttribute('aria-pressed', 'true');
    await user.click(screen.getByRole('button', { name: 'متابعة' }));

    expect(screen.getByRole('heading', { name: 'التوصيل والدفع عند الاستلام' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'كل المحافظات (2)' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'تخطَّ، سأضبطه لاحقًا' }));

    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ activity: 'perfume', look: 'soft', color: '#92400e' });
    expect(calls.some((c) => c.path.endsWith('/shipping/zones'))).toBe(false);
    expect(await screen.findByRole('heading', { name: 'متجرك جاهز' }, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.getByText('الشحن لم يُضبط بعد: التوصيل مجاني لكل المحافظات حتى تضبطه')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'مناطق الشحن' })).toHaveAttribute('href', `/stores/${store}/shipping`);
    expect(screen.getByRole('link', { name: 'عرض متجري' })).toHaveAttribute('href', 'http://beit-mona.iftahonline.localhost:18081');

    await user.click(screen.getByRole('button', { name: 'اذهب إلى لوحة التحكم' }));
    await waitFor(() => {
      expect(screen.getByTestId('where')).toHaveTextContent(`/stores/${store}/home`);
    });
    expect(calls.some((c) => c.method === 'POST' && c.path.endsWith('/onboarding/complete'))).toBe(true);
  });

  it('saves the quick shipping settings before the template', async () => {
    const calls = mockAPI({
      [`GET ${base}/onboarding`]: () => ({ status: 200, body: { status: 'in_progress', template: { status: 'none' } } }),
      [`GET ${base}/shipping`]: () => ({ status: 200, body: shippingCfg }),
      [`GET ${base}`]: () => ({ status: 200, body: { id: store, slug: 'beit-mona', name: 'x' } }),
      [`PUT ${base}/shipping/settings`]: () => ({ status: 200, body: {} }),
      [`POST ${base}/shipping/zones`]: () => ({ status: 201, body: {} }),
      [`PUT ${base}/onboarding/template`]: () => ({ status: 202, body: { status: 'in_progress', template: { status: 'preparing' } } }),
    });
    renderAt(`/stores/${store}/setup`);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'متابعة' }));
    await user.click(screen.getByRole('button', { name: 'متابعة' }));
    await user.click(screen.getByRole('button', { name: 'محافظات أختارها' }));
    await user.click(screen.getByRole('button', { name: 'تجهيز متجري' }));
    expect(await screen.findByText('سعر الشحن: اكتب مبلغًا صحيحًا')).toBeInTheDocument();
    await user.type(screen.getByLabelText(/^سعر الشحن/), '50');
    await user.click(screen.getByRole('button', { name: 'تجهيز متجري' }));
    expect(await screen.findByText('اختر محافظة واحدة على الأقل')).toBeInTheDocument();
    await user.click(screen.getByRole('checkbox', { name: 'الجيزة' }));
    await user.type(screen.getByLabelText(/^مبلغ ثابت لكل طلب/), '10');
    await user.click(screen.getByRole('button', { name: 'تجهيز متجري' }));
    expect(await screen.findByRole('heading', { name: 'جارٍ تجهيز متجرك…' })).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith('/shipping/settings'))?.body).toEqual({
      cod_fee: { fixed_minor: 1000, bp: 0, min_minor: 0, max_minor: 0 },
      version: 2,
    });
    expect(calls.find((c) => c.path.endsWith('/shipping/zones'))?.body).toEqual({ name: 'مناطق التوصيل', areas: ['EG-GZ'], rate_minor: 5000, cities: [] });
    const order = calls.filter((c) => c.method !== 'GET').map((c) => c.path.split('/').slice(-1)[0]);
    expect(order).toEqual(['settings', 'zones', 'template']);
  });

  it('resumes where the merchant left, and leaves a closed wizard', async () => {
    mockAPI({
      [`GET ${base}/onboarding`]: () => ({ status: 200, body: { status: 'in_progress', activity: 'food', look: 'modern', color: '#15803d', template: { status: 'failed' } } }),
      [`GET ${base}/shipping`]: () => ({ status: 200, body: { ...shippingCfg, zones: [{ id: 'z' }] } }),
      [`GET ${base}`]: () => ({ status: 200, body: { id: store, slug: 's', name: 'x' } }),
      [`PUT ${base}/onboarding/template`]: () => ({ status: 202, body: { status: 'in_progress', template: { status: 'preparing' } } }),
    });
    const first = renderAt(`/stores/${store}/setup`);
    const user = userEvent.setup();
    expect(await screen.findByRole('heading', { name: 'تعذّر تجهيز متجرك' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'حاول مرة أخرى' }));
    expect(await screen.findByRole('heading', { name: 'جارٍ تجهيز متجرك…' })).toBeInTheDocument();
    first.unmount();

    mockAPI({
      [`GET ${base}/onboarding`]: () => ({ status: 200, body: { status: 'completed', template: { status: 'ready' } } }),
      [`GET ${base}/shipping`]: () => ({ status: 200, body: shippingCfg }),
      [`GET ${base}`]: () => ({ status: 200, body: { id: store, slug: 's', name: 'x' } }),
    });
    renderAt(`/stores/${store}/setup`);
    await waitFor(() => {
      expect(screen.getByTestId('where')).toHaveTextContent(`/stores/${store}/home`);
    });
  });
});

describe('SamplesBanner', () => {
  it('counts the samples and deletes them after asking', async () => {
    const calls = mockAPI({
      [`GET ${base}/products`]: () => ({ status: 200, body: { items: [{ id: 'p1', sample: true }, { id: 'p2', sample: true }, { id: 'p3' }] } }),
      [`DELETE ${base}/products/samples`]: () => ({ status: 200, body: { deleted: 2 } }),
    });
    vi.spyOn(globalThis, 'confirm').mockReturnValue(true);
    render(<SamplesBanner storeId={store} />);
    const region = await screen.findByRole('region', { name: 'المنتجات التجريبية' });
    expect(within(region).getByText(/2 منتجات تجريبية/)).toBeInTheDocument();
    await userEvent.setup().click(within(region).getByRole('button', { name: 'احذف المنتجات التجريبية' }));
    await waitFor(() => {
      expect(screen.queryByRole('region', { name: 'المنتجات التجريبية' })).toBeNull();
    });
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
  });
});
