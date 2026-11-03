import { useState, type SubmitEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router';

import { api } from '../api/client';
import { toMinor } from '../lib/amounts';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { useCatalog } from './catalog';
import { ConsoleLayout } from './ConsoleLayout';
import {
  byCode,
  currencies,
  currencyName,
  cycles,
  draftFrom,
  featureKindLabel,
  kindLabel,
  paidKind,
  priceKey,
  saving,
  stores,
  subscribersOf,
  toInput,
  type Draft,
  type Feature,
  type Kind,
  type Plan,
} from './plans';

const symbol = { EGP: 'ج.م', SAR: 'ر.س', AED: 'د.إ' } as const;

// PlanEditorPage is one plan's page (the approved mockup): its basics,
// prices or fees, what it grants, a sticky bar that publishes the next
// version, its versions, and retiring it. /plans/new starts a new plan.
export function PlanEditorPage() {
  const { code = '' } = useParams();
  const { plans, features, loaded, error, reload } = useCatalog();
  const navigate = useNavigate();
  const isNew = code === 'new';
  const group = byCode(plans).find((g) => g.code === code);

  return (
    <ConsoleLayout>
      <nav className="crumbs" aria-label="مسار الصفحة">
        <Link to="/plans">الباقات</Link> / {isNew ? 'باقة جديدة' : (group?.head.name_ar ?? code)}
      </nav>
      {error && <p role="alert">{error}</p>}
      {!loaded ? (
        <p className="muted">جارٍ التحميل…</p>
      ) : !isNew && !group ? (
        <p className="empty">
          لا توجد باقة بهذا الكود. <Link to="/plans">عودة إلى الباقات</Link>
        </p>
      ) : (
        <Editor
          key={code}
          versions={group?.versions ?? []}
          head={group?.head}
          features={features}
          onCreated={async (created) => {
            await reload();
            void navigate(`/plans/${created}`);
          }}
        />
      )}
    </ConsoleLayout>
  );
}

function Editor({ head, versions, features, onCreated }: { head: Plan | undefined; versions: Plan[]; features: Feature[]; onCreated: (code: string) => Promise<void> }) {
  const isNew = !head;
  const [draft, setDraft] = useState<Draft>(() => draftFrom(head));
  const [current, setCurrent] = useState({ head, versions });
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const edit = (patch: Partial<Draft>) => {
    setDraft((d) => ({ ...d, ...patch }));
  };
  const next = (current.versions[0]?.version ?? 0) + 1;
  const subscribers = subscribersOf(current.versions);
  const sorted = [...features].sort((a, b) => a.sort - b.sort);
  const limits = sorted.filter((f) => f.kind !== 'flag');
  const flags = sorted.filter((f) => f.kind === 'flag');
  const grant = (key: string) => draft.grants[key] ?? { value: '', unlimited: false };
  const setGrant = (key: string, patch: Partial<Draft['grants'][string]>) => {
    edit({ grants: { ...draft.grants, [key]: { ...grant(key), ...patch } } });
  };

  async function publish(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const input = toInput(draft, field(new FormData(e.currentTarget), 'reason').trim());
    if (typeof input === 'string') {
      setError(input);
      return;
    }
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/admin/v1/plans', { body: input });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    if (isNew) {
      await onCreated(data.code);
      return;
    }
    setCurrent({ head: data, versions: [data, ...current.versions.map((v) => ({ ...v, active: false }))] });
    setNotice(`نُشر ${data.name_ar} v${String(data.version)}. المشتركون ينتقلون إليه عند تجديدهم القادم.`);
  }

  async function retire(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!current.head) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/admin/v1/plans/{code}/retire', {
      params: { path: { code: current.head.code } },
      body: { reason: field(new FormData(e.currentTarget), 'reason').trim() },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setCurrent({ head: { ...current.head, active: false }, versions: current.versions.map((v) => ({ ...v, active: false })) });
    setNotice('أُوقف بيع الباقة. المشتركون عليها يبقون ويجددون الإصدار نفسه.');
  }

  return (
    <div className="plan-editor-page">
      <div className="plan-title">
        <h1>{isNew ? 'باقة جديدة' : draft.name_ar || current.head?.name_ar}</h1>
        <span className={`status kind-${draft.kind}`}>{kindLabel[draft.kind]}</span>
        {current.head && !current.head.active ? (
          <span className="status store-closed">متوقفة عن البيع</span>
        ) : draft.public ? (
          <span className="status store-active">معروضة</span>
        ) : (
          <span className="status">مخفية</span>
        )}
        {!isNew && (
          <span className="muted small">
            {current.head?.active ? `على البيع: الإصدار v${String(current.head.version)} · ` : ''}الحفظ ينشر v{next}
          </span>
        )}
      </div>
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <form className="plan-form" onSubmit={(e) => void publish(e)} aria-label={isNew ? 'باقة جديدة' : `إصدار جديد من ${draft.name_ar}`}>
        <section className="card plan-section" aria-labelledby="h-basics">
          <h2 id="h-basics">الأساسيات</h2>
          <div className="plan-grid">
            {isNew && (
              <>
                <div className="field">
                  <label>
                    <span className="field-label">الكود</span>
                    <input value={draft.code} onChange={(e) => { edit({ code: e.target.value }); }} required pattern="[a-z][a-z_]{1,31}" dir="ltr" aria-describedby="code-hint" />
                  </label>
                  <small id="code-hint" className="muted">حروف إنجليزية صغيرة. لا يتغير بعد النشر.</small>
                </div>
                <label>
                  <span className="field-label">النوع</span>
                  <select value={draft.kind} onChange={(e) => { edit({ kind: e.target.value as Kind }); }}>
                    {(Object.keys(kindLabel) as Kind[]).map((k) => (
                      <option key={k} value={k}>
                        {kindLabel[k]}
                      </option>
                    ))}
                  </select>
                </label>
              </>
            )}
            <label>
              <span className="field-label">الاسم بالعربية</span>
              <input value={draft.name_ar} onChange={(e) => { edit({ name_ar: e.target.value }); }} required maxLength={80} />
            </label>
            <label>
              <span className="field-label">الاسم بالإنجليزية</span>
              <input value={draft.name_en} onChange={(e) => { edit({ name_en: e.target.value }); }} required maxLength={80} dir="ltr" />
            </label>
            <div className="field">
              <label>
                <span className="field-label">أيام التجربة</span>
                <input value={draft.trial_days} onChange={(e) => { edit({ trial_days: e.target.value }); }} inputMode="numeric" aria-describedby="trial-hint" />
              </label>
              <small id="trial-hint" className="muted">0 بلا تجربة. حتى 90 يومًا.</small>
            </div>
            <div className="field">
              <label>
                <span className="field-label">الترتيب في صفحة الباقات</span>
                <input value={draft.sort} onChange={(e) => { edit({ sort: e.target.value }); }} inputMode="numeric" aria-describedby="sort-hint" />
              </label>
              <small id="sort-hint" className="muted">الأصغر يظهر أولًا.</small>
            </div>
          </div>
          <label className="switch-row">
            <span>
              <strong>معروضة للتجار</strong>
              <small className="muted">المخفية لا تظهر في صفحة الفوترة، ومشتركوها يبقون عليها.</small>
            </span>
            <input type="checkbox" role="switch" checked={draft.public} onChange={(e) => { edit({ public: e.target.checked }); }} />
          </label>
        </section>

        {paidKind(draft.kind) && (
          <section className="card plan-section" aria-labelledby="h-prices">
            <div className="section-head">
              <h2 id="h-prices">الأسعار</h2>
              <span className="muted small">قبل الضريبة. السنوي يُعرض للتاجر مع ما يوفّره.</span>
            </div>
            <div className="price-rows">
              {currencies.map((c) => {
                const m = toMinor(draft.prices[priceKey(c, 'monthly')] ?? '');
                const y = toMinor(draft.prices[priceKey(c, 'annual')] ?? '');
                const save = saving(m, y);
                return (
                  <div key={c} className="price-row">
                    <strong>{currencyName[c]}</strong>
                    {cycles.map((cy) => (
                      <label key={cy}>
                        <span className="field-label">{cy === 'monthly' ? 'شهري' : 'سنوي'}</span>
                        <span className="amount">
                          <input
                            aria-label={`${c} ${cy === 'monthly' ? 'شهري' : 'سنوي'}`}
                            value={draft.prices[priceKey(c, cy)] ?? ''}
                            onChange={(e) => { edit({ prices: { ...draft.prices, [priceKey(c, cy)]: e.target.value } }); }}
                            inputMode="decimal"
                            dir="ltr"
                          />
                          <span className="muted">{symbol[c]}</span>
                          {cy === 'annual' && save && <span className="status store-active">{save}</span>}
                        </span>
                      </label>
                    ))}
                  </div>
                );
              })}
            </div>
          </section>
        )}

        {draft.kind === 'pays' && (
          <section className="card plan-section" aria-labelledby="h-fees">
            <div className="section-head">
              <h2 id="h-fees">العمولة على الطلبات المسلّمة</h2>
              <span className="muted small">اترك الحد فارغًا إن لم يكن له حد.</span>
            </div>
            <div className="price-rows">
              {currencies.map((c) => {
                const f = draft.fees[c] ?? { rate: '', min: '', max: '' };
                const set = (patch: Partial<typeof f>) => {
                  edit({ fees: { ...draft.fees, [c]: { ...f, ...patch } } });
                };
                return (
                  <div key={c} className="price-row fee-row">
                    <strong>{currencyName[c]}</strong>
                    <label>
                      <span className="field-label">النسبة %</span>
                      <input aria-label={`${c} النسبة`} value={f.rate} onChange={(e) => { set({ rate: e.target.value }); }} inputMode="decimal" dir="ltr" />
                    </label>
                    <label>
                      <span className="field-label">حد أدنى</span>
                      <input aria-label={`${c} أقل رسوم`} value={f.min} onChange={(e) => { set({ min: e.target.value }); }} inputMode="decimal" dir="ltr" />
                    </label>
                    <label>
                      <span className="field-label">حد أقصى</span>
                      <input aria-label={`${c} أقصى رسوم`} value={f.max} onChange={(e) => { set({ max: e.target.value }); }} inputMode="decimal" dir="ltr" />
                    </label>
                  </div>
                );
              })}
            </div>
          </section>
        )}

        <section className="card plan-section" aria-labelledby="h-grants">
          <div className="section-head">
            <h2 id="h-grants">ما تمنحه الباقة</h2>
            <Link className="small" to="/plans?tab=features">
              كتالوج المميزات
            </Link>
          </div>
          {limits.length > 0 && <h3>الحدود</h3>}
          {limits.map((f) => {
            const g = grant(f.key);
            return (
              <div key={f.key} className="limit-row">
                <span>
                  <strong>{f.name_ar}</strong> {!f.enforced && <span className="status">عرض فقط</span>}
                  <small className="muted">{g.unlimited ? 'بلا حد' : g.value.trim() ? featureKindLabel[f.kind] : 'غير متاحة في هذه الباقة'}</small>
                </span>
                <span className="limit-input">
                  <input aria-label={f.name_ar} value={g.unlimited ? '' : g.value} disabled={g.unlimited} onChange={(e) => { setGrant(f.key, { value: e.target.value }); }} inputMode="numeric" dir="ltr" />
                  <label className="check">
                    <input type="checkbox" checked={g.unlimited} onChange={(e) => { setGrant(f.key, { unlimited: e.target.checked }); }} /> بلا حد
                  </label>
                </span>
              </div>
            );
          })}
          {flags.length > 0 && <h3>المميزات</h3>}
          <div className="flag-grid">
            {flags.map((f) => {
              const on = grant(f.key).value.trim() !== '' && grant(f.key).value.trim() !== '0';
              return (
                <label key={f.key} className="switch-row">
                  <span>
                    {f.name_ar} {!f.enforced && <span className="status">عرض فقط</span>}
                  </span>
                  <span className="switch-state">
                    <small className={on ? 'on' : 'muted'}>{on ? 'متاحة' : 'غير متاحة'}</small>
                    <input type="checkbox" role="switch" aria-label={f.name_ar} checked={on} onChange={(e) => { setGrant(f.key, { value: e.target.checked ? '1' : '0', unlimited: false }); }} />
                  </span>
                </label>
              );
            })}
          </div>
        </section>

        <div className="save-bar">
          {error && <p role="alert">{error}</p>}
          <label className="grow">
            <span className="field-label">سبب التغيير</span>
            <input name="reason" required minLength={3} maxLength={500} placeholder="مثال: رفع حد المنتجات" />
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            {isNew ? 'انشر الباقة' : `انشر الإصدار v${String(next)}`}
          </button>
          <p className="muted small">
            {isNew ? (
              'تظهر للتجار فور نشرها إن كانت معروضة.'
            ) : (
              <>
                {subscribers > 0
                  ? `على الباقة الآن ${stores(subscribers)}. ينتقل كلٌّ منهم إلى v${String(next)} عند تجديده القادم، والجدد يشترونه من الآن.`
                  : `لا مشتركين على الباقة الآن. الجدد يشترون v${String(next)} من الآن.`}
              </>
            )}
          </p>
        </div>
      </form>

      {!isNew && (
        <details className="card plan-fold">
          <summary>سجل الإصدارات</summary>
          <ul>
            {current.versions.map((v) => (
              <li key={v.id}>
                <bdi>v{v.version}</bdi> <span className="muted">· {stores(v.subscribers)}</span> {v.active && <span className="status store-active">على البيع الآن</span>}
              </li>
            ))}
          </ul>
          <p className="muted small">الإصدارات لا تُعدَّل ولا تُحذف.</p>
        </details>
      )}

      {current.head?.active && current.head.kind !== 'free' && (
        <details className="card plan-fold danger-zone">
          <summary>منطقة خطرة: إيقاف بيع الباقة</summary>
          <form onSubmit={(e) => void retire(e)} aria-label="إيقاف بيع الباقة">
            <p className="muted">
              تختفي من صفحة الفوترة ولا يشتريها أحد بعد الآن.{' '}
              {subscribers > 0 ? `من عليها الآن (${stores(subscribers)}) يبقون عليها حتى يغيّروها.` : 'لا مشتركين عليها الآن.'}
            </p>
            <label>
              <span className="field-label">السبب</span>
              <input name="reason" required minLength={3} maxLength={500} />
            </label>
            <button type="submit" className="danger-solid" disabled={busy}>
              أوقف بيع «{current.head.name_ar}»
            </button>
          </form>
        </details>
      )}
    </div>
  );
}
