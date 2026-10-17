// k6 smoke test: proves the deployment answers under light load.
// Run: make loadtest-smoke  (BASE_URL defaults to http://host.docker.internal:18080)
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  vus: 10,
  duration: '15s',
  thresholds: {
    http_req_failed: ['rate<0.001'],
    http_req_duration: ['p(95)<50', 'p(99)<150'],
  },
};

const base = __ENV.BASE_URL || 'http://host.docker.internal:18080';

export default function () {
  const live = http.get(`${base}/livez`);
  check(live, { 'livez 200': (r) => r.status === 200 });
  const version = http.get(`${base}/v1/meta/version`);
  check(version, { 'version 200': (r) => r.status === 200 && r.json('go_version') !== '' });
}
