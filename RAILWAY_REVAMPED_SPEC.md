# Railway Revamped — Master Blueprint & System Specification
**Project Code:** `RR-2026-V2`  
**Status:** Grounded Engineering Blueprint & Master Solution Matrix  
**Target Domain:** Sovereign High-Throughput Rail Reservation & Passenger Transit Lifecycle  

---

## 1. Executive Summary & Vision

The Indian Railways booking ecosystem—facilitated by IRCTC and the underlying Centre for Railway Information Systems (CRIS) Passenger Reservation System (PRS)—is one of the highest-volume transactional platforms on earth.

According to Ministry of Railways and Press Information Bureau (PIB) documentation from August 2026, the legacy PRS architecture (originating in 1986 and deployed on 2010-era Itanium/OpenVMS servers) supports approximately 32,000 bookings per minute, with a phased migration underway toward a cloud-based replacement targeting 150,000 bookings per minute. However, recurring outages and friction persist during peak Tatkal openings (10:00 AM AC, 11:00 AM Non-AC), overnight regional maintenance shutdowns continue to be documented (such as Mumbai PRS in Jan 2026 and Delhi PRS in Mar 2026), and recent regulatory changes (such as the April 2026 cancellation slabs and mandatory Aadhaar verification during rush hours) have created friction for legitimate travelers.

**Railway Revamped** is a grounded modernization blueprint that re-architects the national ticketing infrastructure into a resilient, sovereign cloud-native platform. It replaces scalar seat counters with a **Segment-Based Inventory Matrix**, moves identity verification out of the 60-second Tatkal rush to **Tiered Pre-Attestation**, guarantees **0.00% physical double-bookings** via database-level uniqueness constraints, and eliminates leaked inventory through a **reliable sorted-set lease-sweeper**.

---

## 2. Core Architectural Tenets & Grounded Baselines

```
 ┌─────────────────────────────────────────────────────────────────────────────┐
 │                         RAILWAY REVAMPED TENETS                             │
 ├────────────────────────┬──────────────────────────┬─────────────────────────┤
 │ 1. SEGMENT MATRIX      │ 2. TIERED PRE-AUTH       │ 3. SAFE LEASE SWEEPER   │
 │ Berths are modeled as  │ Identity attestation is  │ Seats are leased with a │
 │ bitmasks over station  │ completed *before* rush; │ 300s TTL; persistent    │
 │ hops, enabling partial │ zero SMS OTP friction at │ ZSET sweeper guarantees │
 │ multi-passenger sharing│ 10:00 AM Tatkal rush.    │ zero inventory leaks.   │
 ├────────────────────────┼──────────────────────────┼─────────────────────────┤
 │ 4. ACTIVE-ACTIVE CORE  │ 5. RECONCILED PAYMENTS   │ 6. RADICAL FAIRNESS     │
 │ 4-region domestic SQL  │ Two-phase pre-auth hold  │ Transparent waitlist    │
 │ deployment eliminates  │ prevents money debits on │ progression, family     │
 │ scheduled overnight    │ expired leases; instant  │ berth cohesion, and     │
 │ regional maintenance.  │ ledger credit options.   │ asymmetric QR tickets.  │
 └────────────────────────┴──────────────────────────┴─────────────────────────┘
```

### Operational Baselines & Scoped SLAs
- **Throughput Capacity:** Target benchmark of **250,000 bookings/minute** (with burst headroom to 300,000/minute) to comfortably exceed peak Tatkal surges.
- **Core Platform Commit Latency:** $p99 < 300\text{ ms}$ for origin platform compute (excluding external banking network transit).
- **End-to-End Checkout Duration:** $p95 < 6.0\text{ s}$, $p99 < 15.0\text{ s}$ (accounting for external bank gateway authorization cycles).
- **Core Platform Availability:** **99.99% Availability SLA** for infrastructure under Railway Revamped control.
- **Data Sovereignty:** 100% of data, compute, and cryptographic key management hosted within Indian territorial borders on MeitY-empanelled Cloud Service Providers and National Informatics Centre (NIC) data centers.

