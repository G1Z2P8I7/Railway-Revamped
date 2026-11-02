import http from 'k6/http';
import { check, sleep } from 'k6';

// 5,000 Concurrent Virtual Users simulating peak 10:00 AM Tatkal rush
export const options = {
  stages: [
    { duration: '15s', target: 500 },   // Warm-up ramp
    { duration: '30s', target: 2000 },  // 10:00 AM rush surge
    { duration: '30s', target: 5000 },  // Peak Tatkal spike (250k-300k/min target)
    { duration: '15s', target: 0 },     // Ramp down
  ],
  thresholds: {
    // Core Platform Commit Latency budget: p99 < 300ms
    http_req_duration: ['p(95)<150', 'p(99)<300'],
    http_req_failed: ['rate<0.01'], // <1% network failures
  },
};

const BASE_URL = __ENV.TARGET_URL || 'http://localhost:8080';

export default function () {
  const userId = `vu-citizen-${__VU}-${__ITER}`;
  const trainNo = '12301';
  const runDate = '20261105';
  const classCode = '3A';
  const quotaCode = 'GN';

  // 1. Join 500ms Jitter Virtual Waiting Lobby
  const lobbyPayload = JSON.stringify({
    user_id: userId,
    client_type: 'CITIZEN',
    train_number: trainNo,
    run_date: runDate,
    class_code: classCode,
  });

  const lobbyRes = http.post(`${BASE_URL}/v1/ingress/lobby/join`, lobbyPayload, {
    headers: { 'Content-Type': 'application/json' },
  });

  check(lobbyRes, {
    'lobby join status is 200': (r) => r.status === 200,
  });

  // 2. Acquire Atomic Segment Lease
  const holdPayload = JSON.stringify({
    user_id: userId,
    train_number: trainNo,
    run_date: runDate,
    class_code: classCode,
    quota_code: quotaCode,
    origin_stop_index: 0,
    dest_stop_index: 4,
    passenger_count: 2,
    lease_ttl_seconds: 300,
  });

  const holdRes = http.post(`${BASE_URL}/v1/booking/hold`, holdPayload, {
    headers: { 'Content-Type': 'application/json' },
  });

  const isHeld = check(holdRes, {
    'hold responded without server crash': (r) => r.status === 200 || r.status === 409,
  });

  if (holdRes.status === 200) {
    const holdData = JSON.parse(holdRes.body);
    const leaseToken = holdData.lease_token;

    // 3. Two-Phase Payment Pre-Auth Init
    const payInitPayload = JSON.stringify({
      user_id: userId,
      train_number: trainNo,
      run_date: runDate,
      lease_token: leaseToken,
      amount_inr: 4300.00,
      gateway: 'RAZORPAY',
    });

    const payRes = http.post(`${BASE_URL}/v1/payment/preauth/init`, payInitPayload, {
      headers: { 'Content-Type': 'application/json' },
    });

    if (payRes.status === 200) {
      const payData = JSON.parse(payRes.body);

      // 4. Webhook Authorization Simulation
      const webhookPayload = JSON.stringify({
        txn_id: payData.txn_id,
        gateway_txn_id: `rzp_mock_${__VU}_${__ITER}`,
        gateway: 'RAZORPAY',
        status: 'SUCCESS',
        amount_inr: 4300.00,
      });

      http.post(`${BASE_URL}/v1/payment/webhook`, webhookPayload, {
        headers: { 'Content-Type': 'application/json' },
      });

      // 5. Booking Confirmation & Asymmetric Ed25519 Ticket Generation
      const confirmPayload = JSON.stringify({
        user_id: userId,
        lease_token: leaseToken,
        payment_transaction_id: payData.txn_id,
        has_flexi_cancel: true,
        passengers: [
          { passenger_id: 'p1', full_name: 'Lead Passenger', age: 34, gender: 'M', is_senior_citizen: false },
          { passenger_id: 'p2', full_name: 'Senior Passenger', age: 64, gender: 'F', is_senior_citizen: true },
        ],
      });

      const confirmRes = http.post(`${BASE_URL}/v1/booking/confirm`, confirmPayload, {
        headers: { 'Content-Type': 'application/json' },
      });

      check(confirmRes, {
        'booking confirmed successfully': (r) => r.status === 200,
      });
    }
  }

  sleep(0.1);
}
