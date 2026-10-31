import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { annualSaving, grantText, leave, planPoints, planPrice, sold, type BillingPlan, type BillingPlans, type Cycle } from '../lib/billing';
import { problemMessage } from '../lib/problem';
import { useRole } from '../lib/settings';

// done are the notices the billing page shows after a choice.
const done = {
  apply: 'تم تغيير باقتك.',
  schedule: 'سيتغير اشتراكك عند نهاية الفترة الحالية.',
  trial: 'بدأت تجربتك المجانية.',
};

// Plans is the plan picker (7f, the approved mockup): monthly or annual,
// a card per plan on sale, and the comparison (ADR-0060).
export function Plans() {
  const { storeId = '' } = useParams();
  const navigate = useNavigate();
  const role = useRole(storeId);
  const [data, setData] = useState<BillingPlans | null>(null);
  const [cycle, setCycle] = useState<Cycle>('monthly');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');

  useEffect(() => {
    if (role !== 'owner') return;
    let live = true;
    void api
      .GET('/v1/stores/{store_id}/billing/plans', {
        params: { path: { store_id: storeId } },
      })
      .then(({ data: d, error: err }) => {
        if (!live) return;
        if (d) setData(d);
        if (err) setError(problemMessage(err));
      });
    return () => {
      live = false;
    };
  }, [storeId, role]);

  if (role !== undefined && role !== 'owner') {
    return (
      <Layout storeId={storeId}>
        <h1>الباقات</h1>
        <p className="notice">اختيار الباقة لمالك المتجر فقط.</p>
      </Layout>
    );
  }

  const back = `/stores/${storeId}/billing`;
  const path = { params: { path: { store_id: storeId } } };

  async function choose(p: BillingPlan) {
    setBusy(p.code);
    setError('');
    const paid = p.kind === 'subscription';
    const { data: out, error: err } = await api.POST('/v1/stores/{store_id}/billing/subscription', {
      ...path,
      body: paid ? { plan_code: p.code, cycle } : { plan_code: p.code },
    });
    setBusy('');
    if (err) {
      setError(problemMessage(err));
      return;
    }
    if (out.action === 'pay' && out.checkout_url) {
      leave.goTo(out.checkout_url);
      return;
    }
    void navigate(back, {
      state: { notice: out.action === 'schedule' ? done.schedule : done.apply },
    });
  }

  async function trial(p: BillingPlan) {
    setBusy(p.code);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/billing/trial', { ...path, body: { plan_code: p.code } });
    setBusy('');
    if (err) {
      setError(problemMessage(err));
      return;
    }
    void navigate(back, { state: { notice: done.trial } });
  }

  const plans = data?.plans ?? [];
  const saving = Math.max(0, ...plans.map(annualSaving));
  const currency = data?.currency ?? 'EGP';

  return (
    <Layout storeId={storeId}>
      <Link to={back}>→ الفوترة والباقة</Link>
      <div className="plans-head">
        <h1>اختر الباقة المناسبة لمتجرك</h1>
        <p className="muted">كل الباقات المدفوعة فيها تجربة مجانية مرة واحدة لكل متجر، بدون بطاقة. تقدر تغيّر باقتك في أي وقت.</p>
        <div className="segmented narrow-seg" role="group" aria-label="دورة الدفع">
          <button
            type="button"
            aria-pressed={cycle === 'monthly'}
            onClick={() => {
              setCycle('monthly');
            }}
          >
            شهري
          </button>
          <button
            type="button"
            aria-pressed={cycle === 'annual'}
            onClick={() => {
              setCycle('annual');
            }}
          >
            سنوي
            {saving > 0 ? ` — ${saving === 2 ? 'شهران' : `${String(saving)} أشهر`} مجانًا` : ''}
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}

      <div className="plan-cards">
        {plans.map((p) => {
          const { price, note } = planPrice(p, cycle, currency);
          const subscribe = p.kind === 'subscription' ? `الاشتراك في ${p.name_ar}` : `اختيار ${p.name_ar}`;
          return (
            <section key={p.code} className={`card plan-card${p.current ? ' current' : ''}`} aria-labelledby={`plan-${p.code}`}>
              {p.current && <span className="plan-badge">باقتك الحالية</span>}
              <h2 id={`plan-${p.code}`}>{p.name_ar}</h2>
              <div className="plan-price">
                <strong>{price}</strong>
                <span className="muted small">{note}</span>
              </div>
              <ul className="plan-points">
                {planPoints(p, data?.features ?? []).map((pt) => (
                  <li key={pt}>{pt}</li>
                ))}
              </ul>
              <div className="plan-actions">
                {p.trial_available && (
                  <button type="button" className="button primary" disabled={busy !== ''} onClick={() => void trial(p)}>
                    جرّب {p.trial_days} يومًا مجانًا
                  </button>
                )}
                <button
                  type="button"
                  className={p.trial_available ? 'button' : 'button primary'}
                  disabled={busy !== '' || (p.current && p.kind !== 'subscription') || !sold(p, cycle)}
                  onClick={() => void choose(p)}
                >
                  {p.current && p.kind !== 'subscription' ? 'باقتك الحالية' : subscribe}
                </button>
              </div>
            </section>
          );
        })}
      </div>

      {data && (
        <section className="table-card" aria-labelledby="h-compare">
          <div className="card-head">
            <h2 id="h-compare">مقارنة الباقات</h2>
          </div>
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">الميزة</th>
                  {plans.map((p) => (
                    <th key={p.code} scope="col">
                      {p.name_ar}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {data.features.map((f) => (
                  <tr key={f.key}>
                    <th scope="row">{f.name_ar}</th>
                    {plans.map((p) => (
                      <td key={p.code}>{grantText(p, f)}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
      <p className="muted small center">الأسعار قبل ضريبة القيمة المضافة. وكالة تدير أكثر من متجر؟ تواصل معنا لباقة الوكالات: فاتورة واحدة لكل متاجرك.</p>
    </Layout>
  );
}
