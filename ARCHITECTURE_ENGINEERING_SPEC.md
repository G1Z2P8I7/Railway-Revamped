# Architecture & Engineering Specification — Railway Revamped
**Document ID:** `ENG-ARCH-RR-2026-01`  
**Classification:** Distributed Railway Transaction & Segment Inventory Engine  
**Version:** 2.0.0 (Post-Audit Grounded Architecture)  

---

## 1. System Topology & Technology Architecture

Railway Revamped is designed to replace the 1986 legacy OpenVMS-based Passenger Reservation System (PRS) with an event-driven, distributed, sovereign cloud-native architecture. 

To satisfy Indian government and regulatory compliance (MeitY cloud empanelment, CERT-In cybersecurity guidelines, and RBI payment localization mandates), all physical infrastructure runs exclusively within Indian borders across MeitY-empanelled data center regions (NIC MeghRaj, or sovereign domestic regions of AWS/Azure/GCP India).

```
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                      DOMESTIC EDGE & TRAFFIC SCRUBBING TIER                            │
 │   Domestic Anycast POPs (Delhi, Mumbai, Bengaluru, Kolkata)                            │
 │   TLS 1.3 / HTTP/3 Ingress ── Layer 4/7 DDoS Scrubbing ── Rate-Limiting Engine         │
 └───────────────────────────────────────────┬────────────────────────────────────────────┘
                                             │
                                             ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                             API INGRESS & ROUTING GATEWAY                              │
 │   Envoy Fleet ── JWT Attestation Check ── 500ms Jitter-Compensated Virtual Lobby       │
 └──────┬──────────────────────┬────────────────────────┬──────────────────────┬──────────┘
        │                      │                        │                      │
        ▼                      ▼                        ▼                      ▼
 ┌─────────────┐       ┌───────────────┐        ┌───────────────┐      ┌───────────────┐
 │   SEARCH &  │       │  IDENTITY &   │        │  SEGMENT-LOCK │      │    PAYMENT    │
 │ ROUTE GRAPH │       │  PRE-AUTH     │        │  LEASE CORE   │      │ ORCHESTRATOR  │
 │   ENGINE    │       │    SERVICE    │        │  (GO / RUST)  │      │   & LEDGER    │
 └──────┬──────┘       └───────┬───────┘        └───────┬───────┘      └───────┬───────┘
        │                      │                        │                      │
        └──────────────────────┼────────────────────────┼──────────────────────┘
                               │ (gRPC Internal Mesh)   │
                               ▼                        ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                          TRANSACTIONAL & PERSISTENCE TIER                              │
 │  ┌────────────────────────┐  ┌────────────────────────┐  ┌──────────────────────────┐  │
 │  │ Redis Cluster          │  │ Distributed SQL        │  │ TimescaleDB / ClickHouse │  │
 │  │ (Hashtagged Inventory &│  │ (CockroachDB / Yugabyte│  │ (470 writes/s RTIS GPS & │  │
 │  │ Lease ZSET Sweeper)    │  │ Segment Master Core)   │  │ Operational Audit)       │  │
 │  └────────────────────────┘  └────────────────────────┘  └──────────────────────────┘  │
 └───────────────────────────────────────────┬────────────────────────────────────────────┘
                                             │ (Internal CDC Stream)
                                             ▼
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                              APACHE KAFKA EVENT BACKBONE                               │
 │   Segment Leased ── Payment Authorized ── Charting Events ── Automated Notifications   │
 └────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Segment-Based Inventory Model vs. Scalar Counters

A train does not operate as a single aggregate seat counter. Selling a berth from Station $A$ to Station $C$ on a train running $A \rightarrow B \rightarrow C \rightarrow D$ blocks segments $(A, B)$ and $(B, C)$, but leaves segment $(C, D)$ completely open for another passenger.

### 2.1 The Segment Occupancy Bitmask
Each physical berth in a coach is modeled as a 64-bit integer bitmask across station stop indices $0, 1, \dots, N-1$:
- If a train has $N$ stops, a journey from stop index $i$ to $j$ requires segments $i, i+1, \dots, j-1$.
- The required segment mask is:
  $$\text{Mask}(i, j) = \sum_{k=i}^{j-1} 2^k$$
- A berth is available for journey $(i, j)$ if and only if:
  $$(\text{Berth\_Occupancy\_Mask} \ \& \ \text{Mask}(i, j)) == 0$$

```
 Train Route: [0: NDLS] ──► [1: CNB] ──► [2: PRYJ] ──► [3: DDU] ──► [4: HWH]
 Leg Masks:      Leg 0         Leg 1        Leg 2         Leg 3
 Bit value:      0001          0010         0100          1000

 Passenger 1 (NDLS -> PRYJ): occupies Leg 0 + Leg 1 -> Mask = 0011
 Passenger 2 (PRYJ -> HWH):  occupies Leg 2 + Leg 3 -> Mask = 1100
 Bitwise AND: (0011 & 1100) == 0000 -> Both passengers share the same physical berth!