---

## 3. Comprehensive Problem-to-Solution Matrix (All 82 Items)

Each of the 82 active problems from the comprehensive audit is addressed below. Verified problems (`OPEN`, `PARTLY`, `WORSE`) receive concrete architectural rectifications, while `UNVERIFIED` items receive proactive structural safeguards.

### Category A: Real-Time & High Concurrency Load

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **1** | **Tatkal rush spikes** (10 AM AC / 11 AM non-AC) | `OPEN` | **500ms Jitter Virtual Waiting Lobby:** Edge ingress buffers peak Tatkal arrivals and dispatches them in randomized 500ms batch buckets, neutralizing geographic fiber ping advantages. |
| **2** | **Inventory contention** (Sold out in 60s) | `OPEN` | **In-Memory Segment Lease Core:** Segment bitmask evaluation runs in clustered Redis using `{train:date}` hash tags, granting 300s atomic leases without touching relational disk locks. |
| **3** | **Session timeouts & slowness** | `OPEN` | **Stateless Edge Sessions:** Stateless JWT claims validated at domestic edge nodes; session state is decoupled from origin servers, eliminating dropped sessions during surges. |
| **4** | **Payment gateway dependency & missing PNRs** | `OPEN` | **Hold-and-Authorize Pattern:** Funds are not captured until segment lease lock is validated. Webhook timeout set to 120s with 60s background reconciliation worker. |
| **5** | **Stale availability display** | `UNVERIFIED` | **SSE Segment Availability Feeds:** Push real-time seat decrement events to client screens via Server-Sent Events, ensuring exact availability before users initiate checkout. |
| **6** | **Peak-festival load** (Diwali/Chhath surges) | `OPEN` | **Cell-Based Autonomous Scaling:** Regional railway zones operate in isolated compute cells. Heavy Northern/Eastern holiday surges cannot exhaust Western/Southern capacity. |

### Category B: Server-Side & System Architecture

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **7** | **Scaling booking core** (Legacy 1986 OpenVMS PRS) | `PARTLY` | **Next-Gen Distributed Core (Go/Rust):** Replaces legacy monolithic PRS with a distributed microservices core targeting 250,000 bookings/minute with Raft consensus. |
| **8** | **Single points of congestion** | `UNVERIFIED` | **Decoupled Service Topology:** Strict physical decoupling of Search/Catalog, Identity, Segment Leasing, Payment Settlement, and Chart Generation. |
| **9** | **Maintenance windows** (Overnight regional shutdowns) | `OPEN` | **4-Region Active-Active Distributed SQL:** Deploys CockroachDB/Yugabyte across Delhi, Mumbai, Bengaluru, and Kolkata with online schema updates—0 scheduled nightly downtime. |
| **10** | **Poor error handling** | `UNVERIFIED` | **Actionable RFC 7807 Error Envelopes:** Standardized error payloads providing transparent user explanations, actionable retry links, and deterministic idempotency keys. |
| **11** | **Third-party dependencies** | `UNVERIFIED` | **Circuit Breakers & Autonomous Fallbacks:** Critical booking paths utilize circuit breakers (Resilience4j pattern) to isolate external failures without degrading core reservation. |

### Category C: Bots, Touts & Fairness

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **12** | **Automated booking tools & scripts** | `PARTLY` | **Layered Bot Defense & Memory-Hard PoW:** Combines JA4+ edge TLS fingerprinting with dynamic memory-hard challenges (Argon2 / mPoW) under surge, bankrupting bot compute. |
| **13** | **Agent & tout activity** | `PARTLY` | **30-Minute Citizen Exclusivity Window:** Commercial API keys and third-party aggregator endpoints are hard-throttled for the first 30 minutes of Tatkal (10:00–10:30 AM AC). |
| **14** | **Captcha friction on human users** | `PARTLY` | **Zero-Captcha Human Verification:** Captchas removed for authenticated passkey/biometric sessions; behavioral biometrics handle verification passively. |
| **15** | **Account abuse & multi-account farming** | `PARTLY` | **Strict Per-User Active Lease Capping:** Enforces $\text{Active\_Leases}(\text{User\_ID}) \le 1$. Multi-tab automated hoarding is blocked with `HTTP 409 Conflict`. |

