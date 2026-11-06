import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { PlanLock } from '../components/PlanLock';
import { dateTime } from '../lib/orders';
import { gatewayLabel } from '../lib/payments';
import { lockedFor, usePlanFeatures } from '../lib/plan';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

type Connections = components['schemas']['PaymentConnections'];
type ConnectInput = components['schemas']['ConnectPaymentGatewayInput'];

// Stripe's restricted key needs these permissions (ADR-0094): the
// platform creates the webhook endpoint itself.
const stripePermissions = ['Checkout Sessions: Write', 'PaymentIntents: Read', 'Refunds: Write', 'Webhook Endpoints: Write'];

// connectMessages say a refused connection in Arabic, by field and code
// (the server's are English).
const connectMessages: Record<string, string> = {
  'secret_key:refused': 'رفضت البوابة هذه المفاتيح. تأكد أنك نسختها كاملة من حساب الوضع الحقيقي (live).',
  'secret_key:invalid': 'الصق المفتاح السري كما هو من حسابك في البوابة.',
  'secret_key:webhook_refused': 'قَبِل Stripe المفتاح، لكنه رفض إنشاء رابط الإشعارات. أضف للمفتاح صلاحية Webhook Endpoints: Write ثم أعد المحاولة.',
  'api_key:invalid': 'الصق الـ API key كما هو من حسابك في البوابة.',
  'webhook_secret:invalid': 'الصق سر الإشعارات كما هو من حسابك في البوابة.',
  'integration_ids:required': 'اكتب رقم تكامل البطاقات (Integration ID) من حسابك في Paymob.',
  'gateway:not_offered': 'هذه البوابة غير متاحة في بلد متجرك.',
  'gateway:unknown': 'هذه البوابة غير متاحة.',
};

// connectError is a refused connection's message for the merchant.
function connectError(err: unknown): string {
  const fields = (err as { errors?: { field: string; code: string }[] } | undefined)?.errors ?? [];
  for (const f of fields) {
    const m = connectMessages[`${f.field}:${f.code}`];
    if (m) return m;
  }
  return problemMessage(err);
}

