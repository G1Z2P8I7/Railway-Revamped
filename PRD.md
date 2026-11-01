# Product Requirements Document (PRD) — Railway Revamped
**Document ID:** `PRD-RR-2026-01`  
**Product:** Railway Revamped  
**Version:** 2.0.0 (Post-Audit Grounded Edition)  
**Target Platform:** Web (Desktop/Mobile PWA) & Native Mobile (iOS / Android)  

---

## 1. Product Overview & Strategic Intent

### 1.1 Objective
Railway Revamped modernizes the Indian passenger rail reservation experience. It replaces legacy architectural bottlenecks with high-throughput distributed segment leasing, eliminates rush-hour checkout chokes through pre-authenticated identity, guarantees family seating cohesion, and ensures complete financial safety for passenger transactions.

### 1.2 Core Value Proposition
- **High-Concurrency Booking Core:** Target capacity of 250,000 bookings/minute (with burst headroom to 300,000/minute) with sub-300ms core platform commit latency.
- **Pre-Authenticated Checkout:** On-device biometric verification (~200ms) with zero synchronous SMS OTP dependency during the critical 60-second Tatkal rush.
- **Deterministic Family Allocation:** Spatial constraint solver ensuring families on a single PNR are grouped together, with automated lower-berth priority for seniors.
- **Inclusive Multi-Tier Access:** Tiered attestation supporting cutting-edge mobile hardware down to basic smartphones (Persona 3), supported by an ultra-light (<50 KB) text mode and preserved voice helplines (139).
- **Financial Integrity:** Two-phase authorization preventing money debits on expired inventory, backed by instant transit ledger refunds and automated bank reversals.

---

## 2. Target Personas & Inclusive User Scenarios

### Persona 1: Urgent / Emergency Commuter (Arjun, 28)
- **Context:** Tech professional in Bengaluru needing an emergency Tatkal ticket to Patna.
- **Pain Points:** Site crashes at 10:00 AM; SMS OTP delayed by telco queueing; money deducted without a PNR.
- **Solution in RR:** Pre-authenticated session, 500ms jitter-compensated virtual lobby, atomic seat lease, and immediate PNR generation.

### Persona 2: Family Vacation Planner (Sunita, 42)
- **Context:** Traveling with 5 family members, including an elderly parent (71) and two children.
- **Pain Points:** Family scattered across different coaches; lack of visibility into waitlist clearance odds.
- **Solution in RR:** Spatial berth cohesion algorithm grouping the family in a single cabin; automated lower berth for senior passenger; Bayesian waitlist clearance probabilities.

### Persona 3: Rural / Low-End Device Commuter (Ramesh, 53)
- **Context:** Farmer in rural Odisha using an entry-level Android phone with basic OS patch levels and 2G/3G connectivity.
- **Pain Points:** High-end hardware attestation blocks his phone; complex captchas fail to load; English-only interfaces.
- **Solution in RR:** Tiered attestation accepting `MEETS_BASIC_INTEGRITY`; ultra-low-bandwidth text mode (<50 KB payload); vernacular Odia interface; assisted booking integration via local Common Service Center (CSC) post-office kiosks.

### Persona 4: Frequent Business Traveler (Priya, 34)
- **Context:** Travel between Delhi and Chandigarh for corporate consulting.
- **Pain Points:** Strict cancellation fees under April 2026 rules; manual TDR filing for delayed trains.
- **Solution in RR:** 1-click self-service boarding updates; automated 1-click TDR filing when trains are delayed >3 hours; optional Flexi-Cancel protection.

---

## 3. Product Goals & Grounded Key Performance Indicators (KPIs)

| Metric Category | Baseline (Observed / Reported) | Railway Revamped Target KPI | Measurement & Scope Boundary |
| :--- | :--- | :--- | :--- |
| **Peak Core Capacity** | Reported ~32k/min (legacy PRS) / 1.5L/min (in-progress cloud PRS) | **250,000 bookings/min** (300k burst headroom) | Core reservation engine throughput |
| **Core Platform Commit Latency** | Variable (often timing out at peak) | **p99 < 300 ms** | Origin platform compute (excludes bank gateway network transit) |
| **End-to-End Checkout Time** | 45s – 180s (including OTP delays) | **p95 < 6.0 s / p99 < 15.0 s** | Total user journey including banking gateway redirect and callback |
| **Search Response Latency** | 1.5s – 4.0s under load | **p95 < 150 ms** | Cached & indexed route graph reads |
| **Core Platform Availability** | Overnight shutdowns (3-5h reported in specific zones) | **99.99% Availability** | Scoped strictly to Railway Revamped core infrastructure (excludes 3rd party banks) |
| **Physical Double-Bookings** | Edge-case segment collisions | **0.00% (Absolute Zero)** | Enforced via SQL composite primary key on physical berths |
| **Failed-Payment Recovery** | 5 – 7 business days manual refund | **< 5s (Transit Ledger) / Auto-Initiated < 30s (Bank)** | NPCI/Gateway reversal API dispatch |

