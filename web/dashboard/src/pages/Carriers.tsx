import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { carrierLabel } from '../lib/shipments';
import { field } from '../session';

type Connections = components['schemas']['CarrierConnections'];
type Connected = components['schemas']['CarrierConnected'];

// Carriers connects the store's accounts at shipping companies (ADR-0026).
// The key is checked with the carrier, then kept encrypted; the webhook
// URL and secret are shown once, to paste into the carrier's dashboard.
export function Carriers() {
  const { storeId = '' } = useParams();
  const [list, setList] = useState<Connections | null>(null);
  const [error, setError] = useState('');
  const [connected, setConnected] = useState<Connected | null>(null);
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void api
      .GET('/v1/stores/{store_id}/shipping/carriers', {
        params: { path: { store_id: storeId } },
      })
      .then(({ data, error: err }) => {
        if (!live) return;
        if (data) setList(data);
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
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/shipping/carriers', {
      params: { path: { store_id: storeId } },
      body: {
        carrier: field(form, 'carrier'),
        api_key: field(form, 'api_key').trim(),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setConnected(data);
    setReloads((n) => n + 1);
  }

  async function disconnect(id: string) {
    if (!globalThis.confirm('فصل هذا الحساب؟ لن تُنشأ شحنات جديدة به، ولن تُقبل إشعاراته.')) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/shipping/carriers/{connection_id}', {
      params: { path: { store_id: storeId, connection_id: id } },
    });
    if (err) setError(problemMessage(err));
    else setReloads((n) => n + 1);
  }

  const active = list?.connections.filter((c) => c.status === 'active') ?? [];
  const free = list?.carriers.filter((c) => !active.some((a) => a.carrier === c)) ?? [];
  return (
    <Layout storeId={storeId}>
      <h1>شركات الشحن</h1>
      {error && <p role="alert">{error}</p>}
      {connected && (
        <div className="panel secret" role="status">
          <p>
            تم ربط {carrierLabel(connected.connection.carrier)}. انسخ الآتي إلى إعدادات الإشعارات (Webhook) في لوحة شركة الشحن —{' '}
            <strong>السر يظهر هذه المرة فقط</strong>:
          </p>
          <dl>
            <dt>الرابط</dt>
            <dd dir="ltr">
              <code>{connected.webhook_url}</code>
            </dd>
            <dt>السر (ترويسة X-Webhook-Secret)</dt>
            <dd dir="ltr">
              <code>{connected.webhook_secret}</code>
            </dd>
          </dl>
          <button
            type="button"
            className="link"
            onClick={() => {
              setConnected(null);
            }}
          >
            نسخته، إخفاء
          </button>
        </div>
      )}
      {list && (
        <>
          {list.connections.length === 0 && <p className="empty">لم تربط أي شركة شحن بعد.</p>}
          <table>
            <tbody>
              {list.connections.map((c) => (
                <tr key={c.id}>
                  <td>{carrierLabel(c.carrier)}</td>
                  <td>{c.status === 'active' ? 'مربوط' : 'مفصول'}</td>
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
          {free.length > 0 && (
            <form className="panel" onSubmit={(e) => void connect(e)}>
              <h2>ربط حساب</h2>
              <label>
                الشركة
                <select name="carrier">
                  {free.map((c) => (
                    <option key={c} value={c}>
                      {carrierLabel(c)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                مفتاح API من لوحة الشركة
                <input name="api_key" type="password" required maxLength={512} autoComplete="off" />
              </label>
              <button type="submit" disabled={busy}>
                {busy ? 'جارٍ التحقق…' : 'ربط'}
              </button>
            </form>
          )}
        </>
      )}
    </Layout>
  );
}
