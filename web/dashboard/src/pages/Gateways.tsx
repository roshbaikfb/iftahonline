import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime } from '../lib/orders';
import { gatewayLabel } from '../lib/payments';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

type Connections = components['schemas']['PaymentConnections'];
type ConnectInput = components['schemas']['ConnectPaymentGatewayInput'];

// Gateways connects the store's own account at a card gateway (ADR-0029;
// the owner only). The keys are checked with the gateway, then kept
// encrypted and never shown again. Once connected, checkout offers card
// payment.
export function Gateways() {
  const { storeId = '' } = useParams();
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
    const body: ConnectInput = { gateway, api_key: field(form, 'api_key').trim(), webhook_secret: field(form, 'webhook_secret').trim() };
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
      setError(problemMessage(err));
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
      <h1>الدفع بالبطاقة</h1>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {list && (
        <>
          {list.connections.length === 0 && <p className="empty">لم تربط بوابة دفع بعد: متجرك يقبل الدفع عند الاستلام فقط.</p>}
          <table>
            <tbody>
              {list.connections.map((c) => (
                <tr key={c.id}>
                  <td>{gatewayLabel(c.gateway)}</td>
                  <td>{c.status === 'active' ? 'مربوطة' : 'مفصولة'}</td>
                  <td>{dateTime(c.created_at)}</td>
                  <td>
                    {c.status === 'active' && (
                      <button type="button" className="danger" onClick={() => void disconnect(c.id)}>
                        فصل
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!active && list.gateways.length > 0 && (
            <form className="panel" onSubmit={(e) => void connect(e)}>
              <h2>ربط حسابك في بوابة الدفع</h2>
              <p>انسخ المفاتيح من لوحة البوابة. أموال المبيعات تذهب إلى حسابك أنت مباشرة؛ لا نرى بيانات البطاقات أبدًا.</p>
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
              <label>
                API key
                <input name="api_key" type="password" required maxLength={1024} autoComplete="off" dir="ltr" />
              </label>
              <label>
                {gateway === 'paymob' ? 'HMAC secret' : 'سر الإشعارات'}
                <input name="webhook_secret" type="password" required maxLength={512} autoComplete="off" dir="ltr" />
              </label>
              {gateway === 'paymob' && (
                <label>
                  أرقام تكامل البطاقات (Integration IDs)
                  <input name="integration_ids" required inputMode="numeric" dir="ltr" placeholder="4567890" />
                </label>
              )}
              <button type="submit" disabled={busy}>
                {busy ? 'جارٍ التحقق من المفاتيح…' : 'ربط'}
              </button>
            </form>
          )}
        </>
      )}
    </Layout>
  );
}
