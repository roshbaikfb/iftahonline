import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime, dayStart, money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { carrierLabel } from '../lib/shipments';
import { field } from '../session';

type Report = components['schemas']['CODReport'];
type Held = components['schemas']['HeldCOD'];
type Imported = components['schemas']['CODStatementImport'];
type Connection = components['schemas']['CarrierConnection'];

// readText reads a chosen file as UTF-8 text.
function readText(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => {
      resolve(typeof r.result === 'string' ? r.result : '');
    };
    r.onerror = () => {
      reject(r.error ?? new Error('تعذّرت قراءة الملف'));
    };
    r.readAsText(file, 'utf-8');
  });
}

// COD is cash-on-delivery reconciliation (ADR-0028): per carrier
// account, what delivered parcels should bring in, what the carrier paid
// out, and what it still holds, aged; the parcels still held; and the
// import of a carrier's settlement statement (CSV).
export function COD() {
  const { storeId = '' } = useParams();
  const [params, setParams] = useSearchParams();
  const from = params.get('from') ?? '';
  const to = params.get('to') ?? '';
  const [report, setReport] = useState<Report | null>(null);
  const [held, setHeld] = useState<Held[]>([]);
  const [next, setNext] = useState<string | undefined>();
  const [connections, setConnections] = useState<Connection[]>([]);
  const [imported, setImported] = useState<Imported | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    const query = { ...(from ? { from: dayStart(from) } : {}), ...(to ? { to: dayStart(to, 1) } : {}) };
    void Promise.all([
      api.GET('/v1/stores/{store_id}/shipping/cod', { params: { path: { store_id: storeId }, query } }),
      api.GET('/v1/stores/{store_id}/shipping/cod/held', { params: { path: { store_id: storeId }, query: { limit: 50 } } }),
      api.GET('/v1/stores/{store_id}/shipping/carriers', { params: { path: { store_id: storeId } } }),
    ])
      .then(([r, h, c]) => {
        if (!live) return;
        setReport(r.data ?? null);
        setHeld(h.data?.items ?? []);
        setNext(h.data?.next_cursor);
        setConnections(c.data?.connections ?? []);
        setError(r.error ? problemMessage(r.error) : '');
      })
      .catch((err: unknown) => {
        if (live) setError(problemMessage(err));
      });
    return () => {
      live = false;
    };
  }, [storeId, from, to, reloads]);

  async function moreHeld() {
    if (!next) return;
    const { data } = await api.GET('/v1/stores/{store_id}/shipping/cod/held', {
      params: { path: { store_id: storeId }, query: { limit: 50, cursor: next } },
    });
    setHeld((prev) => [...prev, ...(data?.items ?? [])]);
    setNext(data?.next_cursor);
  }

  function applyRange(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const p = new URLSearchParams();
    for (const k of ['from', 'to']) {
      const v = field(form, k);
      if (v) p.set(k, v);
    }
    setParams(p);
  }

  async function importStatement(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const input = e.currentTarget.elements.namedItem('statement');
    const file = input instanceof HTMLInputElement ? input.files?.[0] : undefined;
    if (!file || file.size === 0) {
      setError('اختر ملف CSV');
      return;
    }
    setBusy(true);
    setError('');
    setImported(null);
    try {
      const { data, error: err } = await api.POST('/v1/stores/{store_id}/shipping/cod/statements', {
        params: { path: { store_id: storeId }, query: { connection_id: field(form, 'connection_id') } },
        body: await readText(file),
        bodySerializer: (b) => b,
        headers: { 'Content-Type': 'text/csv' },
      });
      if (err) setError(problemMessage(err));
      else {
        setImported(data);
        setReloads((n) => n + 1);
      }
    } catch (caught) {
      setError(problemMessage(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <h1>تحصيل الدفع عند الاستلام</h1>
      </div>
      <form className="filters" onSubmit={applyRange} aria-label="الفترة">
        <label>
          من
          <input type="date" name="from" defaultValue={from} />
        </label>
        <label>
          إلى
          <input type="date" name="to" defaultValue={to} />
        </label>
        <button type="submit" className="secondary">
          عرض
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
      {report && (
        <>
          <p className="hint">
            الطرود المسلّمة من {dateTime(report.from)} إلى {dateTime(report.to)}
          </p>
          {report.carriers.length === 0 && <p className="empty">لا توجد طرود مسلّمة بالدفع عند الاستلام في هذه الفترة.</p>}
          {report.carriers.map((c) => (
            <section className="card" key={c.connection_id}>
              <h2>{carrierLabel(c.carrier)}</h2>
              <div className="stats">
                <div>
                  <small>طرود مسلّمة</small>
                  <strong>{c.delivered}</strong>
                </div>
                <div>
                  <small>المفترض تحصيله</small>
                  <strong>{money(c.expected_minor, c.currency)}</strong>
                </div>
                <div>
                  <small>ما حوّلته الشركة</small>
                  <strong>{money(c.settled_minor, c.currency)}</strong>
                </div>
                <div className="key">
                  <small>ما زال لدى الشركة</small>
                  <strong>{money(c.held_minor, c.currency)}</strong>
                </div>
              </div>
              <div className="stats aging" role="group" aria-label="عمر المبالغ المتبقية">
                <div>
                  <small>0–7 أيام</small>
                  <strong>{money(c.held_0_7_minor, c.currency)}</strong>
                </div>
                <div>
                  <small>8–14 يومًا</small>
                  <strong>{money(c.held_8_14_minor, c.currency)}</strong>
                </div>
                <div className={c.held_15_plus_minor > 0 ? 'late' : undefined}>
                  <small>أكثر من 15 يومًا</small>
                  <strong>{money(c.held_15_plus_minor, c.currency)}</strong>
                </div>
              </div>
              {c.over_settled > 0 && (
                <p className="notice" role="note">
                  {c.over_settled} طرد حُوِّل عنه أكثر من قيمته — راجع الكشف.
                </p>
              )}
            </section>
          ))}
          {report.unmatched_lines > 0 && (
            <p className="notice" role="note">
              {report.unmatched_lines} سطر في الكشوف لا يطابق أي طرد ({money(report.unmatched_minor, report.carriers[0]?.currency ?? 'EGP')}
              ).
            </p>
          )}
        </>
      )}
      <div className={connections.length > 0 ? 'split' : undefined}>
        <section className="table-card">
          <h2 className="card-title">طرود لم تُحوَّل قيمتها بعد</h2>
          {held.length === 0 ? (
            <p className="empty flat">لا شيء: كل الطرود المسلّمة حُوِّلت قيمتها.</p>
          ) : (
            <div className="scroll">
              <table className="orders narrow">
                <thead>
                  <tr>
                    <th>رقم التتبع</th>
                    <th>الشركة</th>
                    <th>سُلّم</th>
                    <th>المتبقي</th>
                  </tr>
                </thead>
                <tbody>
                  {held.map((h) => (
                    <tr key={h.shipment_id}>
                      <td>
                        <Link to={`/stores/${storeId}/shipments/${h.shipment_id}`} dir="ltr" className="code">
                          {h.tracking_number}
                        </Link>
                      </td>
                      <td>{carrierLabel(h.carrier)}</td>
                      <td className="muted">{dateTime(h.delivered_at)}</td>
                      <td className="num">{money(h.held_minor, h.currency)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {next && (
            <div className="foot">
              <span />
              <button type="button" className="secondary" onClick={() => void moreHeld()}>
                تحميل المزيد
              </button>
            </div>
          )}
        </section>
        {connections.length > 0 && (
          <form className="panel" onSubmit={(e) => void importStatement(e)}>
            <h2>استيراد كشف تحويلات (CSV)</h2>
            <p className="hint">صدّر الكشف من لوحة شركة الشحن. يجب أن يحتوي على عمودي رقم التتبع والمبلغ. استيراد الكشف نفسه مرة أخرى لا يضيف شيئًا.</p>
            <label>
              الحساب
              <select name="connection_id">
                {connections.map((c) => (
                  <option key={c.id} value={c.id}>
                    {carrierLabel(c.carrier)}
                    {c.status === 'active' ? '' : ' (مفصول)'}
                  </option>
                ))}
              </select>
            </label>
            <label>
              الملف
              <input type="file" name="statement" accept=".csv,text/csv" />
            </label>
            <button type="submit" disabled={busy}>
              {busy ? 'جارٍ الاستيراد…' : 'استيراد'}
            </button>
            {imported && (
              <div role="status">
                <p>
                  قُرئ {imported.lines} سطر: أُضيف {imported.imported} (طابق {imported.matched} طردًا)، و{imported.duplicates} مستورد من قبل.
                </p>
                {imported.errors.length > 0 && (
                  <ul>
                    {imported.errors.map((er) => (
                      <li key={er.line}>
                        سطر {er.line}: {er.message}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
          </form>
        )}
      </div>
    </Layout>
  );
}
