import { useEffect, useState, type SubmitEvent } from 'react';
import { useParams } from 'react-router';

import { api, type components } from '../api/client';
import { Layout } from '../components/Layout';
import { bpToPercent, percentToBp, toMajor, toMinor } from '../lib/amounts';
import { money } from '../lib/orders';
import { problemMessage } from '../lib/problem';
import { field } from '../session';

type Config = components['schemas']['ShippingConfig'];
type Zone = components['schemas']['ShippingZone'];
type ZoneInput = components['schemas']['ShippingZoneInput'];
type CODFee = components['schemas']['CODFee'];

const noFee: CODFee = { fixed_minor: 0, bp: 0, min_minor: 0, max_minor: 0 };

async function fetchConfig(storeId: string): Promise<{ cfg?: Config; error?: string }> {
  try {
    const { data, error } = await api.GET('/v1/stores/{store_id}/shipping', {
      params: { path: { store_id: storeId } },
    });
    return error ? { error: problemMessage(error) } : { cfg: data };
  } catch (err) {
    return { error: problemMessage(err) };
  }
}

// readFee reads the COD fee fields named prefix-fixed, -pct, -min, -max;
// a string is the first field that is not an amount.
function readFee(form: FormData, prefix: string): CODFee | string {
  const fee = { ...noFee };
  for (const [name, key] of [
    ['fixed', 'fixed_minor'],
    ['min', 'min_minor'],
    ['max', 'max_minor'],
  ] as const) {
    const text = field(form, `${prefix}-${name}`);
    const v = text.trim() === '' ? 0 : toMinor(text);
    if (v === undefined) return `${feeLabels[name]}: اكتب مبلغًا صحيحًا`;
    fee[key] = v;
  }
  const pct = field(form, `${prefix}-pct`);
  const bp = pct.trim() === '' ? 0 : percentToBp(pct);
  if (bp === undefined || bp > 1000) return `${feeLabels.pct}: من 0 إلى 10%`;
  fee.bp = bp;
  return fee;
}

const feeLabels = {
  fixed: 'مبلغ ثابت',
  pct: 'نسبة من الطلب %',
  min: 'حد أدنى',
  max: 'حد أقصى (0 = بلا حد)',
} as const;

// optionalAmount reads an amount that may be left empty.
function optionalAmount(text: string): number | undefined | 'bad' {
  if (text.trim() === '') return undefined;
  return toMinor(text) ?? 'bad';
}

function FeeFields({ prefix, fee }: { prefix: string; fee: CODFee }) {
  return (
    <div className="fee">
      <label>
        {feeLabels.fixed}
        <input name={`${prefix}-fixed`} inputMode="decimal" defaultValue={toMajor(fee.fixed_minor)} />
      </label>
      <label>
        {feeLabels.pct}
        <input name={`${prefix}-pct`} inputMode="decimal" defaultValue={bpToPercent(fee.bp)} />
      </label>
      <label>
        {feeLabels.min}
        <input name={`${prefix}-min`} inputMode="decimal" defaultValue={toMajor(fee.min_minor)} />
      </label>
      <label>
        {feeLabels.max}
        <input name={`${prefix}-max`} inputMode="decimal" defaultValue={toMajor(fee.max_minor)} />
      </label>
    </div>
  );
}