### Category D: Security & Privacy

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **16** | **Credential attacks & stuffing** | `UNVERIFIED` | **Passwordless Tiered Authentication:** Shifts auth to WebAuthn/FIDO2 passkeys and device biometrics, eliminating static passwords vulnerable to credential stuffing. |
| **17** | **Data exposure & leak risks** | `OPEN` | **Sovereign HSM Tokenization:** Plaintext Aadhaar is never stored; irreversible tokens generated inside domestic FIPS 140-2 Level 3 HSMs. Passenger PII encrypted with AES-256-GCM. |
| **18** | **Phishing, fake apps & fake numbers** | `OPEN` | **TRAI DLT Verified Caller-ID & Helpline 139:** Secures official helpline 139 with verified headers; automated takedown bots scrub fraudulent search-engine sponsored refund ads. |
| **19** | **Session hijacking & token security** | `UNVERIFIED` | **Origin-Bound Secure Cookies:** Web sessions guarded by `HttpOnly`, `SameSite=Strict` cookies; mobile apps utilize device-bound short-lived tokens. |

### Category E: User Experience & Design

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **20** | **Clunky, bloated UI** | `PARTLY` | **Streamlined Modern Interface:** High-speed micro-frontends with sub-second page transitions, clear typographic hierarchy, and a 3-step checkout flow. |
| **21** | **Confusing quotas & obscure rules** | `UNVERIFIED` | **Smart Multi-Quota Assistant:** Scans all eligible passenger quotas (General, Tatkal, Senior Citizen, Ladies, Divyangjan) in parallel and displays them in a comparative view. |
| **22** | **Search limitations & rigid stations** | `PARTLY` | **Metropolitan Catchment Clustering:** Searching "Delhi" queries all terminal stations (NDLS, DLI, NZM, ANVT, DEC) with integrated transit times and connecting train finders. |
| **23** | **Accessibility gaps** | `PARTLY` | **Inclusive Access & Low-Bandwidth Mode:** Full WCAG 2.1 AAA compliance, 12 Indian languages via Bhashini NLP, and an ultra-light (<50 KB) text mode for 2G/3G connections. |
| **24** | **Refund & cancellation rule clarity** | `UNVERIFIED` | **Visual Pre-Cancellation Breakdown:** Real-time calculator displays the exact refund amount and penalty deductions before the user confirms cancellation. |

### Category F: Support & Operations

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **25** | **Customer support responsiveness** | `UNVERIFIED` | **In-App Dispute Concierge & RailMadad:** Context-aware resolution inside the authenticated app with direct links to train TTE handhelds and division grievance officers. |
| **26** | **Refund delays (5–7 business days)** | `OPEN` | **Grounded Dual-Refund Model:** Instant credit (<5s) to closed-loop Transit Ledger; automated source reversal instruction dispatched to bank rails within 30s (settling in 2–4 hours for UPI). |
| **27** | **Opaque rule changes** | `UNVERIFIED` | **Machine-Readable Policy Changelog:** Clear public changelog with contextual in-app notices explaining rule adjustments and fare revisions. |
| **28** | **Non-refundable convenience fees** | `OPEN` | **Transparent Fee Schedules:** Upfront pricing displaying all base fares and statutory taxes with zero surprise add-ons at payment gateway redirect. |

### Category G: Pricing & Fare Transparency

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **29** | **Opaque dynamic/flexi fares** | `UNVERIFIED` | **Interactive Dynamic Fare Timeline:** Visual graphs depicting base fare, flexi slab stage, catering fees, and GST with historical surge trends. |
| **30** | **Hidden extras appearing at checkout** | `UNVERIFIED` | **All-Inclusive Upfront Pricing:** Search results display the exact final payable amount; no surprise insurance or charges added downstream. |
| **31** | **Pre-selected travel insurance traps** | `OPEN` | **Explicit Opt-In Checkboxes:** Insurance unchecked by default, clearly displaying policy coverage, claim rules, and underwriter details. |
| **32** | **Harsh refund deductions (April 2026 rules)** | `WORSE` | **Policy Reform & Voluntary Flexi-Cancel:** Recommends reverting the 8-hour zero-refund lockout at policy level; offers optional voluntary trip protection add-on for 100% refund. |

