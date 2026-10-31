import { useCallback, useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { cycleLabel, dateAr, statusLabel } from '../lib/billing';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { dateTimeAr, storeStatusLabel } from './lib';

type Detail = components['schemas']['AdminStoreDetail'];
type Billing = components['schemas']['Billing'];
type Grants = components['schemas']['AdminStoreEntitlements'];
type Feature = components['schemas']['AdminFeature'];
type Agency = components['schemas']['AdminAgency'];
type Grant = components['schemas']['AdminPlanGrant'];

// fetchStore reads everything the page shows, at once.
function fetchStore(storeId: string) {
  const p = { params: { path: { store_id: storeId } } };
  return Promise.all([
    api.GET('/admin/v1/stores/{store_id}', p),
    api.GET('/admin/v1/stores/{store_id}/billing', p),
    api.GET('/admin/v1/stores/{store_id}/entitlements', p),
    api.GET('/admin/v1/features'),
    api.GET('/admin/v1/agencies'),
  ]);
}

const grantText = (g: Grant | undefined, kind: Feature['kind'] | undefined) => {
  if (!g) return '—';
  if (g.unlimited) return 'بلا حد';
  if (kind === 'flag') return (g.value ?? 0) > 0 ? 'مفعّلة' : 'متوقفة';
  return (g.value ?? 0).toLocaleString('en-US');
};

// StorePage is one store in the console (the approved mockup): a support
// session, suspension, its billing with a transfer to settle, its agency,
// and its per-store feature flags. Every change asks for a reason, which
// the admin log keeps.
export function StorePage() {
  const { storeId = '' } = useParams();
  const path = { params: { path: { store_id: storeId } } };
  const [detail, setDetail] = useState<Detail | null>(null);
  const [billing, setBilling] = useState<Billing | null>(null);
  const [grants, setGrants] = useState<Grants | null>(null);
  const [features, setFeatures] = useState<Feature[]>([]);
  const [agencies, setAgencies] = useState<Agency[]>([]);
  const [support, setSupport] = useState<{ url: string; expires: string } | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchStore>>) => {
    const [d, b, g, f, a] = r;
    if (d.data) setDetail(d.data);
    if (b.data) setBilling(b.data);
    if (g.data) setGrants(g.data);
    if (f.data) setFeatures(f.data.items);
    if (a.data) setAgencies(a.data.items);
    const err = d.error ?? b.error ?? g.error;
    if (err) setError(problemMessage(err));
  }, []);

  useEffect(() => {
    let live = true;
    void fetchStore(storeId).then((r) => {
      if (live) apply(r);
    });
    return () => {
      live = false;
    };
  }, [storeId, apply]);

  const load = async () => {
    apply(await fetchStore(storeId));
  };

  // act runs a change, then shows done and reads the store again.
  async function act(call: () => Promise<{ error?: unknown }>, done: string, form?: HTMLFormElement) {
    setBusy(true);
    setError('');
    setNotice('');
    const { error: err } = await call();
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    form?.reset();
    setNotice(done);
    await load();
  }

  function impersonate(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    setSupport(null);
    void api
      .POST('/admin/v1/stores/{store_id}/impersonate', {
        ...path,
        body: { reason: field(form, 'reason'), minutes: Number(field(form, 'minutes')), write: form.get('write') === 'on' },
      })
      .then(({ data, error: err }) => {
        setBusy(false);
        if (err) {
          setError(problemMessage(err));
          return;
        }
        setSupport({ url: data.url, expires: data.expires_at });
      });
  }

  function setStatus(e: SubmitEvent<HTMLFormElement>, to: 'suspend' | 'reactivate') {
    e.preventDefault();
    const el = e.currentTarget;
    const reason = field(new FormData(el), 'reason');
    void act(
      () =>
        to === 'suspend'
          ? api.POST('/admin/v1/stores/{store_id}/suspend', { ...path, body: { reason } })
          : api.POST('/admin/v1/stores/{store_id}/reactivate', { ...path, body: { reason } }),
      to === 'suspend' ? 'أُوقف المتجر.' : 'أُعيد تشغيل المتجر.',
      el,
    );
  }

  function settle(e: SubmitEvent<HTMLFormElement>, invoice: string) {
    e.preventDefault();
    const el = e.currentTarget;
    const form = new FormData(el);
    void act(
      () =>
        api.POST('/admin/v1/stores/{store_id}/invoices/{invoice_id}/settle', {
          params: { path: { store_id: storeId, invoice_id: invoice } },
          body: { reference: field(form, 'reference'), reason: field(form, 'reason') },
        }),
      'سُجّلت الفاتورة مدفوعة.',
      el,
    );
  }

  function joinAgency(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    const form = new FormData(el);
    void act(
      () =>
        api.POST('/admin/v1/agencies/{agency_id}/stores/{store_id}', {
          params: { path: { agency_id: field(form, 'agency'), store_id: storeId } },
          body: { reason: field(form, 'reason') },
        }),
      'انضم المتجر للوكالة.',
      el,
    );
  }

  function override(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    const form = new FormData(el);
    const until = field(form, 'until');
    const unlimited = form.get('unlimited') === 'on';
    const body: components['schemas']['AdminOverrideInput'] = { reason: field(form, 'reason'), unlimited };
    if (!unlimited) body.value = Number(field(form, 'value'));
    if (until) body.expires_at = new Date(`${until}T23:59:59Z`).toISOString();
    void act(
      () => api.PUT('/admin/v1/stores/{store_id}/entitlements/{feature}', { params: { path: { store_id: storeId, feature: field(form, 'feature') } }, body }),
      'حُفظ الاستثناء.',
      el,
    );
  }

  function clear(feature: string) {
    const reason = window.prompt('سبب إزالة الاستثناء');
    if (!reason) return;
    void act(
      () => api.POST('/admin/v1/stores/{store_id}/entitlements/{feature}/clear', { params: { path: { store_id: storeId, feature } }, body: { reason } }),
      'أُزيل الاستثناء.',
    );
  }

  const store = detail?.store;
  const host = detail?.domains.find((d) => d.is_primary)?.host ?? detail?.domains[0]?.host ?? '';
  const enforced = features.filter((f) => f.enforced);

  return (
    <ConsoleLayout>
      <Link to="/">→ المتاجر</Link>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      {store && (
        <div className="page-head">
          <div>
            <h1>
              {store.name} <span className={`status store-${store.status}`}>{storeStatusLabel[store.status]}</span>
            </h1>
            <p className="muted ltr">
              {host} · {store.currency} · {store.locale}
            </p>
          </div>
          <details className="console-danger">
            <summary className="button">{store.status === 'suspended' ? 'إعادة تشغيل المتجر' : 'إيقاف المتجر'}</summary>
            <form className="inline-form" aria-label={store.status === 'suspended' ? 'إعادة تشغيل المتجر' : 'إيقاف المتجر'} onSubmit={(e) => {
                setStatus(e, store.status === 'suspended' ? 'reactivate' : 'suspend'); }}>
              <label>
                <span className="field-label">السبب</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <button type="submit" className="button" disabled={busy}>
                {store.status === 'suspended' ? 'أعد التشغيل' : 'أوقف'}
              </button>
            </form>
          </details>
        </div>
      )}

      <section className="card support-card" aria-labelledby="h-support">
        <h2 id="h-support">جلسة دعم بحساب صاحب المتجر</h2>
        <p className="muted small">تُسجَّل في سجل الأدمن وسجل المتجر، وكل طلب فيها يُسجَّل. لا تُمدَّد وتنتهي وحدها.</p>
        <form className="inline-form" onSubmit={impersonate}>
          <label className="grow">
            <span className="field-label">السبب (إجباري)</span>
            <input name="reason" required minLength={3} maxLength={500} placeholder="مثال: تذكرة 4821 — مشكلة في صفحة المنتج" />
          </label>
          <label>
            <span className="field-label">المدة</span>
            <select name="minutes" defaultValue="30">
              <option value="15">15 دقيقة</option>
              <option value="30">30 دقيقة</option>
              <option value="60">60 دقيقة</option>
            </select>
          </label>
          <label className="check">
            <input type="checkbox" name="write" /> السماح بالتعديل (الافتراضي قراءة فقط)
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            افتح جلسة الدعم
          </button>
        </form>
        {support && (
          <p className="ok-note">
            <a href={support.url} target="_blank" rel="noopener noreferrer">
              افتح جلسة الدعم في الداشبورد
            </a>{' '}
            — الرابط صالح دقيقة واحدة ولمرة واحدة، والجلسة تنتهي {dateTimeAr(support.expires)}.
          </p>
        )}
      </section>

      <div className="bill-grid">
        {billing && (
          <section className="card" aria-labelledby="h-billing">
            <h2 id="h-billing">الفوترة</h2>
            <dl className="facts">
              <dt>الباقة</dt>
              <dd>
                {billing.plan_name_ar}
                {billing.cycle ? ` · ${cycleLabel[billing.cycle]}` : ''}
              </dd>
              <dt>الحالة</dt>
              <dd>
                <span className={`status bill-${billing.status}`}>{statusLabel[billing.status]}</span>
                {billing.grace_until ? ` حتى ${dateAr(billing.grace_until)}` : ''}
              </dd>
              {billing.period_end && (
                <>
                  <dt>نهاية الفترة</dt>
                  <dd>{dateAr(billing.period_end)}</dd>
                </>
              )}
              {billing.next_plan_name_ar && (
                <>
                  <dt>بعد الفترة</dt>
                  <dd>{billing.next_plan_name_ar}</dd>
                </>
              )}
              <dt>البطاقة</dt>
              <dd>{billing.card ?? '—'}</dd>
            </dl>
            {billing.open_invoices.map((inv) => (
              <div key={inv.id} className="console-invoice">
                <div className="row">
                  <span className="ltr">{inv.number}</span>
                  <strong>{money(inv.total_minor, inv.currency)}</strong>
                </div>
                <p className="muted small">
                  مستحقة {dateAr(inv.due_at)} · محاولات {inv.attempts}
                </p>
                <form className="inline-form" onSubmit={(e) => { settle(e, inv.id); }} aria-label={`تسجيل ${inv.number} مدفوعة`}>
                  <label>
                    <span className="field-label">رقم التحويل</span>
                    <input name="reference" required maxLength={120} />
                  </label>
                  <label className="grow">
                    <span className="field-label">السبب</span>
                    <input name="reason" required minLength={3} maxLength={500} />
                  </label>
                  <button type="submit" className="button" disabled={busy}>
                    سجّل كمدفوعة
                  </button>
                </form>
              </div>
            ))}
          </section>
        )}

        <section className="card" aria-labelledby="h-agency">
          <h2 id="h-agency">الوكالة</h2>
          <p className="muted small">ينضم المتجر بشرط ألا تكون عليه فاتورة مفتوحة، ويأخذ باقة الوكالة وفترتها.</p>
          {agencies.length === 0 ? (
            <p className="muted">لا توجد وكالات بعد.</p>
          ) : (
            <form className="inline-form" onSubmit={joinAgency}>
              <label className="grow">
                <span className="field-label">الوكالة</span>
                <select name="agency" required>
                  {agencies.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name} ({a.currency} · {a.stores} متجر)
                    </option>
                  ))}
                </select>
              </label>
              <label className="grow">
                <span className="field-label">السبب</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <button type="submit" className="button" disabled={busy || (billing?.open_invoices.length ?? 0) > 0}>
                ضمّه للوكالة
              </button>
            </form>
          )}
          {(billing?.open_invoices.length ?? 0) > 0 && <p className="warn-note">عليه فاتورة مفتوحة: سجّلها كمدفوعة أولًا.</p>}
        </section>
      </div>

      {grants && (
        <section className="table-card" aria-labelledby="h-features">
          <div className="card-head wrap">
            <h2 id="h-features">مميزات هذا المتجر</h2>
            <span className="muted small">استثناء يحل محل ما تمنحه الباقة لهذا المتجر وحده، ويطبَّق فورًا.</span>
          </div>
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">الميزة</th>
                  <th scope="col">من الباقة</th>
                  <th scope="col">الاستثناء</th>
                  <th scope="col">الفعلي</th>
                  <th scope="col">
                    <span className="visually-hidden">إجراء</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {enforced.map((f) => {
                  const o = grants.overrides.find((x) => x.feature === f.key);
                  return (
                    <tr key={f.key}>
                      <th scope="row">{f.name_ar}</th>
                      <td className="muted">{grantText(grants.plan.find((g) => g.feature === f.key), f.kind)}</td>
                      <td>
                        {o ? (
                          <>
                            <strong className={o.active ? '' : 'muted'}>{grantText(o, f.kind)}</strong>
                            <div className="muted small">
                              {o.reason}
                              {o.expires_at ? ` · حتى ${dateAr(o.expires_at)}` : ''}
                              {o.active ? '' : ' · منتهٍ'}
                            </div>
                          </>
                        ) : (
                          <span className="muted">—</span>
                        )}
                      </td>
                      <td>
                        <strong>{grantText(grants.effective.find((g) => g.feature === f.key), f.kind)}</strong>
                      </td>
                      <td>
                        {o && (
                          <button type="button" className="link-button danger" disabled={busy} onClick={() => { clear(f.key); }}>
                            إزالة
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <form className="inline-form foot" onSubmit={override} aria-label="استثناء جديد">
            <label>
              <span className="field-label">الميزة</span>
              <select name="feature" required>
                {enforced.map((f) => (
                  <option key={f.key} value={f.key}>
                    {f.name_ar}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span className="field-label">القيمة (للتشغيل/الإيقاف: 1 أو 0)</span>
              <input name="value" type="number" min={0} defaultValue={1} />
            </label>
            <label className="check">
              <input type="checkbox" name="unlimited" /> بلا حد
            </label>
            <label>
              <span className="field-label">حتى (اختياري)</span>
              <input name="until" type="date" />
            </label>
            <label className="grow">
              <span className="field-label">السبب</span>
              <input name="reason" required minLength={3} maxLength={500} />
            </label>
            <button type="submit" className="button primary" disabled={busy}>
              حفظ الاستثناء
            </button>
          </form>
        </section>
      )}
    </ConsoleLayout>
  );
}
