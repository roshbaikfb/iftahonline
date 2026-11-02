import { useEffect, useState, type SubmitEvent } from 'react';
import { Link } from 'react-router';

import { api } from '../api/client';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';
import { ConsoleLayout } from './ConsoleLayout';
import { cycleLabel, memberState, percent, seatsLeft, toBps, type FoundingMember, type FoundingProgram } from './founding';
import { dateTimeAr } from './lib';

// FoundingPage runs the founding-merchants program (approved
// 2026-11-02): on or off, its seats and discount, and its members.
export function FoundingPage() {
  const [program, setProgram] = useState<FoundingProgram | null>(null);
  const [members, setMembers] = useState<FoundingMember[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    void api.GET('/admin/v1/founding').then((r) => {
      if (!live) return;
      if (r.data) {
        setProgram(r.data.program);
        setMembers(r.data.members);
      } else setError(problemMessage(r.error));
    });
    return () => {
      live = false;
    };
  }, []);

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    setBusy(true);
    setError('');
    setNotice('');
    const { data, error: err } = await api.PUT('/admin/v1/founding', {
      body: {
        enabled: form.get('enabled') === 'on',
        seats: Number(field(form, 'seats')),
        discount_bps: toBps(field(form, 'discount')),
        reason: field(form, 'reason'),
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setProgram(data);
    setNotice('حُفظت إعدادات البرنامج.');
  }

  return (
    <ConsoleLayout>
      <div>
        <h1>التجار المؤسسون</h1>
        <p className="muted">
          أول التجار الذين يدفعون اشتراكًا شهريًا أو سنويًا يحصلون على خصم مدى الحياة ويُثبَّت سعرهم ما داموا مشتركين. يُحجز المقعد عند دفع أول فاتورة، ولا يعود
          لأحد إن خرج صاحبه، ومتجر واحد لكل تاجر.
        </p>
      </div>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}

      {program && (
        <form className="card" onSubmit={(e) => void save(e)} aria-labelledby="h-program">
          <h2 id="h-program">البرنامج</h2>
          <p className="muted small">
            {program.enabled ? 'مفعّل' : 'متوقف'} · {members.length} من {program.seats} مقعد محجوز · باقي {seatsLeft(program, members)} · الخصم{' '}
            {percent(program.discount_bps)}
          </p>
          <div className="inline-form">
            <label className="check">
              <input type="checkbox" name="enabled" defaultChecked={program.enabled} /> البرنامج مفعّل
            </label>
            <label>
              <span className="field-label">عدد المقاعد</span>
              <input name="seats" type="number" required min={1} max={10000} defaultValue={program.seats} />
            </label>
            <label>
              <span className="field-label">الخصم (%)</span>
              <input name="discount" type="number" required min={1} max={90} step={0.5} defaultValue={program.discount_bps / 100} />
            </label>
            <label className="grow">
              <span className="field-label">السبب (إجباري)</span>
              <input name="reason" required minLength={3} maxLength={500} />
            </label>
            <button type="submit" className="button primary" disabled={busy}>
              حفظ
            </button>
          </div>
          <p className="muted small">تغيير الخصم يسري على من ينضم بعده فقط؛ الأعضاء يحتفظون بخصمهم.</p>
        </form>
      )}

      <section className="table-card" aria-labelledby="h-members">
        <div className="card-head">
          <h2 id="h-members">الأعضاء</h2>
        </div>
        {members.length === 0 ? (
          <p className="empty flat">لم ينضم أحد بعد.</p>
        ) : (
          <div className="scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">المقعد</th>
                  <th scope="col">المتجر</th>
                  <th scope="col">السعر المثبّت</th>
                  <th scope="col">الخصم</th>
                  <th scope="col">انضم</th>
                  <th scope="col">الحالة</th>
                </tr>
              </thead>
              <tbody>
                {members.map((m) => (
                  <tr key={m.store_id}>
                    <td>{m.seat}</td>
                    <td>
                      <Link to={`/stores/${m.store_id}`}>فتح المتجر</Link>
                    </td>
                    <td>
                      {money(m.locked_minor, m.currency)} <small className="muted">
                        ({m.plan_code} · {cycleLabel[m.cycle]})
                      </small>
                    </td>
                    <td>{percent(m.discount_bps)}</td>
                    <td className="muted">{dateTimeAr(m.joined_at)}</td>
                    <td>
                      <span className={`status ${m.lost_reason ? 'store-closed' : 'store-active'}`}>{memberState(m)}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </ConsoleLayout>
  );
}