### Category H: Waitlist & Confirmation Logic

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **33** | **Unreliable confirmation odds** | `PARTLY` | **Bayesian Waitlist Forecasting:** Transparent probability bands trained on historical charting trends, seasonal spikes, and quota re-pooling data. |
| **34** | **Opaque berth allocation & family splitting** | `PARTLY` | **Spatial Cohesion Constraint Solver:** Cost-minimization solver groups passengers on a single PNR in the same bay/cabin and automatically reserves lower berths for seniors. |
| **35** | **Auto-upgrade & Vikalp confusion** | `UNVERIFIED` | **Visual Alternate Train Selector:** Interactive cards detailing alternate Vikalp options, transit duration differences, and upgrade probabilities. |

### Category I: Booking Rules & Policy Limits

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **36** | **Monthly ticket limits friction** | `OPEN` | **Verified Family Circle Accounts:** Legitimate family profiles linked via DigiLocker share household limits (up to 24 tickets/month) without triggering tout flags. |
| **37** | **Aadhaar verification checkout choke** | `WORSE` | **Shift-Left Pre-Attestation:** Aadhaar KYC verified once during account setup. Booking at 10:00 AM completes via ~200ms on-device biometric check with zero SMS OTPs. |
| **38** | **Lack of multi-leg booking** | `UNVERIFIED` | **Connecting Journey Engine:** Auto-constructs multi-leg routes with guaranteed minimum connection windows (≥90 minutes) and single-click booking. |
| **39** | **Rigid modification rules** | `PARTLY` | **Self-Service Ticket Modification:** 1-click boarding point adjustments up to 4 hours before departure directly from the digital ticket view. |

### Category J: Information Accuracy & Live Transit

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **40** | **Inaccurate live train status** | `UNVERIFIED` | **Direct RTIS Locomotive GPS Feed:** Real-Time Train Information System updates ingested at ~467 writes/s into TimescaleDB/ClickHouse for sub-minute live tracking. |
| **41** | **Coach position & composition errors** | `UNVERIFIED` | **Dynamic Station Rake Maps:** Platform layout feeds displaying real-time coach stopping markers and distance from station footbridges. |
| **42** | **Late cancellation & diversion alerts** | `UNVERIFIED` | **Instant Push Alerts:** Immediate notifications dispatched via verified WhatsApp, SMS, and app push the moment control office logs train diversions. |

### Category K: App & Technical Quality

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **43** | **Mobile app crashes under rush load** | `OPEN` | **Lightweight Native Core & PWA:** Eliminates bloated webviews in favor of native mobile frameworks with offline-first asset caching. |
| **44** | **Inconsistent web vs app behavior** | `UNVERIFIED` | **Unified Headless API:** Single gRPC/GraphQL service mesh powering both web and mobile clients, guaranteeing identical business rules. |
| **45** | **Weak or missing push alerts** | `UNVERIFIED` | **High-Throughput Notification Backbone:** Kafka-backed notification workers integrated with Apple APNs, Firebase Cloud Messaging, and transactional SMS. |
| **46** | **No public status page** | `UNVERIFIED` | **Public Transparency Status Dashboard (`status.railway.in`):** Real-time monitoring of regional core latencies, gateway health, and automated incident logs. |

