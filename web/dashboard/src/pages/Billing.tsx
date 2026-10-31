import { useEffect, useState } from 'react';
import { Link, useLocation, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import {
  annualSaving,
  cycleLabel,
  dateAr,
  daysLeft,
  leave,
  meterLabel,
  meterLevel,
  meterNote,
  meterPercent,
  meterText,
  monthAr,
  statusLabel,
  type Billing as BillingData,
  type BillingPlans,
  type UsageMeter,
} from '../lib/billing';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { useRole } from '../lib/settings';

// Billing is the merchant's billing page (7f, the approved mockup): the
// plan and where it stands, the card on file, this month's usage, and
// the open invoices with "pay now" (ADR-0053, ADR-0060, ADR-0061).
export function Billing() {
  const { storeId = '' } = useParams();
  const location = useLocation();
  const role = useRole(storeId);
  const [billing, setBilling] = useState<BillingData | null>(null);
  const [plans, setPlans] = useState<BillingPlans | null>(null);
  const [meters, setMeters] = useState<UsageMeter[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState((location.state as { notice?: string } | null)?.notice ?? '');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (role !== 'owner') return;
    let live = true;
    const path = { params: { path: { store_id: storeId } } };
    void Promise.all([
      api.GET('/v1/stores/{store_id}/billing', path),
      api.GET('/v1/stores/{store_id}/billing/plans', path),
      api.GET('/v1/stores/{store_id}/usage', path),
    ]).then(([b, p, u]) => {
      if (!live) return;
      if (b.data) setBilling(b.data);
      if (p.data) setPlans(p.data);
      if (u.data) setMeters(u.data.items);
      const err = b.error ?? p.error ?? u.error;
      if (err) setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, [storeId, role]);

  if (role !== undefined && role !== 'owner') {
    return (
      <Layout storeId={storeId}>
        <h1>الفوترة والباقة</h1>
        <p className="notice">الفوترة لمالك المتجر فقط.</p>
      </Layout>
    );
  }

  const path = { params: { path: { store_id: storeId } } };
  const current = plans?.plans.find((p) => p.current);
  const currency = plans?.currency ?? 'EGP';
  const paid = billing?.status === 'active' && billing.cycle !== undefined;
  const scheduled = billing?.next_plan_name_ar !== undefined || billing?.next_cycle !== undefined;

  async function run(call: () => Promise<{ data?: BillingData; error?: unknown }>, done: string) {
    setBusy(true);
    setError('');
    setNotice('');
    const { data, error: err } = await call();
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    if (data) setBilling(data);
    setNotice(done);
  }

  async function pay(invoice: string) {
    setBusy(true);
    setError('');
    const { data, error: err } = await api.POST('/v1/stores/{store_id}/billing/invoices/{invoice_id}/pay', {
      params: { path: { store_id: storeId, invoice_id: invoice } },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    leave.goTo(data.checkout_url);
  }

  async function toAnnual() {
    if (!current) return;
    setBusy(true);
    setError('');
    const { error: err } = await api.POST('/v1/stores/{store_id}/billing/subscription', { ...path, body: { plan_code: current.code, cycle: 'annual' } });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    const { data } = await api.GET('/v1/stores/{store_id}/billing', path);
    if (data) setBilling(data);
    setNotice('ستتحول للدورة السنوية عند التجديد القادم.');
  }

  function cancel() {
    if (!billing?.period_end) return;
    if (!window.confirm(`يستمر اشتراكك حتى ${dateAr(billing.period_end)} ثم ينتقل متجرك للباقة المجانية. متأكد؟`)) return;
    void run(() => api.POST('/v1/stores/{store_id}/billing/cancel', path), 'أُلغي التجديد.');
  }

  const firstOpen = billing?.open_invoices[0];
  const periodLabel = billing?.status === 'trialing' ? 'تنتهي التجربة' : billing?.status === 'past_due' ? 'آخر موعد للدفع' : 'التجديد القادم';
  const periodDate = billing?.status === 'trialing' ? billing.trial_ends_at : billing?.status === 'past_due' ? billing.grace_until : billing?.period_end;

  return (
    <Layout storeId={storeId}>
      <div className="page-head">
        <div>
          <h1>الفوترة والباقة</h1>
          <p className="muted">باقتك، واستهلاكك هذا الشهر، وفواتيرك.</p>
        </div>
        <div className="actions">
          <Link className="button" to={`/stores/${storeId}/billing/plans`}>
            قارن الباقات
          </Link>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && (
        <p role="status" className="ok-note">
          {notice}
        </p>
      )}

      {billing?.status === 'trialing' && billing.trial_ends_at && (
        <div className="bill-banner trial" role="status">
          <div>
            <strong>
              أنت تجرّب باقة «{billing.plan_name_ar}» مجانًا — باقي {daysLeft(billing.trial_ends_at)} أيام
            </strong>
            <p className="muted">اختر باقتك قبل {dateAr(billing.trial_ends_at)}، وإلا يرجع متجرك للباقة المجانية (تظهر فيه إعلانات). لا نطلب بطاقة للتجربة.</p>
          </div>
          <Link className="button primary" to={`/stores/${storeId}/billing/plans`}>
            اختر باقتك
          </Link>
        </div>
      )}
      {(billing?.status === 'past_due' || billing?.status === 'suspended') && firstOpen && (
        <div className="bill-banner late" role="alert">
          <div>
            <strong>لم ننجح في تحصيل فاتورة {monthAr(firstOpen.period)}</strong>
            <p>
              {billing.status === 'suspended'
                ? 'متجرك متوقف حتى تُدفع الفاتورة. بياناتك محفوظة.'
                : `ادفعها قبل ${billing.grace_until ? dateAr(billing.grace_until) : ''} حتى لا يتوقف متجرك.`}
            </p>
          </div>
          <button type="button" className="button success" disabled={busy} onClick={() => void pay(firstOpen.id)}>
            ادفع الآن {money(firstOpen.total_minor, firstOpen.currency)}
          </button>
        </div>
      )}

      {billing && (
        <div className="bill-grid">
          <section className="card" aria-labelledby="h-plan">
            <div className="card-head bare">
              <h2 id="h-plan">باقتك</h2>
              <span className={`status bill-${billing.status}`}>{statusLabel[billing.status]}</span>
            </div>
            <div className="bill-plan">
              <strong>{billing.plan_name_ar}</strong>
              {current?.monthly_minor !== undefined && billing.cycle && (
                <span className="muted">
                  {billing.cycle === 'annual' && current.annual_minor !== undefined
                    ? `${money(current.annual_minor, currency)} / سنويًا`
                    : `${money(current.monthly_minor, currency)} / شهريًا`}
                </span>
              )}
            </div>
            <dl className="facts">
              {billing.cycle && (
                <>
                  <dt>الدورة</dt>
                  <dd>{cycleLabel[billing.cycle]}</dd>
                </>
              )}
              {periodDate && (
                <>
                  <dt>{periodLabel}</dt>
                  <dd>{dateAr(periodDate)}</dd>
                </>
              )}
              {billing.cycle && (
                <>
                  <dt>التجديد</dt>
                  <dd>{billing.cancel_at_period_end ? 'متوقف' : billing.card ? 'تلقائي من البطاقة المحفوظة' : 'بفاتورة تدفعها هنا'}</dd>
                </>
              )}
            </dl>
            {(scheduled || billing.cancel_at_period_end) && billing.period_end && (
              <p className="notice">
                {billing.cancel_at_period_end
                  ? `ينتهي اشتراكك في ${dateAr(billing.period_end)}، ثم ينتقل متجرك لباقة «${billing.next_plan_name_ar ?? 'المجانية'}».`
                  : `من ${dateAr(billing.period_end)}: باقة «${billing.next_plan_name_ar ?? billing.plan_name_ar}»${billing.next_cycle ? `، دورة ${cycleLabel[billing.next_cycle]}` : ''}.`}{' '}
                <button
                  type="button"
                  className="link-button"
                  disabled={busy}
                  onClick={() => void run(() => api.POST('/v1/stores/{store_id}/billing/resume', path), 'تم التراجع.')}
                >
                  تراجع
                </button>
              </p>
            )}
            <div className="actions-row">
              <Link className="button primary" to={`/stores/${storeId}/billing/plans`}>
                تغيير الباقة
              </Link>
              {paid && billing.cycle === 'monthly' && !scheduled && !billing.cancel_at_period_end && current && annualSaving(current) > 0 && (
                <button type="button" className="button" disabled={busy} onClick={() => void toAnnual()}>
                  التحويل للسنوي — وفّر {annualSaving(current) === 2 ? 'شهرين' : `${String(annualSaving(current))} أشهر`}
                </button>
              )}
              {paid && !billing.cancel_at_period_end && (
                <button type="button" className="link-button danger" disabled={busy} onClick={cancel}>
                  إلغاء التجديد
                </button>
              )}
            </div>
          </section>

          <section className="card" aria-labelledby="h-card">
            <h2 id="h-card">طريقة الدفع</h2>
            {billing.card ? (
              <div className="bill-card">
                <strong>{billing.card}</strong>
                <span className="muted small">نسحب منها التجديد تلقائيًا</span>
              </div>
            ) : (
              <p className="muted">لا توجد بطاقة محفوظة. تُحفظ بطاقتك عند أول دفع على صفحة Paymob.</p>
            )}
            <p className="muted small">بيانات البطاقة عند Paymob فقط؛ لا تمر على خوادمنا. لتغييرها ادفع فاتورتك القادمة ببطاقة أخرى.</p>
            <p className="muted small">تفضّل التحويل البنكي؟ تواصل معنا وسنؤكد الدفع يدويًا.</p>
          </section>
        </div>
      )}

      {meters.length > 0 && (
        <section className="card" aria-labelledby="h-usage">
          <div className="card-head bare wrap">
            <h2 id="h-usage">استهلاكك هذا الشهر</h2>
            <span className="muted small">تتجدد العدادات الشهرية أول كل شهر. لا نوقف طلبات عملائك أبدًا؛ ننبهك عند 80% و100%.</span>
          </div>
          <div className="bill-meters">
            {meters.map((m) => (
              <div key={m.feature}>
                <div className="bill-meter-head">
                  <span>{meterLabel[m.feature]}</span>
                  <strong className={meterLevel(m) === 'ok' ? '' : 'warn'}>{meterText(m)}</strong>
                </div>
                <div
                  className={`meter ${meterLevel(m)}`}
                  role="meter"
                  aria-label={meterLabel[m.feature]}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={meterPercent(m)}
                >
                  <span style={{ inlineSize: `${String(meterPercent(m))}%` }} />
                </div>
                {meterNote[m.feature] && <p className="muted small">{meterNote[m.feature]}</p>}
              </div>
            ))}
          </div>
        </section>
      )}

      {billing && (
        <section className="table-card" aria-labelledby="h-invoices">
          <div className="card-head">
            <h2 id="h-invoices">الفواتير المفتوحة</h2>
            <span className="muted small">شاملة ضريبة القيمة المضافة</span>
          </div>
          {billing.open_invoices.length === 0 ? (
            <p className="empty flat">لا توجد فواتير مستحقة.</p>
          ) : (
            <div className="scroll">
              <table>
                <thead>
                  <tr>
                    <th scope="col">الرقم</th>
                    <th scope="col">الشهر</th>
                    <th scope="col">المبلغ</th>
                    <th scope="col">الحالة</th>
                    <th scope="col">
                      <span className="visually-hidden">إجراء</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {billing.open_invoices.map((inv) => (
                    <tr key={inv.id}>
                      <td className="ltr">{inv.number}</td>
                      <td>{monthAr(inv.period)}</td>
                      <td className="money">{money(inv.total_minor, inv.currency)}</td>
                      <td>
                        <span className="status pay-awaiting">مستحقة {dateAr(inv.due_at)}</span>
                      </td>
                      <td>
                        <button type="button" className="button success" disabled={busy} onClick={() => void pay(inv.id)}>
                          ادفع الآن
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}
    </Layout>
  );
}
