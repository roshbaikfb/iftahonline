import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { mockAPI, type Call } from '../test/api';
import { bannerState, liveCount, period, startOfDay, type AdBanner } from './ads';
import { Gate } from './Console';
import { AdminSessionContext, type AdminSession } from './session';

const admin = { id: '01M5ADMIN000000000000000AA', email: 'ops@iftahonline.com', name: 'سارة' };
const banner = '01M9BANNER00000000000000AA';

function renderAds() {
  const state = { kind: 'signed-in', admin } as const;
  const value: AdminSession = { state, signedIn: vi.fn(), signOut: vi.fn(() => Promise.resolve()) };
  render(
    <AdminSessionContext.Provider value={value}>
      <MemoryRouter initialEntries={['/ads']}>
        <Gate state={state} />
      </MemoryRouter>
    </AdminSessionContext.Provider>,
  );
}

function ad(over: Partial<AdBanner> = {}): AdBanner {
  return {
    id: banner,
    title: 'افتح متجرك مجانًا',
    image_url: 'https://img.test/b.webp',
    width: 640,
    height: 200,
    link_url: 'https://iftahonline.com/start',
    placement: 'home_top',
    starts_at: '2026-10-01T00:00:00Z',
    weight: 10,
    active: true,
    clicks: 1234,
    ...over,
  };
}

const settings = { adsense_enabled: false, publisher_id: '', units: {}, ads_txt: '' };