```

### 2.2 Relational Uniqueness Constraint on Physical Berths
While in-memory structures provide fast leasing, the primary source of truth is the relational database with strict serializable isolation:

```sql
CREATE TABLE berth_allocations (
    train_run_id        UUID NOT NULL,
    coach_id            VARCHAR(10) NOT NULL,
    berth_number        SMALLINT NOT NULL,
    segment_index       SMALLINT NOT NULL, -- Individual station hop
    pnr_number          VARCHAR(10) NOT NULL,
    quota_code          VARCHAR(5) NOT NULL,
    status              VARCHAR(10) NOT NULL, -- 'HELD', 'CONFIRMED'
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (train_run_id, coach_id, berth_number, segment_index),
    FOREIGN KEY (train_run_id) REFERENCES train_runs(id) ON DELETE RESTRICT
);
```
This hard composite primary key guarantees mathematically **0.00% physical double-bookings** regardless of caching layer failures.

---

## 3. Distributed Concurrency & The Safe Lease-Sweeper Engine

### 3.1 Redis Cluster Hashtagging & Atomic Allocation
To prevent cross-slot execution errors in Redis Cluster, all keys for a specific train run are locked to the same hash slot using Redis curly-brace hash tags `{train_id:run_date}`.

Furthermore, we declare all keys explicitly and avoid fire-and-forget keyspace notifications.

#### Atomic Seat-Hold Lua Script (`lease_segment.lua`)
```lua
-- KEYS[1]: {train:date}:inv:class:quota (e.g. {12301:20261105}:inv:3A:GN)
-- KEYS[2]: {train:date}:active_leases
-- KEYS[3]: {train:date}:lease_expiry_zset
-- ARGV[1]: Requested Berth Count (e.g., 2)
-- ARGV[2]: User Account ID
-- ARGV[3]: Lease TTL in seconds (e.g., 300)
-- ARGV[4]: Lease Token (UUIDv4)
-- ARGV[5]: Segment Bitmask (e.g. 3 for stops 0->2)

local current_available = redis.call('GET', KEYS[1])
if not current_available then
    return redis.error_reply("ERR_INVENTORY_KEY_NOT_FOUND")
end

local available_num = tonumber(current_available)
local requested_num = tonumber(ARGV[1])

if available_num >= requested_num then
    -- 1. Decrement available counter
    redis.call('DECRBY', KEYS[1], requested_num)
    
    local now = redis.call('TIME')[1]
    local expires_at = now + tonumber(ARGV[3])
    
    -- 2. Store full lease metadata in hash
    local lease_payload = cjson.encode({
        user_id = ARGV[2],
        count = requested_num,
        inv_key = KEYS[1],
        seg_mask = ARGV[5],
        created_at = now,
        expires_at = expires_at
    })
    redis.call('HSET', KEYS[2], ARGV[4], lease_payload)
    
    -- 3. Push to sorted set scored by absolute expiration timestamp
    redis.call('ZADD', KEYS[3], expires_at, ARGV[4])
    
    return {1, ARGV[4], available_num - requested_num}
else
    return {0, "INSUFFICIENT_INVENTORY", available_num}