### Category L: Ecosystem & Business Model

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **47** | **Monopolistic stagnation** | `OPEN` | **Open Transit Standard (ONDC Rail):** Standardized, secure APIs allowing licensed third-party aggregators to compete on UX against a common reservation core. |
| **48** | **Third-party app reliance during outages** | `OPEN` | **High-Availability Core Architecture:** 99.99% availability of core reservation infrastructure eliminates the need for passengers to flee to third-party tools during rush. |
| **49** | **Upselling clutter & ads** | `PARTLY` | **Ad-Free Core Transaction Flow:** Zero third-party banners, gaming pop-ups, or distracting ads in the booking workflow. Catering and lounges isolated in an optional post-booking tab. |
| **50** | **E-catering reliability issues** | `UNVERIFIED` | **Escrow-Protected Meal Delivery:** Payments held in escrow and released to FSSAI-licensed station vendors only upon passenger delivery verification code. |

### Category M: Trust, Fraud & Grievances

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **51** | **Refund scams & fake helplines** | `OPEN` | **In-App Refund Tracking & Anti-Spoof DLT:** In-app authenticated resolution combined with anti-spoof DLT headers; public warnings against remote-desktop scams. |
| **52** | **Confusing TDR filing process** | `UNVERIFIED` | **Automated 1-Click TDR:** Detects train delays >3 hours or AC failures automatically and pre-populates TDR forms with 1-click filing. |
| **53** | **Grievance routing delays** | `PARTLY` | **Bidirectional RailMadad Integration:** Direct link between PNR and onboard staff handhelds with automated divisional SLA escalations. |

### Category N: Inclusivity & Equity

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **54** | **Concession access hurdles** | `UNVERIFIED` | **Digital DigiLocker Pass Verification:** Automated validation of Divyangjan and student concessions via DigiLocker and national UDID portal. |
| **55** | **Low-bandwidth disadvantage** | `UNVERIFIED` | **Ultra-Low Bandwidth Mode (<50 KB):** Text-optimized booking interface functioning seamlessly over spotty 2G/3G connections in rural areas. |
| **56** | **First-time user confusion** | `PARTLY` | **Voice-Guided Vernacular Prompts:** Conversational step-by-step assistance in 12 regional languages for first-time digital bookers. |

### Category O: Account & Access Security

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **57** | **False-positive bot blocking of genuine users** | `OPEN` | **Multi-Dimensional Anomaly Scoring:** Evaluates holistic behavioral entropy rather than raw IP counts; family circles are explicitly exempt from shared payment flags. |
| **58** | **Arbitrary account deactivation** | `OPEN` | **Transparent Account Health Center:** Account health view with actionable self-remediation via DigiLocker re-attestation and a guaranteed 24-hour support SLA. |
| **59** | **Rigid user IDs & forgotten logins** | `UNVERIFIED` | **Multi-Identifier Login:** Universal authentication via Mobile Number + Biometric Passkey or Aadhaar VID without remembering arcane usernames. |
| **60** | **Single-session conflict drops** | `UNVERIFIED` | **Cross-Device Session Handoff:** Seamlessly start a search on desktop and complete payment on mobile without session corruption or forced logouts. |
| **61** | **Geographic/network IP restrictions** | `UNVERIFIED` | **Sovereign International Edge Gateways:** Verified NRI access through international edge points of presence with verified foreign payment routing. |

### Category P: Time & Fairness Mechanics

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **62** | **Lack of authoritative server clock** | `UNVERIFIED` | **Microsecond Precision NTP Synchronization:** Client countdown timers synchronized to national atomic time (NPL India) displayed on booking screens. |
| **63** | **Network latency inequality** | `UNVERIFIED` | **500ms Randomized Jitter Batching:** Virtual waiting room aggregates rush requests in 500ms windows, eliminating raw millisecond network ping advantages. |
| **64** | **Unfair agent dependence** | `PARTLY` | **Enforced 30-Minute Citizen Priority:** Commercial API access barred during initial Tatkal opening; reservation reserved exclusively for individual citizens. |

