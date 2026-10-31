import { useCallback, useEffect, useState, type SubmitEvent } from 'react';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { byCode, currencies, cycles, draftFrom, featureKindLabel, kindLabel, priceKey, toInput, type Draft, type Feature, type Kind, type Plan } from './plans';

function fetchCatalog() {
  return Promise.all([api.GET('/admin/v1/plans'), api.GET('/admin/v1/features')]);
}

// PlansPage is the plan catalog (the approved mockup): the plans and their
// versions, an editor that publishes the next version (stores renew into
// it, ADR-0056), retiring a plan, and the features catalog.
export function PlansPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [features, setFeatures] = useState<Feature[]>([]);
  const [code, setCode] = useState('');
  const [draft, setDraft] = useState<Draft | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchCatalog>>) => {
    const [p, f] = r;
    if (p.data) setPlans(p.data.items);
    if (f.data) setFeatures(f.data.items);
    const err = p.error ?? f.error;
    if (err) setError(problemMessage(err));
  }, []);

  useEffect(() => {
    let live = true;
    void fetchCatalog().then((r) => {
      if (live) apply(r);
    });
    return () => {
      live = false;
    };
  }, [apply]);

  const groups = byCode(plans);
  const current = groups.find((g) => g.code === code);

  function pick(c: string) {
    const g = groups.find((x) => x.code === c);
    setCode(c);
    setIsNew(false);
    setDraft(draftFrom(g?.head));
    setNotice('');
    setError('');
  }

  function startNew() {
    setCode('');
    setIsNew(true);
    setDraft(draftFrom(undefined));
    setNotice('');
  }

  const edit = (patch: Partial<Draft>) => {
    if (draft) setDraft({ ...draft, ...patch });
  };

  async function publish(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!draft) return;
    const input = toInput(draft, field(new FormData(e.currentTarget), 'reason'));
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
    setNotice(`نُشر ${data.name_ar} v${String(data.version)}. المشتركون يأخذونه عند تجديدهم القادم.`);
    setCode(data.code);
    setIsNew(false);
    apply(await fetchCatalog());
  }

  async function retire() {
    if (!current) return;
    const reason = window.prompt(`سبب إيقاف بيع «${current.head.name_ar}»`);
    if (!reason) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/admin/v1/plans/{code}/retire', { params: { path: { code: current.code } }, body: { reason } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setNotice('أُوقف بيع الباقة. المشتركون عليها يبقون ويجددون نفس الإصدار.');
    apply(await fetchCatalog());
  }

  async function saveFeature(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const el = e.currentTarget;
    const form = new FormData(el);
    setBusy(true);
    setError('');
    const { error: err } = await api.PUT('/admin/v1/features/{key}', {
      params: { path: { key: field(form, 'key').trim() } },
      body: {
        kind: field(form, 'kind') as Feature['kind'],
        name_ar: field(form, 'name_ar'),
        name_en: field(form, 'name_en'),
        sort: Number(field(form, 'sort') || '0'),
        reason: field(form, 'reason'),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    el.reset();
    setNotice('حُفظت الميزة.');
    apply(await fetchCatalog());
  }

  const paid = draft?.kind === 'subscription' || draft?.kind === 'agency';
  return (
    <ConsoleLayout>
      <h1>الباقات والمميزات</h1>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}
      <div className="plans-console">
        <section className="card plans-list" aria-labelledby="h-plans">
          <h2 id="h-plans">الباقات</h2>
          <ul>
            {groups.map((g) => (
              <li key={g.code}>
                <button type="button" className={g.code === code ? 'plan-pick current' : 'plan-pick'} aria-pressed={g.code === code} onClick={() => { pick(g.code); }}>
                  <span>
                    <strong>{g.head.name_ar}</strong> <span className="muted small">{kindLabel[g.head.kind]}</span>
                  </span>
                  <span className="muted small ltr">
                    {g.versions.map((v) => `v${String(v.version)}${v.active ? '' : '×'}`).join(' ')}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <button type="button" className="button" onClick={startNew}>
            باقة جديدة
          </button>
        </section>

        {draft && (
          <form className="card plan-editor" onSubmit={(e) => void publish(e)} aria-label="إصدار جديد">
            <h2>{isNew ? 'باقة جديدة' : `${current?.head.name_ar ?? ''} — إصدار جديد (v${String((current?.versions[0]?.version ?? 0) + 1)})`}</h2>
            {!isNew && <p className="muted small">لن يتغير شيء على المشتركين قبل تجديدهم القادم. الإصدارات لا تُعدَّل ولا تُحذف.</p>}
            <div className="editor-fields">
              {isNew && (
                <>
                  <label>
                    <span className="field-label">الكود</span>
                    <input value={draft.code} onChange={(e) => { edit({ code: e.target.value }); }} required pattern="[a-z][a-z_]{1,31}" dir="ltr" />
                  </label>
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
              <label>
                <span className="field-label">أيام التجربة</span>
                <input value={draft.trial_days} onChange={(e) => { edit({ trial_days: e.target.value }); }} inputMode="numeric" />
              </label>
              <label>
                <span className="field-label">الترتيب</span>
                <input value={draft.sort} onChange={(e) => { edit({ sort: e.target.value }); }} inputMode="numeric" />
              </label>
              <label className="check">
                <input type="checkbox" checked={draft.public} onChange={(e) => { edit({ public: e.target.checked }); }} /> معروضة للتجار
              </label>
            </div>

            {paid && (
              <fieldset>
                <legend>الأسعار</legend>
                <table className="compact">
                  <thead>
                    <tr>
                      <th scope="col">العملة</th>
                      <th scope="col">شهري</th>
                      <th scope="col">سنوي</th>
                    </tr>
                  </thead>
                  <tbody>
                    {currencies.map((c) => (
                      <tr key={c}>
                        <th scope="row">{c}</th>
                        {cycles.map((cy) => (
                          <td key={cy}>
                            <input
                              aria-label={`${c} ${cy === 'monthly' ? 'شهري' : 'سنوي'}`}
                              value={draft.prices[priceKey(c, cy)] ?? ''}
                              onChange={(e) => { edit({ prices: { ...draft.prices, [priceKey(c, cy)]: e.target.value } }); }}
                              inputMode="decimal"
                              dir="ltr"
                            />
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </fieldset>
            )}
            {draft.kind === 'pays' && (
              <fieldset>
                <legend>رسوم الطلبات المسلّمة</legend>
                <table className="compact">
                  <thead>
                    <tr>
                      <th scope="col">العملة</th>
                      <th scope="col">النسبة %</th>
                      <th scope="col">أقل رسوم</th>
                      <th scope="col">أقصى رسوم</th>
                    </tr>
                  </thead>
                  <tbody>
                    {currencies.map((c) => {
                      const f = draft.fees[c] ?? { rate: '', min: '', max: '' };
                      const set = (patch: Partial<typeof f>) => {
                        edit({ fees: { ...draft.fees, [c]: { ...f, ...patch } } });
                      };
                      return (
                        <tr key={c}>
                          <th scope="row">{c}</th>
                          <td>
                            <input aria-label={`${c} النسبة`} value={f.rate} onChange={(e) => { set({ rate: e.target.value }); }} inputMode="decimal" dir="ltr" />
                          </td>
                          <td>
                            <input aria-label={`${c} أقل رسوم`} value={f.min} onChange={(e) => { set({ min: e.target.value }); }} inputMode="decimal" dir="ltr" />
                          </td>
                          <td>
                            <input aria-label={`${c} أقصى رسوم`} value={f.max} onChange={(e) => { set({ max: e.target.value }); }} inputMode="decimal" dir="ltr" />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </fieldset>
            )}

            <fieldset>
              <legend>ما تمنحه الباقة</legend>
              <p className="muted small">اترك القيمة فارغة لميزة لا تمنحها الباقة. التشغيل/الإيقاف: 1 أو 0.</p>
              <table className="compact">
                <thead>
                  <tr>
                    <th scope="col">الميزة</th>
                    <th scope="col">النوع</th>
                    <th scope="col">القيمة</th>
                    <th scope="col">بلا حد</th>
                  </tr>
                </thead>
                <tbody>
                  {features.map((f) => {
                    const g = draft.grants[f.key] ?? { value: '', unlimited: false };
                    const set = (patch: Partial<typeof g>) => {
                      edit({ grants: { ...draft.grants, [f.key]: { ...g, ...patch } } });
                    };
                    return (
                      <tr key={f.key}>
                        <th scope="row">
                          {f.name_ar} {!f.enforced && <span className="status">عرض فقط</span>}
                        </th>
                        <td className="muted">{featureKindLabel[f.kind]}</td>
                        <td>
                          <input aria-label={f.name_ar} value={g.value} disabled={g.unlimited} onChange={(e) => { set({ value: e.target.value }); }} inputMode="numeric" dir="ltr" />
                        </td>
                        <td>
                          {f.kind !== 'flag' && <input type="checkbox" aria-label={`${f.name_ar} بلا حد`} checked={g.unlimited} onChange={(e) => { set({ unlimited: e.target.checked }); }} />}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </fieldset>

            <div className="inline-form">
              <label className="grow">
                <span className="field-label">سبب التغيير (إجباري)</span>
                <input name="reason" required minLength={3} maxLength={500} />
              </label>
              <button type="submit" className="button primary" disabled={busy}>
                {isNew ? 'انشر الباقة' : 'انشر الإصدار'}
              </button>
              {!isNew && current?.head.active && current.head.kind !== 'free' && (
                <button type="button" className="link-button danger" disabled={busy} onClick={() => void retire()}>
                  إيقاف بيع الباقة
                </button>
              )}
            </div>
          </form>
        )}
      </div>

      <section className="table-card" aria-labelledby="h-catalog">
        <div className="card-head wrap">
          <h2 id="h-catalog">كتالوج المميزات</h2>
          <span className="muted small">الميزة الجديدة تظهر على الباقات للعرض، ولا يطبّقها النظام حتى يدعمها الكود. نوع الميزة لا يتغير بعد إنشائها.</span>
        </div>
        <div className="scroll">
          <table>
            <thead>
              <tr>
                <th scope="col">المفتاح</th>
                <th scope="col">الاسم</th>
                <th scope="col">النوع</th>
                <th scope="col">التطبيق</th>
              </tr>
            </thead>
            <tbody>
              {features.map((f) => (
                <tr key={f.key}>
                  <td className="ltr">{f.key}</td>
                  <td>{f.name_ar}</td>
                  <td className="muted">{featureKindLabel[f.kind]}</td>
                  <td>
                    <span className={f.enforced ? 'status store-active' : 'status'}>{f.enforced ? 'يطبّقه النظام' : 'عرض فقط'}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <form className="inline-form foot" onSubmit={(e) => void saveFeature(e)} aria-label="ميزة جديدة">
          <label>
            <span className="field-label">المفتاح</span>
            <input name="key" required pattern="[a-z][a-z_]{0,31}" dir="ltr" placeholder="live_chat" />
          </label>
          <label>
            <span className="field-label">الاسم بالعربية</span>
            <input name="name_ar" required maxLength={80} />
          </label>
          <label>
            <span className="field-label">الاسم بالإنجليزية</span>
            <input name="name_en" required maxLength={80} dir="ltr" />
          </label>
          <label>
            <span className="field-label">النوع</span>
            <select name="kind" defaultValue="flag">
              <option value="flag">تشغيل/إيقاف</option>
              <option value="count">عدد</option>
              <option value="monthly">شهري</option>
            </select>
          </label>
          <label>
            <span className="field-label">الترتيب</span>
            <input name="sort" inputMode="numeric" defaultValue="0" />
          </label>
          <label className="grow">
            <span className="field-label">السبب</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <button type="submit" className="button primary" disabled={busy}>
            حفظ الميزة
          </button>
        </form>
      </section>
    </ConsoleLayout>
  );
}