// Gateways connects the store's own account at a card gateway (ADR-0029;
// the owner only), among those of the store's country (ADR-0093). The
// keys are checked with the gateway, then kept encrypted and never shown
// again. Stripe takes one restricted key: the platform creates its
// webhook endpoint (ADR-0094, the approved mockup). Once connected,
// checkout offers card payment beside cash on delivery.
export function Gateways() {
  const { storeId = '' } = useParams();
  const locked = lockedFor(usePlanFeatures(storeId), 'card_payments');
  const [list, setList] = useState<Connections | null>(null);
  const [gateway, setGateway] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void api
      .GET('/v1/stores/{store_id}/payments/gateways', { params: { path: { store_id: storeId } } })
      .then(({ data, error: err }) => {
        if (!live) return;
        if (data) {
          setList(data);
          setGateway((g) => g || (data.gateways[0] ?? ''));
        }
        setError(err ? problemMessage(err) : '');
      })
      .catch((err: unknown) => {
        if (live) setError(problemMessage(err));
      });
    return () => {
      live = false;
    };
  }, [storeId, reloads]);

  async function connect(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const body: ConnectInput =
      gateway === 'stripe'
        ? { gateway, secret_key: field(form, 'secret_key').trim() }
        : { gateway, api_key: field(form, 'api_key').trim(), webhook_secret: field(form, 'webhook_secret').trim() };
    if (gateway === 'paymob') {
      const ids = field(form, 'integration_ids')
        .split(/[\s,،]+/)
        .filter(Boolean)
        .map(Number);
      if (ids.length === 0 || ids.some((n) => !Number.isInteger(n) || n < 1)) {
        setError('أرقام التكامل: اكتب رقمًا أو أكثر مفصولة بفاصلة');
        return;
      }
      body.secret_key = field(form, 'secret_key').trim();
      body.public_key = field(form, 'public_key').trim();
      body.integration_ids = ids;
    }
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/payments/gateways', { params: { path: { store_id: storeId } }, body });
    setBusy(false);
    if (err) {
      setError(connectError(err));
      return;
    }
    setNotice('تم الربط: صفحة الدفع في متجرك تعرض الآن الدفع بالبطاقة.');
    setReloads((n) => n + 1);
  }

  async function disconnect(id: string) {
    if (!globalThis.confirm('فصل البوابة؟ يتوقف الدفع بالبطاقة في متجرك، وتبقى المدفوعات السابقة قابلة للاسترداد.')) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/payments/gateways/{connection_id}', {
      params: { path: { store_id: storeId, connection_id: id } },
    });
    if (err) setError(problemMessage(err));
    else setReloads((n) => n + 1);
  }

  const active = list?.connections.some((c) => c.status === 'active') ?? false;
  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <h1>طرق الدفع</h1>
      </div>
      {error && <p role="alert">{error}</p>}
      {locked && <PlanLock storeId={storeId} feature="card_payments" />}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      <section className="card cod-card" aria-labelledby="h-cod">
        <div>
          <h2 id="h-cod">الدفع عند الاستلام</h2>
          <p className="hint">متاح دائمًا لكل الطلبات.</p>
        </div>
        <span className="status st-on">مفعّل</span>
      </section>
      {list && (
        <div className={!active && list.gateways.length > 0 ? 'split gateways-split' : undefined}>
          <section className="card">
            <h2>بوابة الدفع</h2>
            {list.connections.length === 0 ? (
              <p className="hint">لم تربط بوابة دفع بعد: متجرك يقبل الدفع عند الاستلام فقط.</p>
            ) : (
              <ul className="rows">
                {list.connections.map((c) => (
                  <li key={c.id}>
                    <div className="what">
                      <strong>{gatewayLabel(c.gateway)}</strong>
                      <small>رُبطت في {dateTime(c.created_at)}</small>
                    </div>
                    <span className={c.status === 'active' ? 'status st-on' : 'status'}>{c.status === 'active' ? 'مربوطة' : 'مفصولة'}</span>
                    {c.status === 'active' && (
                      <button type="button" className="danger" onClick={() => void disconnect(c.id)}>
                        فصل
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
          {!active && list.gateways.length > 0 && (
            <form className="panel" onSubmit={(e) => void connect(e)}>
              <h2>ربط حسابك في بوابة الدفع</h2>
              <p className="hint">انسخ المفاتيح من لوحة البوابة. أموال المبيعات تذهب إلى حسابك أنت مباشرة؛ لا نرى بيانات البطاقات أبدًا.</p>
              <label>
                البوابة
                <select
                  value={gateway}
                  onChange={(e) => {
                    setGateway(e.target.value);
                  }}
                >
                  {list.gateways.map((g) => (
                    <option key={g} value={g}>
                      {gatewayLabel(g)}
                    </option>
                  ))}
                </select>
              </label>
              {gateway === 'stripe' && (
                <>
                  <ol className="steps-list">
                    <li>
                      في Stripe افتح <bdi className="ltr">Developers › API keys › Create restricted key</bdi>، وأعطه هذه الصلاحيات:
                      <ul className="perm-list">
                        {stripePermissions.map((p) => (
                          <li key={p}>
                            <bdi className="ltr">{p}</bdi>
                          </li>
                        ))}
                      </ul>
                    </li>
                    <li>الصق المفتاح هنا. سننشئ رابط الإشعارات في حسابك تلقائيًا.</li>
                  </ol>
                  <label>
                    المفتاح المحدود (Restricted key)
                    <input
                      name="secret_key"
                      type="password"
                      required
                      maxLength={512}
                      autoComplete="off"
                      dir="ltr"
                      pattern="(rk|sk)_.+"
                      placeholder="rk_live_…"
                    />
                    <small className="hint">
                      يبدأ بـ <bdi className="ltr">rk_</bdi>. المفتاح السري <bdi className="ltr">sk_</bdi> يصلح أيضًا، لكن المحدود أأمن.
                    </small>
                  </label>
                </>
              )}
              {gateway === 'paymob' && (
                <>
                  <label>
                    Secret key
                    <input name="secret_key" type="password" required maxLength={512} autoComplete="off" dir="ltr" />
                  </label>
                  <label>
                    Public key
                    <input name="public_key" required maxLength={512} autoComplete="off" dir="ltr" />
                  </label>
                </>
              )}
              {gateway !== 'stripe' && (
                <>
                  <label>
                    API key
                    <input name="api_key" type="password" required maxLength={1024} autoComplete="off" dir="ltr" />
                  </label>
                  <label>
                    {gateway === 'paymob' ? 'HMAC secret' : 'سر الإشعارات'}
                    <input name="webhook_secret" type="password" required maxLength={512} autoComplete="off" dir="ltr" />
                  </label>
                </>
              )}
              {gateway === 'paymob' && (
                <label>
                  أرقام تكامل البطاقات (Integration IDs)
                  <input name="integration_ids" required inputMode="numeric" dir="ltr" placeholder="4567890" />
                </label>
              )}
              <button type="submit" disabled={busy || locked}>
                {busy ? 'جارٍ التحقق من المفاتيح…' : gateway === 'stripe' ? 'اربط Stripe' : 'ربط'}
              </button>
            </form>
          )}
        </div>
      )}
    </Layout>
  );
}