end
```

### 3.2 Reliable Lease Sweeper Worker (No Leaked Seats)
Instead of relying on fragile, fire-and-forget Redis pub/sub expiry events:
1. A persistent background daemon runs every 1 second executing a transactional sweep across `{train:date}:lease_expiry_zset`.
2. It fetches all tokens where `score <= current_timestamp` using `ZRANGEBYSCORE`.
3. For each expired token, it reads the stored metadata from `{train:date}:active_leases`, atomically increments the inventory counter back, and removes the lease from both the hash and the ZSET.
4. If a sweeper node crashes, the next leader resumes from the ZSET—**guaranteeing zero seat leaks** across server restarts.

### 3.3 Strict Per-User Lease Cap
To prevent ticket hoarding via automated parallel browser tabs, the API gateway enforces an atomic constraint:
$$\text{Active\_Leases}(\text{User\_ID}) \le 1$$
Attempting to acquire a second concurrent lease while an active lease is in `HELD` state yields `HTTP 409 Conflict`.

---

## 4. Multi-Region Active-Active Topology & Zero-Downtime PRS

Legacy regional PRS servers (Delhi, Mumbai, Kolkata, Chennai, Secunderabad) suffered from scheduled overnight maintenance shutdowns (11:45 PM – 3/4 AM). Railway Revamped achieves continuous operation across 4 domestic cloud regions:

```
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                      4-REGION ACTIVE-ACTIVE RELATIONAL TOPOLOGY                        │
 ├────────────────────────────┬─────────────────────────────┬─────────────────────────────┤
 │    REGION 1: NORTH         │      REGION 2: WEST         │     REGION 3: SOUTH         │
 │    (Delhi NCR Data Center) │      (Mumbai Data Center)   │     (Bengaluru Data Center) │
 └─────────────┬──────────────┴──────────────┬──────────────┴──────────────┬──────────────┘
               │                             │                             │
               └──────────────┬──────────────┴──────────────┬──────────────┘
                              │                             │
                              ▼                             ▼
               ┌─────────────────────────────┐ ┌──────────────────────────┐
               │      REGION 4: EAST         │ │  Disaster Recovery (DR)  │
               │      (Kolkata Data Center)  │ │  RPO = 0s, RTO <= 60s    │
               └─────────────────────────────┘ └──────────────────────────┘
```

### 4.1 Zero-Downtime Rolling Operations
- **Online Schema Evolution:** DDL migrations execute online using non-blocking multi-version concurrency control (MVCC). 
- **Partition Leases:** Database ranges are partitioned by originating railway zone. Write consensus requires a quorum of $(N/2)+1$ nodes across regions, ensuring continuous write availability even during complete loss of a regional data center.

### 4.2 Right-Sized Real-Time Telemetry Storage
- Ingesting RTIS locomotive transponder updates for ~14,000 trains reporting once every 30 seconds equals $\sim 467\text{ writes/second}$.
- Rather than running complex, high-maintenance NoSQL clusters like ScyllaDB, this stream is handled cleanly by a **PostgreSQL/TimescaleDB** time-series partition or **ClickHouse** cluster, drastically reducing infrastructure sprawl and maintenance overhead for government engineers.

---

## 5. Waitlist, RAC & Charting Architecture

The reservation lifecycle adheres to Indian Railways' three-tier booking structure:

```
 [Confirmed Berth (CNF)] ◄─── [Reservation Against Cancellation (RAC)] ◄─── [Waitlist (WL)]