---

## 4. Functional Requirements (FR)

### FR-01: Tiered Identity, Pre-Attestation & Session Security
- **FR-01.1 Pre-Authentication Flow:** Identity attestation (Aadhaar KYC via DigiLocker) is completed during account onboarding. At booking rush (10:00 AM Tatkal), zero synchronous SMS OTPs are required.
- **FR-01.2 Tiered Attestation:**
  - *Tier 1 (High-End):* WebAuthn / FIDO2 Passkeys, Google Play Integrity (`MEETS_DEVICE_INTEGRITY`), Apple App Attest.
  - *Tier 2 (Basic / Legacy Android - Persona 3):* `MEETS_BASIC_INTEGRITY` paired with device-bound app tokens.
  - *Tier 3 (Assisted / Web Kiosk):* Pre-verified biometric authentication at Common Service Centers (CSC) or Post Office booking terminals.
- **FR-01.3 Strict Per-User Lease Cap:** An authenticated account is permitted exactly **one active seat lease** at any given time (maximum 6 passengers per lease). Parallel hoarding across multiple tabs is blocked with `HTTP 409 Conflict`.
- **FR-01.4 Account Recovery:** If a user loses their phone, recovery is initiated via Aadhaar Virtual ID + registered mobile OTP with a 12-hour security cooling-off window.

### FR-02: Intelligent Catchment Search & Multi-Quota Assistant
- **FR-02.1 Catchment Search:** Searching a metropolitan region (e.g. "Delhi") queries all cluster terminals (NDLS, DLI, NZM, ANVT, DEC) with integrated local transit transfer times.
- **FR-02.2 Real-Time Availability Feed:** Client availability updates via Server-Sent Events (SSE) reflecting segment occupancy.
- **FR-02.3 Multi-Quota Scanner:** Surfaces General, Tatkal, Senior Citizen, Ladies, and Divyangjan quotas in a unified comparative view.

### FR-03: Concurrency Engine & 500ms Jitter Virtual Lobby
- **FR-03.1 Virtual Waiting Lobby:** Users hitting Tatkal endpoints within $T-2$ minutes are placed in a virtual waiting lobby. At $T=0$, requests are dispatched in **500 ms randomized buckets**, eliminating raw millisecond network ping advantages of fiber users over rural mobile connections.
- **FR-03.2 Citizen Exclusivity Window:** For the first 30 minutes of Tatkal (10:00–10:30 AM AC, 11:00–11:30 AM Non-AC), commercial aggregator and agent API keys are hard-throttled. Only authenticated citizen accounts are processed.
- **FR-03.3 Atomic Segment Lease:** The system issues a 300-second cryptographic lease on the selected segment. If payment does not complete within 300 seconds, the background lease-sweeper automatically returns the segment to the pool.

### FR-04: Deterministic Berth Allocation & 8-Hour Chart Preparation
- **FR-04.1 Spatial Family Cohesion Solver:** The allocation solver minimizes intra-group berth distance, ensuring families are seated in the same bay/cabin.
- **FR-04.2 Senior Citizen Berth Guarantee:** Passengers aged 60+ and pregnant women are systematically assigned available lower berths.
- **FR-04.3 Three-Tier Queue Progression:** Manages Confirmed (CNF) $\rightarrow$ Reservation Against Cancellation (RAC, 2 passengers sharing side-lower berth) $\rightarrow$ Waitlist (WL).
- **FR-04.4 8-Hour Automated Chart Preparation:** At $T-8$ hours before origin departure, unutilized quota berths are swept, waitlists/RAC are cascaded, physical berths are assigned, and remaining empty seats are released for "Current Booking".

### FR-05: Payment Orchestration & Resilient Reversals
- **FR-05.1 Hold-and-Authorize Flow:** Funds are not captured until the active seat lease is validated.
- **FR-05.2 Webhook & Polling Timeouts:** The gateway webhook window is set to **120 seconds**. A background reconciliation poller runs every **60 seconds** to resolve hanging states.
- **FR-05.3 Dual Refund Mechanism:**
  - *Option A (Instant - < 5s):* Credit to closed-loop Railway Revamped Transit Ledger.
  - *Option B (Source Bank Reversal):* Refund API instruction dispatched within 30 seconds; funds settle according to issuer bank clearing cycles (typically 2–4 hours for UPI, 1–3 business days for cards).

### FR-06: Cancellation Policy & Voluntary Trip Protection
- **FR-06.1 Policy Reform Recommendation:** Recommend policy reversal of the punitive April 2026 cancellation slabs (restoring a reasonable 4-hour window rather than 8-hour zero-refund lockouts).
- **FR-06.2 Automated Waitlist Cascade:** When a passenger cancels, the vacated seat is immediately allocated to the next RAC/Waitlist passenger in sequence.
- **FR-06.3 Optional Flexi-Cancel Add-on:** A voluntary micro-fee add-on (₹20–₹50) allowing passengers to cancel up to 2 hours before charting with a 100% fare refund.

