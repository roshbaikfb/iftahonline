import { useCallback, useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api, type components } from '../api/client';
import { cycleLabel, dateAr, statusLabel } from '../lib/billing';
import { telLink, waLink } from '../lib/customers';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { storefrontURL } from '../lib/storefront';
import { field } from '../session';
import type { Entry } from './audit';
import { AuditRow } from './AuditRow';
import { ConsoleLayout } from './ConsoleLayout';
import { countryName, storeCountries } from './countries';
import { BalanceCard } from './BalanceCard';
import { Dialog } from './Dialog';
import { dateTimeAr, initial, storeStatusLabel } from './lib';
import { currencyName } from './plans';
import { useAdmin, useCan } from './session';

type Detail = components['schemas']['AdminStoreDetail'];
type Billing = components['schemas']['Billing'];
type Grants = components['schemas']['AdminStoreEntitlements'];
type Feature = components['schemas']['AdminFeature'];
type Agency = components['schemas']['AdminAgency'];
type Grant = components['schemas']['AdminPlanGrant'];
type Profile = components['schemas']['AdminStoreProfile'];

// fetchStore reads everything the page shows, at once.
function fetchStore(storeId: string) {
  const p = { params: { path: { store_id: storeId } } };
  return Promise.all([
    api.GET('/admin/v1/stores/{store_id}', p),
    api.GET('/admin/v1/stores/{store_id}/billing', p),
    api.GET('/admin/v1/stores/{store_id}/entitlements', p),
    api.GET('/admin/v1/features'),
    api.GET('/admin/v1/agencies'),
    api.GET('/admin/v1/audit-log', { params: { query: { store: storeId, limit: 5 } } }),
    api.GET('/admin/v1/stores/{store_id}/profile', p),
  ]);
}

const grantText = (g: Grant | undefined, kind: Feature['kind'] | undefined) => {
  if (!g) return '—';
  if (g.unlimited) return 'بلا حد';
  if (kind === 'flag') return (g.value ?? 0) > 0 ? 'متاحة' : 'غير متاحة';
  return (g.value ?? 0).toLocaleString('en-US');
};

const dateLong = (iso: string) => new Date(iso).toLocaleDateString('ar-EG-u-nu-latn', { day: 'numeric', month: 'long', year: 'numeric' });