```

### 5.1 RAC Allocation Model
- Passengers in RAC are allocated a seat (sharing a Side Lower berth: 2 passengers per berth).
- As confirmed tickets are cancelled prior to charting, RAC passengers are automatically promoted to confirmed berths in chronological priority order.

### 5.2 Automated 8-Hour Chart Preparation Engine
At exactly $T-8$ hours before scheduled train departure from origin:
1. **Quota Re-Pooling:** Unutilized VIP, Defence, Foreign Tourist, and Emergency quotas are swept and returned to the General/Tatkal pool.
2. **Deterministic Promotion Cascading:**
   - Waitlisted tickets are promoted to RAC.
   - RAC tickets are promoted to full confirmed berths.
3. **Berth Spatial Solver Execution:** The constraint satisfaction solver assigns physical coach and berth numbers, optimizing for family cohesion and senior citizen lower berth requirements.
4. **Current Booking Window:** Any berths remaining unallocated post-charting are opened for "Current Booking" at flat base rates until 30 minutes before departure.

---

## 6. Payment Flow & Resilient Settlement

### 6.1 Two-Phase Hold-and-Authorize Pattern
To eliminate the issue of deducted funds without confirmed seats:
1. Money is **not captured** until the segment lease is verified.
2. During payment initiation, the gateway issues a pre-authorization / UPI intent request.
3. If the payment gateway callback returns `SUCCESS` within the 120-second webhook window, the lease commits to `CONFIRMED` in CockroachDB.
4. If payment fails or lease expires, the pre-auth is voided immediately with zero debit.

```
 [CLIENT]               [PAYMENT ORCHESTRATOR]        [GATEWAY (NPCI/BANK)]        [REDIS / SQL]
    │                             │                            │                        │
    ├── 1. Request Checkout ─────►│                            │                        │
    │                             ├── 2. Verify Lease Status ──┼───────────────────────►│
    │                             │◄── Lease Valid (TTL: 240s)─┼────────────────────────┤
    │◄── 3. Return Payment Intent─┤                            │                        │
    │                             │                            │                        │
    ├── 4. Authorize Payment ────►│                            │                        │
    │                             ├── 5. Forward to Gateway ──►│                        │
    │                             │                            ├── 6. Customer Bank ────┤
    │                             │◄── 7. Webhook (120s limit)─┤                        │
    │                             │                            │                        │
    │                             ├── 8. Atomic Commit PNR ────┼───────────────────────►│
    │◄── 9. Ticket Confirmed! ────┤                            │                        │
```

### 6.2 Realistic Refund Timelines
Refunds cannot realistically complete in "<10 seconds" for bank accounts because interbank clearing cycles govern UPI/IMPS reversals:
- **Instant Option (< 5 seconds):** Credit to closed-loop **Railway Revamped Transit Ledger**, usable immediately for re-booking.
- **Bank Source Reversal:** Automated refund instruction dispatched to the payment gateway within **30 seconds**; actual bank credit reflects in the customer's account in **2 to 4 hours** (UPI) or **1 to 3 banking days** (Credit/Debit Card/Net Banking), depending on issuer bank settlement windows.
- Background reconciliation pollers run every **60 seconds** to resolve pending transactions and eliminate orphan debits.

---

## 7. Strangler Fig Migration & Cutover Plan

Transitioning from the legacy 1986 CRIS PRS mainframe to Railway Revamped without disrupting national rail transit:

```
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                              STRANGLER FIG MIGRATION PHASES                            │
 ├────────────────────────────┬─────────────────────────────┬─────────────────────────────┤
 │ PHASE 0 (Months 1–4)       │ PHASE 1 (Months 5–8)        │ PHASE 2 (Months 9–14)       │
 │ CDC Mirroring              │ Non-Peak Pilot              │ Shadow Run & Zone Split     │
 │ Stream train schedules and │ Route 50 Shatabdi / Vande   │ Shadow book all Tatkal; cut │
 │ station master data to the │ Bharat trains entirely onto │ over Western & Southern     │
 │ new distributed core.      │ the new booking engine.     │ Railway zones first.        │
 ├────────────────────────────┴─────────────────────────────┴─────────────────────────────┤
 │ PHASE 3 (Months 15–20): Nationwide cutover of Northern, Central, and Eastern zones.    │
 │ PHASE 4 (Months 21–24): Full legacy PRS mainframe retirement and audit archive.        │
 └────────────────────────────────────────────────────────────────────────────────────────┘
```

- **Rollback Safeguard:** Bi-directional event bridges ensure that during Phases 1 and 2, any booking made on the new system immediately writes a shadow record to the legacy PRS, allowing instant failback if unexpected latency or booking collisions occur.
