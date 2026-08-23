import http from 'k6/http';
import { check } from 'k6';
import { Counter, Trend } from 'k6/metrics';

const gatewayURL = __ENV.GATEWAY_URL || 'http://gateway:8080';
const backendURL = __ENV.MOCK_BACKEND_URL || 'http://mock-backend:8081';

const allowed = new Counter('gatekeeper_allowed');
const rejected = new Counter('gatekeeper_rejected');
const unexpected = new Counter('gatekeeper_unexpected');
const gatewayDuration = new Trend('gatekeeper_duration', true);

http.setResponseCallback(http.expectedStatuses(200, 429));

export const options = {
  scenarios: {
    exact_limit: {
      executor: 'per-vu-iterations',
      vus: 1000,
      iterations: 1,
      maxDuration: '1m',
    },
  },
  thresholds: {
    checks: ['rate==1'],
    gatekeeper_allowed: ['count==100'],
    gatekeeper_rejected: ['count==900'],
    gatekeeper_unexpected: ['count==0'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const reset = http.post(`${backendURL}/_mock/reset`, null, {
    tags: { phase: 'setup' },
  });
  if (!check(reset, { 'backend counters reset': (response) => response.status === 204 })) {
    throw new Error(`could not reset backend counters: HTTP ${reset.status}`);
  }

  return { apiKey: `exact-limit-${Date.now()}` };
}

export default function (data) {
  const response = http.get(`${gatewayURL}/api/search?q=load-test`, {
    headers: { 'X-API-Key': data.apiKey },
    tags: { phase: 'load', route: 'search' },
  });
  gatewayDuration.add(response.timings.duration);

  if (response.status === 200) {
    allowed.add(1);
    unexpected.add(0);
  } else if (response.status === 429) {
    rejected.add(1);
    unexpected.add(0);
  } else {
    unexpected.add(1);
  }

  check(response, {
    'status is allowed or rejected': (result) => result.status === 200 || result.status === 429,
  });
}

export function teardown() {
  const response = http.get(`${backendURL}/_mock/stats`, {
    tags: { phase: 'teardown' },
  });
  const stats = response.json();

  check(response, {
    'backend received exactly 100 requests': (result) =>
      result.status === 200 &&
      stats.total === 100 &&
      stats.search === 100 &&
      stats.upload === 0,
  });
}
