import { useEffect, useState, type SubmitEvent } from 'react';
import { Link, useParams } from 'react-router';

import { api } from '../api/client';
import { Layout } from '../components/Layout';
import { toMajor, toMinor } from '../lib/amounts';
import { dayLabel } from '../lib/home';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { isoDay, platformLabel, type AdPlatform, type AdSpend } from '../lib/profit';
import { field } from '../session';

// monthRange is this month so far, the ad spend list's range.
function monthRange(today = new Date()): { from: string; to: string } {
  return { from: isoDay(new Date(today.getFullYear(), today.getMonth(), 1)), to: isoDay(today) };
}

const platforms = Object.keys(platformLabel) as AdPlatform[];

// ProfitSettings is the dashboard's estimates and ad spend (P8 8f, the
// approved mockup): what a delivery, a return and the carrier's cash fee
// cost until payouts tell, and the ad spend typed in by day and campaign.
export function ProfitSettings() {
  const { storeId = '' } = useParams();
  const path = { store_id: storeId };
  const [est, setEst] = useState({ delivery: '', ret: '', fee: '' });
  const [list, setList] = useState<AdSpend[]>([]);
  // Every entry of the range, listed or not (the list stops at 500).
  const [all, setAll] = useState<{ count: number; total: number } | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState(false);
  const [reloads, setReloads] = useState(0);
  const range = monthRange();

  useEffect(() => {
    let live = true;
    void api.GET('/v1/stores/{store_id}/analytics/settings', { params: { path: { store_id: storeId } } }).then(({ data, error: err }) => {
      if (!live) return;
      if (data) {
        setEst({
          delivery: data.delivery_cost_minor === undefined ? '' : toMajor(data.delivery_cost_minor),
          ret: data.return_cost_minor === undefined ? '' : toMajor(data.return_cost_minor),
          fee: data.cod_fee_bp ? String(data.cod_fee_bp / 100) : '',
        });
      }
      if (err) setError(problemMessage(err));
    });
    return () => {
      live = false;
    };
  }, [storeId]);

  useEffect(() => {
    let live = true;
    void api
      .GET('/v1/stores/{store_id}/analytics/ad-spend', { params: { path: { store_id: storeId }, query: { from: range.from, to: range.to } } })
      .then(({ data }) => {
        if (live && data) {
          setList(data.items);
          setAll(data.count === undefined || data.total_minor === undefined ? null : { count: data.count, total: data.total_minor });
        }
      });
    return () => {
      live = false;
    };
  }, [storeId, range.from, range.to, reloads]);

  async function saveEstimates(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    setError('');
    setNotice('');
    const fee = est.fee.trim() === '' ? 0 : Math.round(Number(est.fee.replace(',', '.')) * 100);
    const delivery = est.delivery.trim() === '' ? undefined : toMinor(est.delivery);
    const ret = est.ret.trim() === '' ? undefined : toMinor(est.ret);
    if (!Number.isFinite(fee) || (est.delivery.trim() !== '' && delivery === undefined) || (est.ret.trim() !== '' && ret === undefined)) {
      setError('اكتب المبالغ والنسبة أرقامًا');
      return;
    }
    setBusy(true);
    const { error: err } = await api.PUT('/v1/stores/{store_id}/analytics/settings', {
      params: { path },
      body: { cod_fee_bp: fee, ...(delivery !== undefined ? { delivery_cost_minor: delivery } : {}), ...(ret !== undefined ? { return_cost_minor: ret } : {}) },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setNotice('حُفظت التقديرات. نعيد حساب كل الأيام خلال دقائق.');
  }

  async function addSpend(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const data = new FormData(form);
    const amount = toMinor(field(data, 'amount'));
    if (amount === undefined) {
      setError('اكتب المبلغ رقمًا');
      return;
    }
    setError('');
    setNotice('');
    setBusy(true);
    const { error: err } = await api.POST('/v1/stores/{store_id}/analytics/ad-spend', {
      params: { path },
      body: {
        day: field(data, 'day'),
        platform: field(data, 'platform') as AdPlatform,
        campaign: field(data, 'campaign').trim(),
        amount_minor: amount,
        note: '',
      },
    });
    setBusy(false);
    if (err) {
      setError(problemMessage(err));
      return;
    }
    form.reset();
    setNotice('أُضيف الإنفاق.');
    setReloads((n) => n + 1);
  }

  async function remove(id: string) {
    setError('');
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/analytics/ad-spend/{ad_spend_id}', {
      params: { path: { ...path, ad_spend_id: id } },
    });
    if (err) {
      setError(problemMessage(err));
      return;
    }
    setReloads((n) => n + 1);
  }

  const currency = list[0]?.currency ?? 'EGP';
  const total = all?.total ?? list.reduce((n, s) => n + s.amount_minor, 0);
  const hidden = all ? all.count - list.length : 0;
  return (
    <Layout storeId={storeId}>
      <div className="profit">
        <nav className="crumbs" aria-label="مسار الصفحة">
          <Link to={`/stores/${storeId}/profit`}>الربح الصافي</Link> / <span>التقديرات والإنفاق الإعلاني</span>
        </nav>
        <h1>التقديرات والإنفاق الإعلاني</h1>
        {error && <p role="alert">{error}</p>}
        {notice && (
          <p role="status" className="ok-note">
            {notice}
          </p>
        )}
        <div className="profit-row">
          <form className="card profit-est" aria-labelledby="h-est" onSubmit={(e) => void saveEstimates(e)}>
            <div>
              <h2 id="h-est">تقديرات تكلفة التوصيل</h2>
              <p className="muted small">نستخدمها حتى تصل تسوية شركة الشحن لكل طرد، ثم نستبدلها بما اقتطعته فعلًا.</p>
            </div>
            <label>
              تكلفة توصيل الطرد
              <input
                name="delivery"
                inputMode="decimal"
                value={est.delivery}
                onChange={(e) => {
                  setEst({ ...est, delivery: e.target.value });
                }}
              />
              <small className="muted">اتركه فارغًا لنستخدم سعر التوصيل الذي دفعه العميل.</small>
            </label>
            <label>
              تكلفة رجوع الطرد
              <input
                name="return"
                inputMode="decimal"
                placeholder="مثل سعر التوصيل"
                value={est.ret}
                onChange={(e) => {
                  setEst({ ...est, ret: e.target.value });
                }}
              />
            </label>
            <label>
              رسوم شركة الشحن على التحصيل (%)
              <input
                name="fee"
                inputMode="decimal"
                value={est.fee}
                onChange={(e) => {
                  setEst({ ...est, fee: e.target.value });
                }}
              />
              <small className="muted">نسبة من المبلغ الذي يحصّله المندوب.</small>
            </label>
            <button type="submit" className="primary" disabled={busy}>
              حفظ التقديرات
            </button>
          </form>

          <section className="card profit-ads" aria-labelledby="h-ads">
            <div>
              <h2 id="h-ads">الإنفاق الإعلاني</h2>
              <p className="muted small">
                اكتب اسم الحملة كما في رابط الإعلان (utm_campaign) ليُطابَق مع طلباتها. الربط المباشر مع Meta وTikTok وGoogle قريبًا.
              </p>
            </div>
            <form className="ad-form" onSubmit={(e) => void addSpend(e)}>
              <label>
                اليوم
                <input name="day" type="date" required defaultValue={range.to} max={range.to} />
              </label>
              <label>
                المنصة
                <select name="platform" defaultValue="meta">
                  {platforms.map((p) => (
                    <option key={p} value={p}>
                      {platformLabel[p]}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                الحملة
                <input name="campaign" dir="ltr" maxLength={100} />
              </label>
              <label>
                المبلغ
                <input name="amount" inputMode="decimal" required />
              </label>
              <button type="submit" className="primary" disabled={busy}>
                إضافة
              </button>
            </form>
            {list.length === 0 ? (
              <p className="empty">لا إنفاق مسجّل هذا الشهر.</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">اليوم</th>
                      <th scope="col">المنصة</th>
                      <th scope="col">الحملة</th>
                      <th scope="col">المبلغ</th>
                      <th scope="col">
                        <span className="visually-hidden">حذف</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {list.map((s) => (
                      <tr key={s.id}>
                        <td>{dayLabel(s.day)}</td>
                        <td>{platformLabel[s.platform]}</td>
                        <td dir="ltr">{s.campaign || '—'}</td>
                        <td className="num">{money(s.amount_minor, s.currency)}</td>
                        <td>
                          <button
                            type="button"
                            className="danger"
                            aria-label={`حذف إنفاق ${dayLabel(s.day)} ${s.campaign}`}
                            onClick={() => void remove(s.id)}
                          >
                            حذف
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {hidden > 0 && (
              <p className="muted" role="note">
                تظهر أحدث {list.length} سطرًا فقط، و{hidden} غيرها محسوبة في الإجمالي.
              </p>
            )}
            <p className="profit-total">
              <span>الإجمالي هذا الشهر</span>
              <strong className="num">{money(total, currency)}</strong>
            </p>
          </section>
        </div>
      </div>
    </Layout>
  );
}
