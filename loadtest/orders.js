// k6: shoppers placing cash-on-delivery orders through the one-step form
// (P4 acceptance: 50 orders/s, p95 < 150 ms, 0 errors).
//
// Run: make loadtest-orders. It starts the demo store in load mode
// (sfdemo -load: real PostgreSQL and Redis, the production storefront
// code and middleware) and runs this script against it.
//
// Every order is a different shopper: its own phone and its own address
// in X-Forwarded-For, which the demo trusts from loopback the way
// production trusts the edge. Nothing in the storefront is relaxed for
// the test: the per-IP, per-phone and per-store limits, the honeypot and
// the duplicate window all run.
import http from 'k6/http';
import { check } from 'k6';
import { randomIntBetween, uuidv4 } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const host = __ENV.STORE_HOST || 'souq.localhost';
const port = __ENV.STORE_PORT || '18191';
const base = `http://${host}:${port}`;
const rate = Number(__ENV.RATE || 50);

export const options = {
  hosts: { [host]: '127.0.0.1' },
  scenarios: {
    orders: {
      executor: 'constant-arrival-rate',
      rate,
      timeUnit: '1s',
      duration: __ENV.DURATION || '60s',
      preAllocatedVUs: 60,
      maxVUs: 200,
    },
  },
  thresholds: {
    http_req_failed: ['rate==0'],
    'http_req_duration{name:order}': ['p(95)<150'],
    checks: ['rate==1'],
  },
};

export function setup() {
  const res = http.get(`${base}/p/load-test`);
  const m = res.body && res.body.match(/name="variant" value="([0-9A-Z]{26})"/);
  if (res.status !== 200 || !m) {
    throw new Error(`load-test product not found (${res.status}); start sfdemo with -load`);
  }
  return { variant: m[1] };
}

export default function (data) {
  const phone = `010${randomIntBetween(10000000, 99999999)}`;
  const ip = `10.${randomIntBetween(0, 255)}.${randomIntBetween(0, 255)}.${randomIntBetween(1, 254)}`;
  const res = http.post(
    `${base}/order`,
    {
      variant: data.variant,
      qty: '1',
      key: uuidv4(),
      name: 'عميل اختبار',
      phone,
      governorate: 'EG-C',
      city: 'مدينة نصر',
      address: '12 شارع عباس العقاد، الدور الثالث',
      notes: '',
      website: '',
    },
    {
      headers: { Origin: base, 'X-Forwarded-For': ip },
      redirects: 0,
      tags: { name: 'order' },
    },
  );
  check(res, {
    'order placed (303 to thank-you)': (r) => r.status === 303 && /\/thank-you\/[0-9A-Z]{26}$/.test(r.headers.Location || ''),
  });
}