// StorePage is one store in the console (the approved mockup): its
// billing with a transfer to settle, its agency, a support session, its
// features with this store's exceptions, and suspending it in a closed
// danger zone. Every change asks for a reason, which the admin log keeps.
export function StorePage() {
  const { storeId = '' } = useParams();
  const path = { params: { path: { store_id: storeId } } };
  const [detail, setDetail] = useState<Detail | null>(null);
  const [billing, setBilling] = useState<Billing | null>(null);
  const [grants, setGrants] = useState<Grants | null>(null);
  const [features, setFeatures] = useState<Feature[]>([]);
  const [agencies, setAgencies] = useState<Agency[]>([]);
  const [history, setHistory] = useState<Entry[]>([]);
  const [profile, setProfile] = useState<Profile | null>(null);
  const { state } = useAdmin();
  const me = state.kind === 'signed-in' ? state.admin.id : '';
  // The role decides which actions show (ADR-0077); the server refuses the rest.
  const can = useCan();
  const [support, setSupport] = useState<{ url: string; expires: string } | null>(null);
  const [dialog, setDialog] = useState<{ kind: 'override' } | { kind: 'clear'; feature: Feature } | { kind: 'country' } | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchStore>>) => {
    const [d, b, g, f, a, h, pr] = r;
    if (d.data) setDetail(d.data);
    if (b.data) setBilling(b.data);
    if (g.data) setGrants(g.data);
    if (f.data) setFeatures(f.data.items);
    if (a.data) setAgencies(a.data.items);
    if (h.data) setHistory(h.data.items);
    if (pr.data) setProfile(pr.data);
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

  // act runs a change, then shows done and reads the store again. It
  // returns the refusal, for a dialog to show.
  async function act(call: () => Promise<{ error?: unknown }>, done: string, form?: HTMLFormElement): Promise<string> {
    setBusy(true);
    setError('');
    setNotice('');
    const { error: err } = await call();
    setBusy(false);
    if (err) {
      const msg = problemMessage(err);
      setError(msg);
      return msg;
    }
    form?.reset();
    setNotice(done);
    await load();
    return '';
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
        body: { reason: field(form, 'reason'), minutes: Number(field(form, 'minutes')), write: field(form, 'access') === 'write' },
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
      to === 'suspend' ? 'أُوقف المتجر.' : 'أُعيد تفعيل المتجر.',
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

  const store = detail?.store;
  const host = detail?.domains.find((d) => d.is_primary)?.host ?? detail?.domains[0]?.host ?? '';
  const enforced = features.filter((f) => f.enforced);
  const suspended = store?.status === 'suspended';
  const openInvoices = billing?.open_invoices.length ?? 0;

  return (
    <ConsoleLayout>
      <nav className="crumbs" aria-label="مسار الصفحة">
        <Link to="/stores">المتاجر</Link> / {store?.name ?? '…'}
      </nav>
      {error && !dialog && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      {store && (
        <div className="store-head">
          <div className="store-title">
            <span className="store-mark big" aria-hidden="true">
              {initial(store.name)}
            </span>
            <div>
              <h1>
                {store.name} <span className={`status store-${store.status}`}>{storeStatusLabel[store.status]}</span>
              </h1>
              <p className="muted">
                <bdi className="ltr">{host}</bdi> · {store.country ? `${countryName(store.country)} · ` : ''}
                {currencyName[store.currency]} · أُنشئ {dateLong(store.created_at)}
                {can('stores.manage') && (
                  <>
                    {' · '}
                    <button
                      type="button"
                      className="link-button"
                      onClick={() => {
                        setError('');
                        setDialog({ kind: 'country' });
                      }}
                    >
                      غيّر البلد
                    </button>
                  </>
                )}
              </p>
            </div>
          </div>
          <a className="button secondary" href={storefrontURL(store.slug)} target="_blank" rel="noopener noreferrer">
            فتح المتجر ↗
          </a>
        </div>
      )}

      <div className="store-grid">
        <section className="card store-card" aria-labelledby="h-owner">
          <h2 id="h-owner">صاحب المتجر</h2>
          {!profile ? (
            <p className="muted small">…</p>
          ) : !can('contacts.read') ? (
            <p className="muted">بيانات التواصل للمالك وفريق العمليات فقط.</p>
          ) : !profile.owner ? (
            <p className="muted">لا صاحب لهذا المتجر.</p>
          ) : (
            <>
              <dl className="store-facts">
                <dt>الاسم</dt>
                <dd>{profile.owner.name}</dd>
                <dt>البريد</dt>
                <dd>
                  <a className="ltr" href={`mailto:${profile.owner.email}`}>
                    {profile.owner.email}
                  </a>
                  {!profile.owner.email_verified && <span className="muted small"> · غير مؤكد</span>}
                </dd>
                <dt>الموبايل</dt>
                <dd>
                  {profile.owner.phone ? (
                    <span className="owner-phone">
                      <bdi className="ltr">{profile.owner.phone}</bdi>
                      <a href={telLink(profile.owner.phone)}>اتصال</a>
                      <a href={waLink(profile.owner.phone)} target="_blank" rel="noopener noreferrer">
                        واتساب
                      </a>
                    </span>
                  ) : (
                    <span className="muted">لم يضف رقمًا</span>
                  )}
                </dd>
                <dt>آخر دخول للوحة</dt>
                <dd className={profile.owner.last_seen_at ? '' : 'muted'}>{profile.owner.last_seen_at ? dateTimeAr(profile.owner.last_seen_at) : 'لم يدخل بعد'}</dd>
              </dl>
              <p className="muted small store-card-foot">للتواصل فقط. لا تُكتب هذه البيانات في أي سجل.</p>
            </>
          )}
        </section>
        <section className="card store-card" aria-labelledby="h-numbers">
          <h2 id="h-numbers">أرقام المتجر</h2>
          {profile && (
            <div className="store-numbers">
              <div>
                <span className="muted">طلبات آخر 30 يومًا</span>
                <strong>{profile.orders_30d.toLocaleString('en-US')}</strong>
              </div>
              <div>
                <span className="muted">آخر طلب</span>
                <strong className={profile.last_order_at ? 'date' : 'date muted'}>{profile.last_order_at ? dateTimeAr(profile.last_order_at) : 'لا طلبات بعد'}</strong>
              </div>
              <div>
                <span className="muted">المنتجات</span>
                <strong>{profile.products.toLocaleString('en-US')}</strong>
              </div>
            </div>
          )}
        </section>
      </div>

      <div className="store-grid">
        {billing && (
          <section className="card store-card" aria-labelledby="h-billing">
            <h2 id="h-billing">الفوترة</h2>
            <dl className="store-facts">
              <dt>الباقة</dt>
              <dd>
                {billing.plan_name_ar}
                {billing.cycle ? ` · ${cycleLabel[billing.cycle]}` : ''}
              </dd>
              <dt>الحالة</dt>
              <dd>
                <span className={`status bill-${billing.status}`}>{statusLabel[billing.status]}</span>
                {billing.status === 'trialing' && billing.trial_ends_at ? ` حتى ${dateAr(billing.trial_ends_at)}` : ''}
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
              <dd className={billing.card ? '' : 'muted'}>{billing.card ?? 'لا توجد'}</dd>
            </dl>
            {openInvoices === 0 ? (
              <p className="muted small store-card-foot">لا فواتير مفتوحة.</p>
            ) : (
              billing.open_invoices.map((inv) => (
                <div key={inv.id} className="console-invoice">
                  <div className="row">
                    <span className="ltr">{inv.number}</span>
                    <strong>{money(inv.total_minor, inv.currency)}</strong>
                  </div>
                  <p className="muted small">
                    مستحقة {dateAr(inv.due_at)} · محاولات {inv.attempts}
                  </p>
                  {can('billing.manage') && (
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
                  )}
                </div>
              ))
            )}
          </section>
        )}

        {store && can('billing.read') && <BalanceCard storeId={storeId} storeName={store.name} />}

        <section className="card store-card" aria-labelledby="h-agency">
          <h2 id="h-agency">الوكالة</h2>
          <p className="muted small">ينضم المتجر بشرط ألا تكون عليه فاتورة مفتوحة، ويأخذ باقة الوكالة وفترتها.</p>
          {!can('catalog.manage') ? (
            <p className="muted">ضمّ المتجر لوكالة للمالك والمالية.</p>
          ) : agencies.length === 0 ? (
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
              <button type="submit" className="button" disabled={busy || openInvoices > 0}>
                ضمّه للوكالة
              </button>
            </form>
          )}
          {openInvoices > 0 && <p className="warn-note">عليه فاتورة مفتوحة: سجّلها كمدفوعة أولًا.</p>}
        </section>
      </div>

      {can('stores.manage') && (
        <section className="card store-card" aria-labelledby="h-support">
          <div className="section-head">
            <h2 id="h-support">جلسة دعم بحساب صاحب المتجر</h2>
            <span className="muted small">تُسجَّل في سجل الأدمن وسجل المتجر، ولا تُمدَّد.</span>
          </div>
          <form className="support-form" onSubmit={impersonate}>
            <label className="grow">
              <span className="field-label">السبب</span>
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
            <fieldset className="segmented">
              <legend className="visually-hidden">الصلاحية</legend>
              <label>
                <input type="radio" name="access" value="read" defaultChecked /> للقراءة فقط
              </label>
              <label>
                <input type="radio" name="access" value="write" /> مسموح بالتعديل
              </label>
            </fieldset>
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
      )}

      {grants && (
        <section className="table-card" aria-labelledby="h-features">
          <div className="card-head wrap">
            <div>
              <h2 id="h-features">مميزات المتجر</h2>
              <span className="muted small">من الباقة، إلا ما استثنيته لهذا المتجر وحده.</span>
            </div>
            {can('stores.manage') && (
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  setError('');
                  setDialog({ kind: 'override' });
                }}
              >
                استثناء جديد
              </button>
            )}
          </div>
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">الميزة</th>
                  <th scope="col">من الباقة</th>
                  <th scope="col">الاستثناء</th>
                  <th scope="col">الفعلي</th>
                </tr>
              </thead>
              <tbody>
                {enforced.map((f) => {
                  const o = grants.overrides.find((x) => x.feature === f.key);
                  return (
                    <tr key={f.key} className={o?.active ? 'overridden' : undefined}>
                      <th scope="row">{f.name_ar}</th>
                      <td className="muted">{grantText(grants.plan.find((g) => g.feature === f.key), f.kind)}</td>
                      <td>
                        {o ? (
                          <>
                            <strong className={o.active ? '' : 'muted'}>{grantText(o, f.kind)}</strong>
                            <div className="muted small">
                              السبب: {o.reason}
                              {o.expires_at ? ` · حتى ${dateAr(o.expires_at)}` : ''}
                              {o.active ? '' : ' · منتهٍ'}
                              {can('stores.manage') && ' · '}
                              {can('stores.manage') && (
                                <button
                                  type="button"
                                  className="link-button danger"
                                  disabled={busy}
                                  onClick={() => {
                                    setError('');
                                    setDialog({ kind: 'clear', feature: f });
                                  }}
                                >
                                  إزالة
                                </button>
                              )}
                            </div>
                          </>
                        ) : (
                          <span className="muted">—</span>
                        )}
                      </td>
                      <td>
                        <strong>{grantText(grants.effective.find((g) => g.feature === f.key), f.kind)}</strong>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {can('audit.read') && (
        <section className="table-card" aria-labelledby="h-history">
          <div className="card-head">
            <h2 id="h-history">آخر إجراءات الأدمن على هذا المتجر</h2>
            <Link to={`/audit?store=${storeId}`}>كل سجله ←</Link>
          </div>
          {history.length === 0 ? (
            <p className="empty flat">لم يفعل أحد شيئًا بهذا المتجر بعد.</p>
          ) : (
            <ol className="audit-list flat">
              {history.map((e) => (
                <AuditRow key={e.id} entry={e} me={me} features={features} withDate />
              ))}
            </ol>
          )}
        </section>
      )}

      {store && can('stores.manage') && (
        <details className="card plan-fold danger-zone">
          <summary>{suspended ? 'إعادة تفعيل المتجر' : 'منطقة خطرة: إيقاف المتجر'}</summary>
          <form
            onSubmit={(e) => {
              setStatus(e, suspended ? 'reactivate' : 'suspend');
            }}
            aria-label={suspended ? 'إعادة تفعيل المتجر' : 'إيقاف المتجر'}
          >
            <p className="muted">
              {suspended ? 'يعود المتجر للعمل فور تفعيله. يُسجَّل السبب في سجل الأدمن.' : 'يتوقف المتجر عن العمل حتى تعيد تفعيله. يُسجَّل السبب في سجل الأدمن.'}
            </p>
            <label>
              <span className="field-label">السبب</span>
              <input name="reason" required minLength={3} maxLength={500} />
            </label>
            <button type="submit" className={suspended ? 'success' : 'danger-solid'} disabled={busy}>
              {suspended ? `أعد تفعيل «${store.name}»` : `أوقف «${store.name}»`}
            </button>
          </form>
        </details>
      )}

      {dialog?.kind === 'override' && store && grants && (
        <OverrideDialog
          storeName={store.name}
          features={enforced}
          plan={grants.plan}
          error={error}
          busy={busy}
          onSave={async (feature, body) => {
            const refused = await act(
              () => api.PUT('/admin/v1/stores/{store_id}/entitlements/{feature}', { params: { path: { store_id: storeId, feature } }, body }),
              'حُفظ الاستثناء.',
            );
            if (!refused) setDialog(null);
          }}
          onClose={() => {
            setDialog(null);
          }}
        />
      )}
      {dialog?.kind === 'country' && store && (
        <Dialog
          title="تغيير بلد المتجر"
          error={error}
          onSubmit={(e) => {
            e.preventDefault();
            const form = new FormData(e.currentTarget);
            void act(
              () => api.PUT('/admin/v1/stores/{store_id}/country', { ...path, body: { country: field(form, 'country'), reason: field(form, 'reason') } }),
              'غُيّر بلد المتجر.',
            ).then((refused) => {
              if (!refused) setDialog(null);
            });
          }}
          onClose={() => {
            setDialog(null);
          }}
        >
          <label>
            <span className="field-label">البلد الجديد</span>
            <select name="country" required defaultValue={storeCountries.find((c) => c !== store.country)}>
              {storeCountries
                .filter((c) => c !== store.country)
                .map((c) => (
                  <option key={c} value={c}>
                    {countryName(c)}
                  </option>
                ))}
            </select>
          </label>
          <ul className="change-list" aria-label="ما يتغير">
            <li>
              <strong>ما يتغير</strong>
            </li>
            <li>• بوابات البطاقات المتاحة للمتجر حسب قاعدة البلد الجديد، للربط الجديد فقط.</li>
            <li>• الضريبة على فواتيره القادمة حسب جدول البلد الجديد.</li>
          </ul>
          <ul className="change-list keeps" aria-label="ما لا يتغير">
            <li>
              <strong>ما لا يتغير</strong>
            </li>
            <li>• حساب الفوترة المفتوح يحتفظ بعملته.</li>
            <li>• عملة المتجر وأسعار منتجاته.</li>
            <li>• بوابة البطاقات المربوطة تبقى تعمل حتى يفصلها التاجر.</li>
          </ul>
          <label>
            <span className="field-label">السبب (إجباري، يُحفظ في سجل الأدمن)</span>
            <textarea name="reason" required minLength={3} maxLength={500} rows={3} />
          </label>
          <div className="actions">
            <button type="submit" className="primary" disabled={busy}>
              غيّر البلد
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setDialog(null);
              }}
            >
              إلغاء
            </button>
          </div>
        </Dialog>
      )}
      {dialog?.kind === 'clear' && (
        <Dialog
          title={`إزالة استثناء «${dialog.feature.name_ar}»`}
          intro="يعود المتجر لما تمنحه باقته في هذه الميزة فورًا."
          error={error}
          onSubmit={(e) => {
            e.preventDefault();
            const reason = field(new FormData(e.currentTarget), 'reason');
            void act(
              () =>
                api.POST('/admin/v1/stores/{store_id}/entitlements/{feature}/clear', {
                  params: { path: { store_id: storeId, feature: dialog.feature.key } },
                  body: { reason },
                }),
              'أُزيل الاستثناء.',
            ).then((refused) => {
              if (!refused) setDialog(null);
            });
          }}
          onClose={() => {
            setDialog(null);
          }}
        >
          <label>
            <span className="field-label">السبب</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <div className="actions">
            <button type="submit" className="danger-solid" disabled={busy}>
              أزل الاستثناء
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setDialog(null);
              }}
            >
              إلغاء
            </button>
          </div>
        </Dialog>
      )}
    </ConsoleLayout>
  );
}

type OverrideInput = components['schemas']['AdminOverrideInput'];

// OverrideDialog sets one feature for this store alone (the approved
// mockup's «استثناء جديد»): a switch for a flag, a number or «بلا حد»
// for a limit, until an optional date.
function OverrideDialog({
  storeName,
  features,
  plan,
  error,
  busy,
  onSave,
  onClose,
}: {
  storeName: string;
  features: Feature[];
  plan: Grant[];
  error: string;
  busy: boolean;
  onSave: (feature: string, body: OverrideInput) => Promise<void>;
  onClose: () => void;
}) {
  const [key, setKey] = useState(features[0]?.key ?? '');
  const [unlimited, setUnlimited] = useState(false);
  const feature = features.find((f) => f.key === key);
  const flag = feature?.kind === 'flag';

  function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const until = field(form, 'until');
    const body: OverrideInput = { reason: field(form, 'reason'), unlimited: !flag && unlimited };
    if (flag) body.value = form.get('on') === 'on' ? 1 : 0;
    else if (!unlimited) body.value = Number(field(form, 'value'));
    if (until) body.expires_at = new Date(`${until}T23:59:59Z`).toISOString();
    void onSave(key, body);
  }

  return (
    <Dialog title={`استثناء جديد لـ «${storeName}»`} intro="يحل محل ما تمنحه الباقة لهذا المتجر وحده، ويُطبَّق فورًا." error={error} onSubmit={save} onClose={onClose}>
      <label>
        <span className="field-label">الميزة</span>
        <select
          name="feature"
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
            setUnlimited(false);
          }}
        >
          {features.map((f) => (
            <option key={f.key} value={f.key}>
              {f.name_ar} — من الباقة {grantText(plan.find((g) => g.feature === f.key), f.kind)}
            </option>
          ))}
        </select>
      </label>
      {flag ? (
        <label className="switch-row">
          <span>متاحة لهذا المتجر</span>
          <input type="checkbox" role="switch" name="on" defaultChecked />
        </label>
      ) : (
        <div className="limit-pair">
          <label>
            <span className="field-label">الحد الجديد</span>
            <input name="value" inputMode="numeric" dir="ltr" pattern="[0-9]+" required={!unlimited} disabled={unlimited} defaultValue="" />
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={unlimited}
              onChange={(e) => {
                setUnlimited(e.target.checked);
              }}
            />{' '}
            بلا حد
          </label>
        </div>
      )}
      <div className="field">
        <label>
          <span className="field-label">حتى (اختياري)</span>
          <input name="until" type="date" aria-describedby="until-hint" />
        </label>
        <small id="until-hint" className="muted">
          بعده يعود المتجر لما تمنحه باقته.
        </small>
      </div>
      <label>
        <span className="field-label">السبب</span>
        <input name="reason" required minLength={3} maxLength={500} />
      </label>
      <div className="actions">
        <button type="submit" className="button primary" disabled={busy}>
          حفظ الاستثناء
        </button>
        <button type="button" className="secondary" onClick={onClose}>
          إلغاء
        </button>
      </div>
    </Dialog>
  );
}