### Category Q: Payments & Refunds Infrastructure

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **65** | **Bank-specific gateway failures** | `UNVERIFIED` | **Real-Time Dynamic Gateway Routing:** Multi-rail routing (SBI, HDFC, Razorpay, Cashfree) steering transactions away from degraded banking channels. |
| **66** | **Saved-card friction & tokenization errors** | `UNVERIFIED` | **RBI-Compliant Network Tokenization:** Biometric one-step card checkout on supported Visa/Mastercard/RuPay devices. |
| **67** | **Refund to source delays** | `OPEN` | **Dual-Refund Rails:** Choice of instant credit (<5s) to Transit Ledger or automated bank reversal instruction dispatched in <30s. |
| **68** | **No real-time payment tracking** | `PARTLY` | **Visual Payment State Machine:** Real-time visual tracker (`Authorized` ➔ `Segment Leased` ➔ `PNR Confirmed`) providing end-to-end status clarity. |

### Category R: Travel-Day & Physical Transit

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **69** | **Passenger ID & minor name mismatches** | `UNVERIFIED` | **DigiLocker Co-Traveler Directory:** Auto-fills verified passenger details directly from DigiLocker, eliminating manual typographical errors. |
| **70** | **Child ticket rule confusion** | `UNVERIFIED` | **Contextual Child Fare Rules:** Prompts rules automatically based on child's age (under 5 free without berth, 5–12 optional half-fare/full-berth) with transparent price display. |
| **71** | **Partial cancellation complexities** | `UNVERIFIED` | **Interactive Passenger Cancellation Matrix:** Seat selector allowing cancellation of individual passengers on a group PNR with immediate refund recalculation. |
| **72** | **Digital ticket verification friction** | `UNVERIFIED` | **Asymmetric Offline QR Verification (Ed25519):** Tickets signed by central private key; TTE handhelds hold only public key, preventing forgery from stolen devices. |

### Category S: Platform Health & Engineering Standards

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **73** | **Unannounced releases & breaking changes** | `UNVERIFIED` | **Canary Deployments & Semantic Versioning:** Progressive traffic shifting (1% ➔ 5% ➔ 25% ➔ 100%) with automated rollback alarms on elevated error rates. |
| **74** | **Third-party trackers & bloated scripts** | `UNVERIFIED` | **Zero-Third-Party Telemetry:** Strict Content Security Policy (CSP); all platform metrics are internal, lightweight, and host-local. |
| **75** | **No open developer API** | `UNVERIFIED` | **Public Developer Sandbox:** Rate-limited test sandboxes and standardized transit schemas for authorized aggregators and civic tech developers. |
| **76** | **Absence of public outage post-mortems** | `UNVERIFIED` | **Public Blameless Post-Mortems:** Engineering reports published on status blog within 48 hours of any platform disruption. |
| **77** | **Poor user-facing observability** | `UNVERIFIED` | **Real-Time Capacity Banners:** In-app banners alerting users to banking gateway slowdowns before they initiate transactions. |

### Category T: Legal, Regulatory & Data Rights

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **78** | **Data deletion & DPDP compliance** | `PARTLY` | **DPDP Act, 2023 & Rules, 2025 Compliance Engine:** Built-in Citizen Privacy Portal providing self-service Data Download and Right-to-Erasure pipelines. |
| **79** | **Unsolicited marketing communications** | `UNVERIFIED` | **Granular Consent Toggles:** Explicit opt-in toggles for marketing; critical transactional travel notifications remain strictly isolated and unpolluted. |
| **80** | **Buried terms & policy alterations** | `UNVERIFIED` | **Plain-Language Summary Highlights:** Plain-language bulleted summaries of policy terms displayed at checkout with diff highlights when rules change. |

### Category U: Specialized Edge Cases & Sustainability

| # | Problem | Audit Status | Railway Revamped Architectural Solution |
|---|---|---|---|
| **81** | **Rail-specific edge cases** (Slip coaches, diverted routes) | `UNVERIFIED` | **Graph-Based Route Topology:** Native modeling of slip coaches, coach detachments, and route diversions in the core booking graph. |
| **82** | **Environmental impact transparency** | `UNVERIFIED` | **Green Travel Carbon Metrics:** Verified carbon savings metrics compared to road/air travel displayed on digital tickets. |

---

