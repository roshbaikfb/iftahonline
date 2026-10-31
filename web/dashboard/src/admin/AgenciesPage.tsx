import { useCallback, useEffect, useState, type SubmitEvent } from 'react';
import { Link, useNavigate } from 'react-router';

import { api, type components } from '../api/client';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { dateTimeAr } from './lib';

type Agency = components['schemas']['AdminAgency'];
type Plan = components['schemas']['AdminPlan'];

function fetchAgencies() {
  return Promise.all([api.GET('/admin/v1/agencies'), api.GET('/admin/v1/plans')]);
}

// AgenciesPage lists the agencies and opens new ones (ADR-0059): many
// stores on one agency plan, one period and one invoice, in one currency.
export function AgenciesPage() {
  const navigate = useNavigate();
  const [agencies, setAgencies] = useState<Agency[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const apply = useCallback((r: Awaited<ReturnType<typeof fetchAgencies>>) => {
    const [a, p] = r;
    if (a.data) setAgencies(a.data.items);
    if (p.data) setPlans(p.data.items.filter((x) => x.kind === 'agency' && x.active));
    const err = a.error ?? p.error;
    if (err) setError(problemMessage(err));
  }, []);

  useEffect(() => {
    let live = true;
    void fetchAgencies().then((r) => {
      if (live) apply(r);
    });
    return () => {
      live = false;
    };
  }, [apply]);

  async function create(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/admin/v1/agencies', {
      body: {
        name: field(form, 'name'),
        currency: field(form, 'currency') as Agency['currency'],
        plan_code: field(form, 'plan_code'),
        reason: field(form, 'reason'),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    void navigate(`/agencies/${data.agency.id}`);
  }

  return (
    <ConsoleLayout>
      <h1>الوكالات</h1>
      {error && <p role="alert">{error}</p>}
      <section className="table-card" aria-labelledby="h-agencies">
        <div className="card-head">
          <h2 id="h-agencies">كل الوكالات</h2>
        </div>
        {agencies.length === 0 ? (
          <p className="empty flat">لا توجد وكالات بعد.</p>
        ) : (
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">الوكالة</th>
                  <th scope="col">العملة</th>
                  <th scope="col">الباقة</th>
                  <th scope="col">المتاجر</th>
                  <th scope="col">أنشئت</th>
                </tr>
              </thead>
              <tbody>
                {agencies.map((a) => (
                  <tr key={a.id}>
                    <td>
                      <Link to={`/agencies/${a.id}`}>{a.name}</Link>
                    </td>
                    <td>{a.currency}</td>
                    <td className="ltr">{a.plan_code}</td>
                    <td>{a.stores}</td>
                    <td className="muted">{dateTimeAr(a.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <form className="card" onSubmit={(e) => void create(e)} aria-label="وكالة جديدة">
        <h2>وكالة جديدة</h2>
        <div className="inline-form">
          <label className="grow">
            <span className="field-label">الاسم</span>
            <input name="name" required maxLength={120} />
          </label>
          <label>
            <span className="field-label">العملة</span>
            <select name="currency" defaultValue="EGP">
              <option value="EGP">EGP</option>
              <option value="SAR">SAR</option>
              <option value="AED">AED</option>
            </select>
          </label>
          <label>
            <span className="field-label">باقة الوكالة</span>
            <select name="plan_code" required>
              {plans.map((p) => (
                <option key={p.code} value={p.code}>
                  {p.name_ar}
                </option>
              ))}
            </select>
          </label>
          <label className="grow">
            <span className="field-label">السبب</span>
            <input name="reason" required minLength={3} maxLength={500} />
          </label>
          <button type="submit" className="button primary" disabled={busy || plans.length === 0}>
            إنشاء الوكالة
          </button>
        </div>
        {plans.length === 0 && <p className="warn-note">لا توجد باقة وكالة معروضة للبيع.</p>}
      </form>
    </ConsoleLayout>
  );
}