// Shipping is where the merchant sets delivery: the store's COD fee and
// free-shipping threshold, and zones of governorates with their rates,
// city rates and their own fee or threshold (ADR-0025). Saves send the
// version read, so someone else's change in between is refused.
export function Shipping() {
  const { storeId = '' } = useParams();
  const [cfg, setCfg] = useState<Config | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [editing, setEditing] = useState<Zone | 'new' | null>(null);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    let live = true;
    void fetchConfig(storeId).then((r) => {
      if (!live) return;
      if (r.cfg) setCfg(r.cfg);
      setError(r.error ?? '');
    });
    return () => {
      live = false;
    };
  }, [storeId, reloads]);

  function done(msg: string) {
    setNotice(msg);
    setError('');
    setEditing(null);
    setReloads((n) => n + 1);
  }

  async function saveSettings(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!cfg) return;
    const form = new FormData(e.currentTarget);
    const fee = readFee(form, 'store');
    const free = optionalAmount(field(form, 'free_over'));
    if (typeof fee === 'string' || free === 'bad') {
      setError(typeof fee === 'string' ? fee : 'الشحن المجاني: اكتب مبلغًا صحيحًا');
      return;
    }
    const { error: err } = await api.PUT('/v1/stores/{store_id}/shipping/settings', {
      params: { path: { store_id: storeId } },
      body: {
        cod_fee: fee,
        version: cfg.settings.version,
        ...(free === undefined ? {} : { free_over_minor: free }),
      },
    });
    if (err) setError(problemMessage(err));
    else done('حُفظت الإعدادات');
  }

  async function removeZone(z: Zone) {
    if (!globalThis.confirm(`حذف منطقة «${z.name}»؟ لن تُوصَّل طلبات محافظاتها بعد ذلك إلا إذا أضفتها لمنطقة أخرى.`)) return;
    const { error: err } = await api.DELETE('/v1/stores/{store_id}/shipping/zones/{zone_id}', {
      params: { path: { store_id: storeId, zone_id: z.id } },
    });
    if (err) setError(problemMessage(err));
    else done('حُذفت المنطقة');
  }

  const names = new Map(cfg?.areas.map((a) => [a.code, a.name_ar]));
  return (
    <Layout storeId={storeId}>
      <h1>الشحن</h1>
      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {cfg && (
        <>
          <section>
            <h2>الإعدادات العامة</h2>
            <form className="panel" onSubmit={(e) => void saveSettings(e)} key={`s-${String(cfg.settings.version)}`}>
              <label>
                شحن مجاني للطلبات من (اتركه فارغًا لعدم وجوده)
                <input
                  name="free_over"
                  inputMode="decimal"
                  defaultValue={cfg.settings.free_over_minor === undefined ? '' : toMajor(cfg.settings.free_over_minor)}
                />
              </label>
              <fieldset>
                <legend>رسوم الدفع عند الاستلام</legend>
                <FeeFields prefix="store" fee={cfg.settings.cod_fee} />
              </fieldset>
              <button type="submit">حفظ الإعدادات</button>
            </form>
          </section>
          <section>
            <h2>مناطق التوصيل</h2>
            {cfg.zones.length === 0 && <p className="empty">لا توجد مناطق: المتجر يوصّل لكل المحافظات مجانًا. أضف منطقة لتحديد الأسعار.</p>}
            <ul className="zones">
              {cfg.zones.map((z) => (
                <li key={z.id}>
                  <strong>{z.name}</strong> — {money(z.rate_minor, cfg.currency)}
                  {z.cities.length > 0 && <small> · {z.cities.length} مدينة بسعر خاص</small>}
                  <div className="areas">{z.areas.map((a) => names.get(a) ?? a).join('، ')}</div>
                  <div className="actions">
                    <button
                      type="button"
                      onClick={() => {
                        setEditing(z);
                      }}
                    >
                      تعديل
                    </button>
                    <button type="button" className="danger" onClick={() => void removeZone(z)}>
                      حذف
                    </button>
                  </div>
                </li>
              ))}
            </ul>
            {editing === null && (
              <button
                type="button"
                onClick={() => {
                  setEditing('new');
                }}
              >
                منطقة جديدة
              </button>
            )}
            {editing !== null && (
              <ZoneEditor
                key={editing === 'new' ? 'new' : editing.id}
                storeId={storeId}
                cfg={cfg}
                zone={editing === 'new' ? null : editing}
                onSaved={() => {
                  done(editing === 'new' ? 'أُضيفت المنطقة' : 'حُفظت المنطقة');
                }}
                onCancel={() => {
                  setEditing(null);
                }}
              />
            )}
          </section>
        </>
      )}
    </Layout>
  );
}

interface City {
  name: string;
  rate: string;
}

