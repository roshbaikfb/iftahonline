import { useEffect, useRef, useState, type SubmitEvent } from 'react';
import { Link, useSearchParams } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { useCatalog } from './catalog';
import { byCode, facts, featureKindLabel, headline, kindLabel, otherPrices, stores, subscribersOf, type Feature } from './plans';

// PlansPage is the plan catalog (the approved mockup): one card per plan
// as merchants see them, and the features catalog in its own tab. A plan
// is edited on its own page.
export function PlansPage() {
  const { plans, features, error, setError, reload } = useCatalog();
  const [params, setParams] = useSearchParams();
  const tab = params.get('tab') === 'features' ? 'features' : 'plans';
  const groups = byCode(plans).sort((a, b) => a.head.sort - b.head.sort);
  const [adding, setAdding] = useState(false);
  const [notice, setNotice] = useState('');

  return (
    <ConsoleLayout>
      <div className="page-head">
        <div>
          <h1>الباقات والمميزات</h1>
          <p className="muted">
            {tab === 'plans'
              ? 'ما يراه التاجر في صفحة الفوترة، بالترتيب نفسه. التعديل يُنشر إصدارًا جديدًا، والمشتركون ينتقلون إليه عند تجديدهم.'
              : 'كل ما يمكن أن تمنحه باقة. الميزة الجديدة تظهر في الباقات للعرض فقط حتى يدعمها النظام.'}
          </p>
        </div>
        {tab === 'plans' ? (
          <Link className="button primary" to="/plans/new">
            باقة جديدة
          </Link>
        ) : (
          <button
            type="button"
            className="button primary"
            onClick={() => {
              setAdding(true);
            }}
          >
            ميزة جديدة
          </button>
        )}
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      <div className="tabs" role="tablist" aria-label="العرض">
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'plans'}
          onClick={() => {
            setParams({});
          }}
        >
          الباقات <span className="count">{groups.length}</span>
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'features'}
          onClick={() => {
            setParams({ tab: 'features' });
          }}
        >
          كتالوج المميزات <span className="count">{features.length}</span>
        </button>
      </div>

      {tab === 'plans' ? (
        <ul className="plan-cards">
          {groups.map(({ code, head, versions }) => {
            const h = headline(head);
            const others = otherPrices(head);
            return (
              <li key={code} className="card plan-card">
                <div className="plan-card-head">
                  <div>
                    <h2>
                      {head.name_ar} <span className="ltr muted">{head.name_en}</span>
                    </h2>
                    <span className={`status kind-${head.kind}`}>{kindLabel[head.kind]}</span>
                  </div>
                  {!head.active ? (
                    <span className="status store-closed">متوقفة عن البيع</span>
                  ) : head.public ? (
                    <span className="status store-active">معروضة</span>
                  ) : (
                    <span className="status">مخفية</span>
                  )}
                </div>
                <div className="plan-price" title={others || undefined}>
                  <strong>
                    {h.price} {h.per && <small>{h.per}</small>}
                  </strong>
                  <span className="muted">{h.sub}</span>
                </div>
                <ul className="plan-facts">
                  {facts(head, features).map((f) => (
                    <li key={f}>{f}</li>
                  ))}
                </ul>
                <div className="plan-card-foot">
                  <span className="muted small">
                    الإصدار <bdi>v{head.version}</bdi> · {stores(subscribersOf(versions))}
                  </span>
                  <Link to={`/plans/${code}`} aria-label={`تعديل ${head.name_ar}`}>
                    تعديل ←
                  </Link>
                </div>
              </li>
            );
          })}
        </ul>
      ) : (
        <section className="table-card" aria-label="كتالوج المميزات">
          <div className="scroll">
            <table className="features-table">
              <thead>
                <tr>
                  <th scope="col">الميزة</th>
                  <th scope="col">النوع</th>
                  <th scope="col">التطبيق</th>
                </tr>
              </thead>
              <tbody>
                {[...features]
                  .sort((a, b) => a.sort - b.sort)
                  .map((f) => (
                    <tr key={f.key}>
                      <td>
                        <strong>{f.name_ar}</strong>
                        <code className="ltr">{f.key}</code>
                      </td>
                      <td className="muted">{featureKindLabel[f.kind]}</td>
                      <td>
                        <span className={f.enforced ? 'status store-active' : 'status'}>{f.enforced ? 'يطبّقها النظام' : 'عرض فقط'}</span>
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {adding && (
        <FeatureDialog
          nextSort={Math.max(0, ...features.map((f) => f.sort)) + 10}
          onSaved={async (name) => {
            setAdding(false);
            setNotice(`حُفظت الميزة «${name}».`);
            await reload();
          }}
          onError={setError}
          onClose={() => {
            setAdding(false);
          }}
        />
      )}
    </ConsoleLayout>
  );
}

// FeatureDialog adds a feature to the catalog (the approved mockup's
// «ميزة جديدة»). Escape or «إلغاء» closes it.
function FeatureDialog({ nextSort, onSaved, onError, onClose }: { nextSort: number; onSaved: (name: string) => Promise<void>; onError: (m: string) => void; onClose: () => void }) {
  const box = useRef<HTMLFormElement>(null);
  const close = useRef(onClose);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    close.current = onClose;
  });
  useEffect(() => {
    const back = document.activeElement as HTMLElement | null;
    box.current?.querySelector<HTMLInputElement>('input')?.focus();
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close.current();
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      back?.focus();
    };
  }, []);

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const name = field(form, 'name_ar').trim();
    setBusy(true);
    setError('');
    onError('');
    const { error: err } = await api.PUT('/admin/v1/features/{key}', {
      params: { path: { key: field(form, 'key').trim() } },
      body: { kind: field(form, 'kind') as Feature['kind'], name_ar: name, name_en: field(form, 'name_en').trim(), sort: nextSort, reason: field(form, 'reason').trim() },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    await onSaved(name);
  }

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <form ref={box} className="modal feature-dialog" role="dialog" aria-modal="true" aria-labelledby="h-feature" onSubmit={(e) => void save(e)}>
        <h2 id="h-feature">ميزة جديدة</h2>
        <p className="muted">تظهر في الباقات للعرض فقط إلى أن يدعمها النظام. نوعها ومفتاحها لا يتغيران بعد الحفظ.</p>
        {error && <p role="alert">{error}</p>}
        <label>
          <span className="field-label">الاسم بالعربية</span>
          <input name="name_ar" required maxLength={80} />
        </label>
        <div className="two">
          <label>
            <span className="field-label">الاسم بالإنجليزية</span>
            <input name="name_en" required maxLength={80} dir="ltr" />
          </label>
          <label>
            <span className="field-label">المفتاح</span>
            <input name="key" required pattern="[a-z][a-z_]{0,31}" dir="ltr" placeholder="live_chat" />
          </label>
        </div>
        <fieldset>
          <legend className="field-label">النوع</legend>
          <label className="radio">
            <input type="radio" name="kind" value="flag" defaultChecked /> تشغيل/إيقاف <small className="muted">متاحة أو لا</small>
          </label>
          <label className="radio">
            <input type="radio" name="kind" value="count" /> عدد <small className="muted">مثل المنتجات</small>
          </label>
          <label className="radio">
            <input type="radio" name="kind" value="monthly" /> في الشهر <small className="muted">يتجدد كل شهر</small>
          </label>
        </fieldset>
        <label>
          <span className="field-label">السبب</span>
          <input name="reason" required minLength={3} maxLength={500} />
        </label>
        <div className="actions">
          <button type="submit" className="button primary" disabled={busy}>
            حفظ الميزة
          </button>
          <button type="button" className="secondary" onClick={onClose}>
            إلغاء
          </button>
        </div>
      </form>
    </div>
  );
}