## 4. Grounded Integration of Audit Part 1 Resolved Items

The audit verified 3 key improvements that have taken place in Indian Railways. Railway Revamped builds upon these exact three foundations:

1. **Two Apps Problem (RailOne Integration):** Preserves the unified mobile interface introduced by RailOne, bringing reserved, suburban unreserved, platform ticketing, and onboard amenities into a single modern progressive web and mobile application.
2. **Integration Gap (IRCTC / UTS / Platform Tickets):** Maintains the consolidated account and wallet infrastructure, eliminating separate logins or siloed payment instruments across suburban and long-distance travel.
3. **Chart-Timing Confusion (8-Hour Advance Charting):** Permanently incorporates the 8-hour pre-departure chart preparation rule (instituted July 2025 / November 2025 circulars) into the automated scheduling daemon, providing passengers certainty early the previous evening for morning departures.

---

## 5. Grounded Rectification of Audit Part 2 Worse Points

The audit identified 2 specific areas where conditions deteriorated recently. Here is how Railway Revamped rectifies them:

1. **Harsh Refund Slabs (April 2026: 0% refund <8 hours):**
   - *Problem:* Passengers facing genuine emergencies under 8 hours forfeit 100% of their fare, while vacant seats are often refilled by waitlist cascades.
   - *Rectification:* Propose reversing this punitive policy rule back to a reasonable 4-hour window. In parallel, offer an optional voluntary "Flexi-Cancel" trip protection add-on (₹20–₹50) allowing emergency cancellation up to 2 hours before charting with a 100% fare refund.
2. **Verification Hurdles during Rush (Aadhaar OTP at Tatkal Opening):**
   - *Problem:* Demanding synchronous SMS OTPs at 10:00:00 AM causes network congestion, telco delays, session timeouts, and lost tickets.
   - *Rectification:* Shift verification strictly to **Pre-Booking Account Attestation**. Passengers link Aadhaar once via DigiLocker. At booking rush, authentication completes via local device biometrics in ~200ms with zero reliance on telecommunication SMS delivery.

---

## 6. Real-World Migration, Governance & Disaster Recovery

### 6.1 Strangler Fig Migration Plan (24-Month Rollout)
- **Phase 0 (Months 1–4):** CDC data mirroring from legacy CRIS PRS; stream master train schedules and station topologies to the distributed database.
- **Phase 1 (Months 5–8):** Pilot dual-write on 50 non-rush Shatabdi and Vande Bharat trains.
- **Phase 2 (Months 9–14):** Shadow booking run during peak Tatkal to validate segment lock concurrency; zonal cutover of Western and Southern Railway zones.
- **Phase 3 (Months 15–20):** Nationwide cutover of Northern, Central, and Eastern railway zones.
- **Phase 4 (Months 21–24):** Complete decommissioning of legacy OpenVMS PRS mainframes and transition to permanent operations.

### 6.2 Disaster Recovery & Business Continuity
- **RPO (Recovery Point Objective):** $\le 0\text{ seconds}$ for committed bookings (guaranteed by synchronous multi-region Raft consensus); $\le 5\text{ seconds}$ for in-flight uncommitted session leases.
- **RTO (Recovery Time Objective):** $\le 60\text{ seconds}$ automated failover across domestic regions in the event of catastrophic data center loss.

### 6.3 Estimated Project Staffing & Core Engineering Pods
- **Platform & Distributed Engine Pod (18 Engineers):** Rust/Go systems engineers, distributed database specialists (CockroachDB/Redis), and performance tuning architects.
- **Identity, Security & Attestation Pod (12 Engineers):** WebAuthn/FIDO2 experts, HSM integration engineers, and anti-fraud graph analysts.
- **Frontend, Accessibility & Mobile Pod (16 Engineers):** Native iOS/Android engineers, WCAG accessibility specialists, and Bhashini NLP vernacular integration engineers.
- **Integration, QA & Strangler Migration Pod (14 Engineers):** Legacy CRIS PRS interface specialists, automated regression testing teams, and site reliability engineers (SREs).