function ZoneEditor({
  storeId,
  cfg,
  zone,
  onSaved,
  onCancel,
}: {
  storeId: string;
  cfg: Config;
  zone: Zone | null;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const [cities, setCities] = useState<City[]>(zone?.cities.map((c) => ({ name: c.name, rate: toMajor(c.rate_minor) })) ?? []);
  const [ownFee, setOwnFee] = useState(zone?.cod_fee !== undefined);
  const [error, setError] = useState('');
  const taken = new Map<string, string>();
  for (const z of cfg.zones) {
    if (z.id !== zone?.id) for (const a of z.areas) taken.set(a, z.name);
  }

  async function save(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const rate = toMinor(field(form, 'rate'));
    const free = optionalAmount(field(form, 'zone_free_over'));
    const areas = form.getAll('areas').filter((v): v is string => typeof v === 'string');
    if (rate === undefined) {
      setError('سعر الشحن: اكتب مبلغًا صحيحًا');
      return;
    }
    if (free === 'bad') {
      setError('الشحن المجاني: اكتب مبلغًا صحيحًا');
      return;
    }
    if (areas.length === 0) {
      setError('اختر محافظة واحدة على الأقل');
      return;
    }
    const body: ZoneInput = {
      name: field(form, 'name').trim(),
      areas,
      rate_minor: rate,
      cities: [],
    };
    for (const c of cities) {
      if (c.name.trim() === '') continue;
      const r = toMinor(c.rate);
      if (r === undefined) {
        setError(`سعر «${c.name}»: اكتب مبلغًا صحيحًا`);
        return;
      }
      body.cities?.push({ name: c.name.trim(), rate_minor: r });
    }
    if (free !== undefined) body.free_over_minor = free;
    if (ownFee) {
      const fee = readFee(form, 'zone');
      if (typeof fee === 'string') {
        setError(fee);
        return;
      }
      body.cod_fee = fee;
    }
    const { error: err } = zone
      ? await api.PUT('/v1/stores/{store_id}/shipping/zones/{zone_id}', {
          params: { path: { store_id: storeId, zone_id: zone.id } },
          body: { ...body, version: zone.version },
        })
      : await api.POST('/v1/stores/{store_id}/shipping/zones', {
          params: { path: { store_id: storeId } },
          body,
        });
    if (err) setError(problemMessage(err));
    else onSaved();
  }

  return (
    <form className="panel zone" onSubmit={(e) => void save(e)}>
      <h3>{zone ? `تعديل «${zone.name}»` : 'منطقة جديدة'}</h3>
      {error && <p role="alert">{error}</p>}
      <label>
        اسم المنطقة
        <input name="name" required maxLength={60} defaultValue={zone?.name ?? ''} />
      </label>
      <label>
        سعر الشحن
        <input name="rate" required inputMode="decimal" defaultValue={zone ? toMajor(zone.rate_minor) : ''} />
      </label>
      <fieldset>
        <legend>المحافظات</legend>
        <div className="checks">
          {cfg.areas.map((a) => (
            <label key={a.code} title={taken.has(a.code) ? `في منطقة «${taken.get(a.code) ?? ''}»` : undefined}>
              <input
                type="checkbox"
                name="areas"
                value={a.code}
                defaultChecked={zone?.areas.includes(a.code)}
                disabled={taken.has(a.code)}
              />
              {a.name_ar}
            </label>
          ))}
        </div>
      </fieldset>
      <fieldset>
        <legend>مدن بسعر خاص (اختياري)</legend>
        {cities.map((c, i) => (
          <div className="city" key={i}>
            <input
              aria-label="المدينة"
              placeholder="المدينة"
              value={c.name}
              onChange={(e) => {
                setCities(cities.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)));
              }}
            />
            <input
              aria-label="سعرها"
              placeholder="السعر"
              inputMode="decimal"
              value={c.rate}
              onChange={(e) => {
                setCities(cities.map((x, j) => (j === i ? { ...x, rate: e.target.value } : x)));
              }}
            />
            <button
              type="button"
              className="link"
              onClick={() => {
                setCities(cities.filter((_, j) => j !== i));
              }}
            >
              إزالة
            </button>
          </div>
        ))}
        <button
          type="button"
          className="link"
          onClick={() => {
            setCities([...cities, { name: '', rate: '' }]);
          }}
        >
          + مدينة
        </button>
      </fieldset>
      <label>
        شحن مجاني من (يتجاوز الإعداد العام؛ اختياري)
        <input
          name="zone_free_over"
          inputMode="decimal"
          defaultValue={zone?.free_over_minor === undefined ? '' : toMajor(zone.free_over_minor)}
        />
      </label>
      <label className="inline">
        <input
          type="checkbox"
          checked={ownFee}
          onChange={(e) => {
            setOwnFee(e.target.checked);
          }}
        />
        رسوم دفع عند الاستلام خاصة بهذه المنطقة
      </label>
      {ownFee && <FeeFields prefix="zone" fee={zone?.cod_fee ?? noFee} />}
      <div className="actions">
        <button type="submit">حفظ المنطقة</button>
        <button type="button" className="link" onClick={onCancel}>
          إلغاء
        </button>
      </div>
    </form>
  );
}
