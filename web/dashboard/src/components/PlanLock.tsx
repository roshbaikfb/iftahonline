import { Link } from 'react-router';

import { featureName, type Full, type Included } from '../lib/plan';
import { useRole } from '../lib/settings';

interface Props {
  storeId: string;
  feature: Included | Full;
  // full: a counted feature with no room left, rather than one the plan
  // does not include at all.
  full?: boolean;
}

// PlanLock says, before the merchant fills a form, that the store's plan
// rules it out, and how to get it: the owner chooses a plan; anyone else
// asks the owner.
export function PlanLock({ storeId, feature, full = false }: Props) {
  const role = useRole(storeId);
  const name = featureName[feature] ?? 'هذه الميزة';
  return (
    <div className="notice plan-lock" role="note">
      <span>{full ? `وصلت إلى حد باقتك في ${name}.` : `باقتك الحالية لا تشمل ${name}.`}</span>{' '}
      {role === 'owner' ? (
        <Link to={`/stores/${storeId}/billing/plans`}>{full ? 'رقِّ باقتك لتضيف المزيد' : 'رقِّ باقتك لتستخدمها'}</Link>
      ) : (
        <span>اطلب من صاحب المتجر ترقية الباقة.</span>
      )}
    </div>
  );
}