### FR-07: Travel-Day Experience, Security & Inclusive Support
- **FR-07.1 Asymmetric Offline QR Ticket:** Digital tickets are cryptographically signed using the railway central private key (Ed25519). TTE handheld terminals hold only the public key to verify ticket authenticity offline, preventing forgery from lost/compromised handhelds.
- **FR-07.2 Dead-Phone Fallback:** Manifest validation via physical Government Photo ID, SMS verification code, or printed ticket copies with verifiable static signatures.
- **FR-07.3 Preserved Secure Voice Support (Helpline 139):** Inbound voice helpline (139) is preserved for rural, elderly, and visually impaired passengers. Calls feature automated security advisories warning passengers never to disclose OTPs or install remote-access apps (AnyDesk/TeamViewer).

---

## 5. Formal PNR Lifecycle & Transaction State Machine

```
               ┌─────────────┐
               │ INITIALIZED │
               └──────┬──────┘
                      │ User selects train & requests lease
                      ▼
               ┌─────────────┐   Lease TTL (300s) elapses
               │    HELD     ├──────────────────────────┐
               └──────┬──────┘                          │
                      │ Payment authorization launched  │
                      ▼                                 ▼
               ┌─────────────┐   300s TTL timeout    ┌─────────┐
               │   PAYMENT   ├──────────────────────►│ EXPIRED │
               │   PENDING   │                       └────┬────┘
               └──────┬──────┘                            │
                      │                                   │ Late webhook debit
       ┌──────────────┴──────────────┐                    │ arrives after lease expired
       ▼                             ▼                    ▼
┌─────────────┐               ┌─────────────┐      ┌──────────────┐
│  CONFIRMED  │               │   FAILED    │      │    REFUND    │
└──────┬──────┘               └─────────────┘      │   PENDING    │
       │                                           └──────┬───────┘
       ├─────────────────────────────┐                    │ Auto-reversal
       ▼                             ▼                    ▼ executed
┌─────────────┐               ┌─────────────┐      ┌──────────────┐
│  TRAVELLED  │               │  CANCELLED  │      │   REFUNDED   │
└─────────────┘               └─────────────┘      └──────────────┘
```

### State Definitions & Valid Transitions
- `INITIALIZED`: Cart created, passenger inputs validated. Next: `HELD`, `ABORTED`.
- `HELD`: Segment occupancy lease locked in memory with 300s TTL. Next: `PAYMENT_PENDING`, `EXPIRED`.
- `PAYMENT_PENDING`: Payment gateway authorization in progress. Next: `CONFIRMED`, `FAILED`, `EXPIRED`.
- `EXPIRED`: 300-second lease window elapsed without payment confirmation. Next: `REFUND_PENDING` (if late webhook debit arrives), or terminal exit.
- `FAILED`: Payment gateway reported explicit authorization failure. Next: Terminal exit.
- `ABORTED`: User voluntarily exited checkout before payment launch. Next: Terminal exit.
- `CONFIRMED`: Payment authorized within lease window; PNR generated; berths recorded in relational database. Next: `TRAVELLED`, `CANCELLED`.
- `CANCELLED`: Confirmed ticket voluntarily cancelled prior to departure. Next: Terminal exit.
- `TRAVELLED`: Journey completed; chart verified by onboard TTE manifest. Next: Terminal archive.
- `REFUND_PENDING`: Funds debited after lease expired or failed; reversal instruction queued. Next: `REFUNDED`.
- `REFUNDED`: Reversal acknowledged by payment gateway/bank rails. Next: Terminal archive.

---

## 6. Non-Functional Requirements & Governance

### NFR-01: Performance & Latency Budgets
- Core Platform Commit Latency: $p99 < 300\text{ ms}$.
- Search Index Query Latency: $p95 < 150\text{ ms}$.
- End-to-End Checkout (including external banking leg): $p95 < 6.0\text{ s}$, $p99 < 15.0\text{ s}$.

### NFR-02: Availability Scope
- Railway Revamped core services: **99.99% Availability SLA**.
- External banking gateway failures are isolated via circuit breakers to prevent cascading core service degradation.

### NFR-03: Data Protection & Legal Retention (DPDP Act, 2023 & Rules, 2025)
- **Aadhaar Protection:** Plaintext Aadhaar numbers are never stored; only irreversible HMAC tokens generated inside a certified Hardware Security Module (HSM).
- **Statutory Data Retention vs. Erasure:** In compliance with the Railways Act and financial auditing laws, completed travel records are retained in encrypted cold storage for **12 months** for legal audit, claims, and anti-fraud investigations, after which passenger PII is scrubbed. Right-to-Erasure requests apply to marketing and active profile directories.
- **Sovereignty & Localization:** 100% of data, backups, and compute run within Indian sovereign boundaries on MeitY-empanelled domestic cloud infrastructure.
