import { useEffect, useState } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { problemMessage } from '../lib/problem';
import { ruleLabel, type RiskRule, type RiskSettings } from '../lib/risk';

const rules: RiskRule[] = ['off', 'medium', 'high'];

// RiskRulesCard is the store's cash-on-delivery protection (ADR-0066):
// from which customer band a new order is held for review, and from
// which one the customer's own "yes" must be verified by phone. Plans
// without the rules show them, disabled, with the way to get them.
export function RiskRulesCard({ storeId, manage }: { storeId: string; manage: boolean }) {
  const [s, setS] = useState<RiskSettings | null>(null);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/risk/settings', { params: { path: { store_id: storeId } } }).then(({ data }) => {
      if (live && data) setS(data);
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  async function save(next: Pick<RiskSettings, 'hold_from' | 'verify_from'>) {
    setError('');
    setSaved(false);
    const { data, error: err } = await api.PUT('/v1/stores/{store_id}/risk/settings', { params: { path: { store_id: storeId } }, body: next });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setS(data);
    setSaved(true);
  }

  if (!s) return null;
  const locked = !manage || !s.available;
  return (
    <section className="card editor-card" aria-labelledby="h-risk-rules">
      <div>
        <h2 id="h-risk-rules">حماية الدفع عند الاستلام</h2>
        <p className="muted small">نقيّم كل عميل من نتائج توصيل رقمه في كل المتاجر، ونطبّق قواعدك على طلباته الجديدة. العميل الجديد لا يُحجز ولا يُطلب منه شيء.</p>
      </div>
      {!s.available && (
        <p className="notice" role="note">
          متاحة في الباقات المدفوعة. <Link to={`/stores/${storeId}/billing/plans`}>اعرض الباقات</Link>
        </p>
      )}
      <label className="field-label" htmlFor="rule-hold">
        احجز الطلب للمراجعة
      </label>
      <select
        id="rule-hold"
        value={s.hold_from}
        disabled={locked}
        onChange={(e) => void save({ hold_from: e.target.value as RiskRule, verify_from: s.verify_from })}
      >
        {rules.map((r) => (
          <option key={r} value={r}>
            {ruleLabel[r]}
          </option>
        ))}
      </select>
      <small className="muted">لا نرسل للعميل رسالة تأكيد، ولا يُؤكَّد الطلب حتى تراجعه وتفك حجزه.</small>
      <label className="field-label" htmlFor="rule-verify">
        اطلب تأكيدًا هاتفيًا
      </label>
      <select
        id="rule-verify"
        value={s.verify_from}
        disabled={locked}
        onChange={(e) => void save({ hold_from: s.hold_from, verify_from: e.target.value as RiskRule })}
      >
        {rules.map((r) => (
          <option key={r} value={r}>
            {ruleLabel[r]}
          </option>
        ))}
      </select>
      <small className="muted">نرسل رسالة التأكيد حتى لو أوقفتها، لكن موافقة العميل لا تؤكد الطلب: اتصل به أولًا.</small>
      {saved && (
        <p role="status" className="ok-note">
          حُفظت القواعد.
        </p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
