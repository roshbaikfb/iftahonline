import type { components } from '../api/client';

export type Pixel = components['schemas']['Pixel'];
export type PixelInput = components['schemas']['PixelInput'];
export type PixelProvider = components['schemas']['PixelProvider'];
export type ProviderName = components['schemas']['PixelProviderName'];
export type MarketingSettings = components['schemas']['MarketingSettings'];
export type ProductFeed = components['schemas']['ProductFeed'];

// How the dashboard shows each provider: its name, mark and colour, what
// its ID is called and looks like (the server's rules, ADR-0043). The API
// may list a provider this build does not know: it is skipped.
export interface ProviderLook {
  name: string;
  mark: string;
  color: string;
  idLabel: string;
  placeholder: string;
  idPattern: RegExp;
  tokenLabel: string;
}

export const providerLook: Record<ProviderName, ProviderLook> = {
  meta: {
    name: 'Meta (فيسبوك وإنستجرام)',
    mark: 'M',
    color: '#1d4ed8',
    idLabel: 'معرّف البكسل (Pixel ID)',
    placeholder: '123456789012345',
    idPattern: /^[0-9]{10,20}$/,
    tokenLabel: 'رمز Conversions API',
  },
  tiktok: {
    name: 'TikTok',
    mark: 'T',
    color: '#0f172a',
    idLabel: 'رمز البكسل (Pixel code)',
    placeholder: 'C4ABCDEF1234567890AB',
    idPattern: /^[A-Z0-9]{15,25}$/,
    tokenLabel: 'رمز Events API',
  },
  snap: {
    name: 'Snapchat',
    mark: 'S',
    color: '#a16207',
    idLabel: 'معرّف البكسل (Pixel ID)',
    placeholder: '0f8fad5b-d9cb-469f-a165-70867728950e',
    idPattern: /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    tokenLabel: 'رمز Conversions API',
  },
  google_ads: {
    name: 'Google Ads',
    mark: 'G',
    color: '#15803d',
    idLabel: 'معرّف التحويل (Conversion ID)',
    placeholder: 'AW-123456789',
    idPattern: /^AW-[0-9]{6,15}$/,
    tokenLabel: '',
  },
  ga4: {
    name: 'Google Analytics 4',
    mark: 'GA',
    color: '#b45309',
    idLabel: 'معرّف القياس (Measurement ID)',
    placeholder: 'G-ABC123XYZ',
    idPattern: /^G-[A-Z0-9]{4,15}$/,
    tokenLabel: 'API secret',
  },
};

export const isKnown = (p: string): p is ProviderName => p in providerLook;

// pixelState is the list's pill and line: not connected, paused, or
// connected from the browser alone or from the server too.
export function pixelState(spec: PixelProvider, pixel: Pixel | undefined): { label: string; cls: string; hint: string } {
  if (!pixel) return { label: 'غير مربوط', cls: 'st-none', hint: 'اربط لتقيس إعلاناتك' };
  if (!pixel.enabled) return { label: 'متوقف', cls: 'st-paused', hint: 'لا يُرسل شيئًا الآن' };
  if (spec.server && pixel.has_server_token) return { label: 'مربوط', cls: 'st-on', hint: 'من المتصفح ومن الخادم' };
  return { label: 'مربوط', cls: 'st-half', hint: 'من المتصفح فقط' };
}

// Draft is the panel's form.
export interface Draft {
  pixelId: string;
  label: string;
  testCode: string;
  enabled: boolean;
  token: string; // a new token; empty keeps the saved one
  replacing: boolean; // the merchant asked to paste a new token
}

export const draftOf = (p: Pixel | undefined): Draft => ({
  pixelId: p?.pixel_id ?? '',
  label: p?.label ?? '',
  testCode: p?.test_code ?? '',
  enabled: p?.enabled ?? true,
  token: '',
  replacing: false,
});

// toPixelInput checks the draft as the server would and builds the
// request; errors are by field, in the merchant's words.
export function toPixelInput(spec: PixelProvider, d: Draft, version: number): { input?: PixelInput; errors: Record<string, string> } {
  const look = isKnown(spec.provider) ? providerLook[spec.provider] : undefined;
  const errors: Record<string, string> = {};
  const id = d.pixelId.trim();
  if (look && !look.idPattern.test(id)) errors['pixel_id'] = `انسخه كما يظهر في حسابك، مثل ${look.placeholder}.`;
  const input: PixelInput = { pixel_id: id, enabled: d.enabled, version };
  if (spec.label) {
    const label = d.label.trim();
    if (!/^[A-Za-z0-9_-]{6,40}$/.test(label)) errors['label'] = 'تجده في إعداد تحويل الشراء في Google Ads.';
    input.label = label;
  }
  if (spec.test_code && d.testCode.trim()) {
    if (!/^[A-Za-z0-9_-]{1,40}$/.test(d.testCode.trim())) errors['test_code'] = 'حروف وأرقام و - و _ فقط.';
    input.test_code = d.testCode.trim();
  }
  if (spec.server && d.token.trim()) {
    if (d.token.trim().length < 8) errors['server_token'] = 'الصقه كاملًا كما يظهر عند المنصة.';
    input.server_token = d.token.trim();
  }
  return Object.keys(errors).length > 0 ? { errors } : { input, errors };
}

// serverErrors puts the API's refusals on the fields they concern.
export function serverErrors(problem: { code?: string; errors?: { field: string }[] }): Record<string, string> {
  const words: Record<string, string> = {
    pixel_id: 'انسخه كما يظهر في حسابك.',
    label: 'تجده في إعداد تحويل الشراء في Google Ads.',
    test_code: 'حروف وأرقام و - و _ فقط.',
    server_token: 'الصقه كاملًا كما يظهر عند المنصة.',
  };
  return Object.fromEntries((problem.errors ?? []).map((e) => [e.field, words[e.field] ?? 'قيمة غير صالحة.']));
}

export const consentChoices: { id: MarketingSettings['consent']; title: string; text: string }[] = [
  { id: 'auto', title: 'تلقائي (موصى به)', text: 'نسأل العميل حيث يلزم القانون: متاجر السعودية. غيرها تعمل البكسلات مباشرة.' },
  { id: 'required', title: 'اسأل دائمًا', text: 'تظهر رسالة الموافقة لكل عميل.' },
  { id: 'off', title: 'لا تسأل', text: 'تعمل البكسلات دون رسالة. أنت مسؤول عن موافقة عملائك.' },
];