const body = (calls: Call[], method: string, path: string) => calls.find((c) => c.method === method && c.path === path)?.body;

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ads in the console', () => {
  it('saves AdSense’s settings, adds a banner through the upload, and stops one', async () => {
    vi.spyOn(window, 'prompt').mockReturnValue('انتهت الحملة');
    const saved = {
      adsense_enabled: true,
      publisher_id: 'ca-pub-1234567890123456',
      units: { grid: '1234567890' },
      ads_txt: 'google.com, pub-1234567890123456, DIRECT, f08c47fec0942fa0\n',
    };
    const calls = mockAPI({
      'GET /admin/v1/ads': () => ({ status: 200, body: { settings, banners: [ad()] } }),
      'PUT /admin/v1/ads/settings': () => ({ status: 200, body: saved }),
      'POST /admin/v1/ads/uploads': () => ({
        status: 201,
        body: {
          key: 'platform/ads/uploads/U1',
          upload: { url: 'https://s3.test/media', fields: { key: 'platform/ads/uploads/U1' }, expires_at: '', max_bytes: 2097152 },
        },
      }),
      'POST /media': () => ({ status: 204 }),
      'POST /admin/v1/ads/banners': () => ({ status: 201, body: ad({ id: 'B2', title: 'شحن أسرع', placement: 'grid', country: 'EG', clicks: 0 }) }),
      [`PUT /admin/v1/ads/banners/${banner}/active`]: () => ({ status: 204 }),
    });
    renderAds();
    const user = userEvent.setup();

    const row = (await screen.findByText('افتح متجرك مجانًا')).closest('tr') as HTMLElement;
    expect(within(row).getByText('1,234')).toBeInTheDocument();
    expect(within(row).getByText('نشط')).toBeInTheDocument();
    expect(within(row).getByText('كل الدول')).toBeInTheDocument();
    const sources = screen.getByRole('form', { name: 'مصدر الإعلان في كل مكان' });
    expect(within(within(sources).getByText('أعلى الرئيسية').closest('tr') as HTMLElement).getByText('1')).toBeInTheDocument();

    await user.type(within(sources).getByLabelText('بين المنتجات — رقم وحدة AdSense'), '1234567890');
    await user.type(within(sources).getByLabelText('رقم الناشر في AdSense'), 'ca-pub-1234567890123456');
    await user.click(within(sources).getByLabelText('تفعيل إعلانات Google'));
    await user.type(within(sources).getByLabelText('السبب (إجباري)'), 'ربط حساب AdSense');
    await user.click(within(sources).getByRole('button', { name: 'حفظ الإعدادات' }));
    expect(await screen.findByText(/google.com, pub-1234567890123456, DIRECT/)).toBeInTheDocument();
    expect(body(calls, 'PUT', '/admin/v1/ads/settings')).toEqual({
      adsense_enabled: true,
      publisher_id: 'ca-pub-1234567890123456',
      units: { home_top: '', grid: '1234567890', product_bottom: '', category_top: '' },
      reason: 'ربط حساب AdSense',
    });

    const form = screen.getByRole('form', { name: 'بانر جديد' });
    await user.type(within(form).getByLabelText('العنوان (للكونسول وقارئ الشاشة)'), 'شحن أسرع');
    await user.upload(within(form).getByLabelText('الصورة (WebP/PNG/JPEG، تُعاد ترميزها)'), new File(['png'], 'b.png', { type: 'image/png' }));
    await user.type(within(form).getByLabelText('الرابط (https)'), 'https://iftahonline.com/shipping');
    await user.selectOptions(within(form).getByLabelText('المكان'), 'grid');
    await user.selectOptions(within(form).getByLabelText('الدولة'), 'EG');
    await user.type(within(form).getByLabelText('من'), '2026-11-01');
    await user.clear(within(form).getByLabelText('الوزن (1–100)'));
    await user.type(within(form).getByLabelText('الوزن (1–100)'), '5');
    await user.type(within(form).getByLabelText('السبب'), 'حملة نوفمبر');
    // jsdom does not see user.upload's file in a required input's validity: submit directly.
    fireEvent.submit(form);
    expect(await screen.findByText('أُضيف البانر.')).toBeInTheDocument();
    expect(body(calls, 'POST', '/admin/v1/ads/uploads')).toEqual({ content_type: 'image/png' });
    expect(calls.some((c) => c.method === 'POST' && c.path === '/media')).toBe(true);
    expect(body(calls, 'POST', '/admin/v1/ads/banners')).toEqual({
      title: 'شحن أسرع',
      upload_key: 'platform/ads/uploads/U1',
      link_url: 'https://iftahonline.com/shipping',
      placement: 'grid',
      country: 'EG',
      starts_at: startOfDay('2026-11-01'),
      weight: 5,
      reason: 'حملة نوفمبر',
    });
    const added = screen.getByText('شحن أسرع', { selector: 'strong' }).closest('tr') as HTMLElement;
    expect(within(added).getByText('مصر')).toBeInTheDocument();
    expect(within(added).getByText('بين المنتجات')).toBeInTheDocument();

    await user.click(within(row).getByRole('button', { name: 'إيقاف' }));
    expect(await within(row).findByText('متوقف')).toBeInTheDocument();
    expect(body(calls, 'PUT', `/admin/v1/ads/banners/${banner}/active`)).toEqual({ active: false, reason: 'انتهت الحملة' });
  });

  it('shows a refused change, and that there are no banners yet', async () => {
    mockAPI({
      'GET /admin/v1/ads': () => ({ status: 200, body: { settings, banners: [] } }),
      'PUT /admin/v1/ads/settings': () => ({
        status: 422,
        body: {
          title: 'Validation failed',
          status: 422,
          detail: 'AdSense needs a publisher ID',
          errors: [{ field: 'publisher_id', code: 'required', message: 'AdSense needs a publisher ID' }],
        },
      }),
    });
    renderAds();
    const user = userEvent.setup();
    expect(await screen.findByText('لا توجد بانرات بعد.')).toBeInTheDocument();
    const sources = screen.getByRole('form', { name: 'مصدر الإعلان في كل مكان' });
    await user.click(within(sources).getByLabelText('تفعيل إعلانات Google'));
    await user.type(within(sources).getByLabelText('السبب (إجباري)'), 'تجربة');
    await user.click(within(sources).getByRole('button', { name: 'حفظ الإعدادات' }));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
  });

  it('tells a banner’s state, counts the live ones, and writes its period', () => {
    const now = new Date('2026-11-10T00:00:00Z');
    expect(bannerState(ad(), now)).toBe('live');
    expect(bannerState(ad({ active: false }), now)).toBe('stopped');
    expect(bannerState(ad({ ends_at: '2026-11-01T00:00:00Z' }), now)).toBe('ended');
    expect(bannerState(ad({ starts_at: '2026-12-01T00:00:00Z' }), now)).toBe('scheduled');
    expect(liveCount([ad(), ad({ placement: 'grid', active: false }), ad({ placement: 'grid' })], now)).toEqual({
      home_top: 1,
      grid: 1,
      product_bottom: 0,
      category_top: 0,
    });
    expect(period(ad({ starts_at: '2026-11-01T12:00:00Z' }))).toBe('من 1 نوفمبر');
    expect(period(ad({ starts_at: '2026-11-01T12:00:00Z', ends_at: '2026-11-30T12:00:00Z' }))).toBe('1 نوفمبر – 30 نوفمبر');
  });
});
