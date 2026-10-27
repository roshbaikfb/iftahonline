import type { ReactNode } from 'react';

import { wizardSteps } from '../lib/onboarding';

// WizardFrame is the setup wizard's page (the approved mockup): the
// brand, the step count, the six-step bar and the step's card. step is
// 0-based.
export function WizardFrame({ step, children }: { step: number; children: ReactNode }) {
  return (
    <div className="wiz">
      <header className="wiz-top">
        <div className="wiz-brand">
          <span className="mark" aria-hidden="true">
            ا
          </span>
          <strong>افتح أونلاين</strong>
        </div>
        <span className="wiz-count">
          الخطوة {step + 1} من {wizardSteps.length}
        </span>
      </header>
      <nav aria-label="خطوات الإعداد">
        <ol className="wiz-steps">
          {wizardSteps.map((label, i) => (
            <li key={label} aria-current={i === step ? 'step' : undefined} className={i < step ? 'done' : i === step ? 'now' : undefined}>
              <span className="bar" />
              <span className="label">{label}</span>
            </li>
          ))}
        </ol>
      </nav>
      <main className="wiz-main">
        <section className="wiz-card">{children}</section>
      </main>
    </div>
  );
}
