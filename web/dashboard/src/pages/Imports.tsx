import { useEffect, useState, type ChangeEvent, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { dateTime } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { postFile } from '../lib/upload';
import { field } from '../session';

type Import = components['schemas']['Import'];

const statusLabel: Record<Import['status'], string> = {
  pending: 'بانتظار الملف',
  queued: 'في الانتظار',
  running: 'جارٍ الاستيراد',
  succeeded: 'اكتمل',
  failed: 'فشل',
};

const statusClass: Record<Import['status'], string> = {
  pending: 's-pending',
  queued: 's-confirmed',
  running: 's-confirmed',
  succeeded: 's-delivered',
  failed: 's-cancelled',
};

// Why an import could not read its source (ADR-0014), for the merchant.
const reasons: Record<string, string> = {
  bad_header: 'أعمدة الملف غير معروفة. ابدأ من الملف النموذجي.',
  csv_malformed: 'الملف ليس CSV صحيحًا.',
  not_utf8: 'احفظ الملف بصيغة «CSV UTF-8».',
  upload_missing: 'لم يكتمل رفع الملف. ارفعه من جديد.',
  woo_auth: 'مفاتيح WooCommerce غير صحيحة أو بلا صلاحية قراءة.',
  woo_not_found: 'لم نجد متجر WooCommerce على هذا الرابط.',
  woo_blocked_address: 'هذا الرابط غير مسموح.',
};

const live = (i: Import) => i.status === 'queued' || i.status === 'running';

// Imports brings products in from a CSV file or a WooCommerce shop (U5,
// in the design system): the merchant starts one, watches its progress,
// and reads which rows were skipped. Products are matched by their link
// (handle): existing ones are updated, others created.
export function Imports() {
  const { storeId = '' } = useParams();
  const [items, setItems] = useState<Import[]>([]);
  const [loading, setLoading] = useState(true);
  const [kind, setKind] = useState<'csv' | 'woocommerce'>('csv');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [open, setOpen] = useState('');
  const [reloads, setReloads] = useState(0);
  const running = items.some(live);

  useEffect(() => {
    let alive = true;
    void api.GET('/v1/stores/{store_id}/imports', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
      if (!alive) return;
      setLoading(false);
      if (data) setItems(data.items);
      else setError(problemMessage(err));
    });
    return () => {
      alive = false;
    };
  }, [storeId, reloads]);

  // While an import runs, look again every two seconds.
  useEffect(() => {
    if (!running) return;
    const t = setTimeout(() => {
      setReloads((n) => n + 1);
    }, 2000);
    return () => {
      clearTimeout(t);
    };
  }, [running, items]);

  async function start(id: string) {
    const { error: err } = await api.POST('/v1/stores/{store_id}/imports/{import_id}/start', { params: { path: { store_id: storeId, import_id: id } } });
    if (err) throw new Error(problemMessage(err));
  }

  async function fromFile(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setError('');
    if (!/\.csv$/i.test(file.name)) {
      setError('الملف يكون CSV. من Excel: «حفظ باسم» ثم «CSV UTF-8».');
      return;
    }
    setBusy(true);
    try {
      const { data, error: err } = await api.POST('/v1/stores/{store_id}/imports', { params: { path: { store_id: storeId } }, body: { kind: 'csv' } });
      if (err) throw new Error(problemMessage(err));
      if (!data.upload) throw new Error('تعذّر تجهيز الرفع');
      await postFile(data.upload, file, 'تعذّر رفع الملف، جرّب ملفًا أصغر.');
      await start(data.import.id);
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(false);
      setReloads((n) => n + 1);
    }
  }

  async function fromWoo(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setError('');
    setBusy(true);
    try {
      const { data, error: err } = await api.POST('/v1/stores/{store_id}/imports', {
        params: { path: { store_id: storeId } },
        body: {
          kind: 'woocommerce',
          woocommerce: { url: field(form, 'url').trim(), consumer_key: field(form, 'key').trim(), consumer_secret: field(form, 'secret').trim() },
        },
      });
      if (err) throw new Error(problemMessage(err));
      await start(data.import.id);
      e.currentTarget.reset();
    } catch (err) {
      setError(problemMessage(err));
    } finally {
      setBusy(false);
      setReloads((n) => n + 1);
    }
  }

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <Link to={`/stores/${storeId}/products`} className="back">
            → المنتجات
          </Link>
          <h1>الاستيراد</h1>
          <p className="muted">أضف منتجاتك دفعة واحدة. المنتج الموجود بنفس الرابط يُحدَّث، والجديد يُضاف.</p>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <section className="card editor-card" aria-labelledby="new-import">
        <h2 id="new-import">استيراد جديد</h2>
        <div className="tabs" role="tablist" aria-label="المصدر">
          <button
            type="button"
            role="tab"
            aria-selected={kind === 'csv'}
            onClick={() => {
              setKind('csv');
            }}
          >
            ملف CSV
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={kind === 'woocommerce'}
            onClick={() => {
              setKind('woocommerce');
            }}
          >
            متجر WooCommerce
          </button>
        </div>
        {kind === 'csv' ? (
          <div className="import-csv">
            <ol className="steps-list">
              <li>
                نزّل{' '}
                <a href="/products-template.csv" download="منتجات.csv">
                  الملف النموذجي
                </a>{' '}
                وافتحه في Excel أو Google Sheets.
              </li>
              <li>اكتب منتجاتك: كل صف نوع، والصفوف المتتالية بنفس «handle» أنواع لمنتج واحد. الأسعار بالجنيه (أو عملة متجرك).</li>
              <li>احفظه بصيغة «CSV UTF-8» ثم ارفعه هنا.</li>
            </ol>
            <label className={`button primary${busy ? ' disabled' : ''}`}>
              {busy ? 'جارٍ الرفع…' : 'ارفع ملف CSV'}
              <input type="file" accept=".csv,text/csv" className="visually-hidden" disabled={busy} onChange={(e) => void fromFile(e)} />
            </label>
          </div>
        ) : (
          <form className="import-woo" onSubmit={(e) => void fromWoo(e)}>
            <p className="muted">من لوحة WooCommerce: الإعدادات ← متقدم ← REST API ← أضف مفتاحًا بصلاحية «قراءة» فقط.</p>
            <label className="field-label">
              رابط المتجر
              <input name="url" type="url" required dir="ltr" placeholder="https://myshop.com" />
            </label>
            <label className="field-label">
              Consumer key
              <input name="key" required dir="ltr" autoComplete="off" placeholder="ck_…" />
            </label>
            <label className="field-label">
              Consumer secret
              <input name="secret" type="password" required dir="ltr" autoComplete="off" placeholder="cs_…" />
            </label>
            <p className="muted small">نحفظ المفاتيح مشفّرة ولا نعرضها مرة أخرى.</p>
            <div>
              <button type="submit" className="primary" disabled={busy}>
                {busy ? 'جارٍ البدء…' : 'ابدأ الاستيراد'}
              </button>
            </div>
          </form>
        )}
      </section>

      <section className="table-card" aria-labelledby="imports-title">
        <div className="card-head">
          <h2 id="imports-title">عمليات الاستيراد</h2>
        </div>
        {loading && <p role="status" className="foot">جارٍ التحميل…</p>}
        {!loading && items.length === 0 && <p className="empty flat">لم تستورد شيئًا بعد.</p>}
        <ul className="imports">
          {items.map((i) => {
            const pct = i.total > 0 ? Math.round((i.processed / i.total) * 100) : 0;
            const shown = open === i.id;
            return (
              <li key={i.id}>
                <div className="import-row">
                  <div>
                    <strong>{i.kind === 'csv' ? 'ملف CSV' : `WooCommerce · ${i.woocommerce_url ?? ''}`}</strong>
                    <small className="sub">{dateTime(i.created_at)}</small>
                  </div>
                  <span className={`status ${statusClass[i.status]}`}>{statusLabel[i.status]}</span>
                  {live(i) && (
                    <div className="progress" role="progressbar" aria-label="التقدم" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
                      <span style={{ inlineSize: `${String(pct)}%` }} />
                    </div>
                  )}
                  <span className="muted small">
                    {i.total > 0 && `${String(i.processed)} من ${String(i.total)} · `}أُضيف {i.created} · حُدّث {i.updated}
                    {i.failed > 0 && ` · تخطّينا ${String(i.failed)}`}
                  </span>
                  {(i.row_errors.length > 0 || i.failure_reason) && (
                    <button
                      type="button"
                      className="link"
                      aria-expanded={shown}
                      onClick={() => {
                        setOpen(shown ? '' : i.id);
                      }}
                    >
                      {shown ? 'إخفاء التفاصيل' : 'التفاصيل'}
                    </button>
                  )}
                </div>
                {shown && (
                  <div className="import-report">
                    {i.failure_reason && <p className="field-error">{reasons[i.failure_reason] ?? i.failure_reason}</p>}
                    {i.row_errors.length > 0 && (
                      <table>
                        <caption>الصفوف التي تخطيناها</caption>
                        <thead>
                          <tr>
                            <th scope="col">السطر</th>
                            <th scope="col">السبب</th>
                          </tr>
                        </thead>
                        <tbody>
                          {i.row_errors.map((r) => (
                            <tr key={`${String(r.line)}-${r.message}`}>
                              <td className="num">{r.line}</td>
                              <td dir="auto">{r.message}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      </section>
    </Layout>
  );
}
