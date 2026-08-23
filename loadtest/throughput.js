import http from 'k6/http';
import { check } from 'k6';
import { Counter, Trend } from 'k6/metrics';

const gatewayURL = __ENV.GATEWAY_URL || 'http://gateway:8080';
const backendURL = __ENV.MOCK_BACKEND_URL || 'http://mock-backend:8081';
const virtualUsers = Number(__ENV.VUS || 100);
const testDuration = __ENV.DURATION || '30s';

const allowed = new Counter('gatekeeper_allowed');
const unexpected = new Counter('gatekeeper_unexpected');
const allowedDuration = new Trend('gatekeeper_allowed_duration', true);

http.setResponseCallback(http.expectedStatuses(200, 204));

export const options = {
  scenarios: {
    allowed_throughput: {
      executor: 'constant-vus',
      vus: virtualUsers,
      duration: testDuration,
      gracefulStop: '5s',
    },
  },
  thresholds: {
    checks: ['rate==1'],
    gatekeeper_unexpected: ['count==0'],
  },
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const reset = http.post(`${backendURL}/_mock/reset`, null, {
    tags: { phase: 'setup' },
  });
  if (!check(reset, { 'backend counters reset': (response) => response.status === 204 })) {
    throw new Error(`could not reset backend counters: HTTP ${reset.status}`);
  }
}

export default function () {
  const response = http.get(`${gatewayURL}/api/search?q=throughput`, {
    headers: { 'X-API-Key': 'throughput-client' },
    tags: { phase: 'load', route: 'search' },
  });

  if (response.status === 200) {
    allowed.add(1);
    allowedDuration.add(response.timings.duration);
    unexpected.add(0);
  } else {
    unexpected.add(1);
  }

  check(response, {
    'request is allowed': (result) => result.status === 200,
  });
}

export function teardown() {
  const response = http.get(`${backendURL}/_mock/stats`, {
    responseType: 'text',
    tags: { phase: 'teardown' },
  });
  check(response, {
    'backend received allowed traffic': (result) => {
      if (result.status !== 200) {
        return false;
      }
      const stats = JSON.parse(result.body);
      return stats.total > 0 && stats.total === stats.search && stats.upload === 0;
    },
  });
}
