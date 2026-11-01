# Security, Bot Mitigation & Fairness Specification — Railway Revamped
**Document ID:** `SEC-FAIR-RR-2026-01`  
**Classification:** Sovereign Identity, Anti-Scalping & Data Privacy Architecture  
**Version:** 2.0.0 (Post-Audit Grounded Edition)  

---

## 1. Threat Model & Adversarial Landscape

The national railway reservation system operates in an adversarial environment characterized by severe economic incentives for touts, commercial scalpers, and bot syndicates. Railway Revamped models threats across **6 primary threat vectors**:

```
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                              ADVERSARIAL THREAT VECTORS                                │
 ├────────────────────────┬──────────────────────────┬────────────────────────────────────┤
 │ 1. HIGH-SPEED BOTS     │ 2. RESIDENTIAL PROXIES   │ 3. CREDENTIAL STUFFING             │
 │ Headless browsers and  │ Rotational IPs bypassing │ Compromised credential dumps used  │
 │ automated HTTP clients │ simplistic IP rate-limit │ to hijack legitimate citizen       │
 │ attempting mass leases.│ algorithms.              │ accounts.                          │
 ├────────────────────────┼──────────────────────────┼────────────────────────────────────┤
 │ 4. REAL-DEVICE TOUT    │ 5. REVERSE-ENGINEERED    │ 6. PHISHING, FAKE HELPLINES &      │
 │    FARMS               │    MOBILE & WEB APIS     │    SCAM RECOVERY OPERATORS         │
 │ Syndicates operating   │ Scripts bypassing client │ Search-engine sponsored fraud ads  │
 │ racks of physical      │ UI to flood internal     │ and fake support numbers targeting │
 │ consumer phones.       │ endpoints directly.      │ passengers seeking refunds.        │
 └────────────────────────┴──────────────────────────┴────────────────────────────────────┘
```

---

## 2. Inclusive Pre-Attestation & Authentication Hierarchy

### 2.1 The Legacy Security Failure
Legacy systems introduced synchronous Aadhaar SMS OTP verification at 10:00:00 AM Tatkal opening to combat bots. This caused a catastrophic failure for legitimate citizens:
- Telecommunication SMS gateways suffer queueing delays of 30 to 180 seconds under nationwide peak load.
- In a 60-second Tatkal sell-out window, any SMS delay over 15 seconds guarantees a lost ticket.
- Legitimate passengers suffer timeouts, while organized tout syndicates bypass SMS queues using automated SIM-pool relays.

### 2.2 Shift-Left Verification & Tiered Attestation
Railway Revamped moves identity verification out of the critical 10:00 AM booking path to account onboarding. To prevent excluding citizens on budget or older smartphones (Persona 3, Ramesh), the system implements a **Tiered Attestation Hierarchy**:

```
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                             TIERED ATTESTATION HIERARCHY                               │
 ├────────────────────────────┬─────────────────────────────┬─────────────────────────────┤
 │ TIER 1: HIGH-END HARDWARE  │ TIER 2: BUDGET / OLDER PHONES│ TIER 3: ASSISTED / KIOSK   │
 │ • WebAuthn / FIDO2 Passkey │ • Play Integrity            │ • Biometric KYC at Common   │
 │ • Play Integrity           │   (`MEETS_BASIC_INTEGRITY`) │   Service Centers (CSC) /   │
 │   (`MEETS_DEVICE_INTEGRITY`)│ • App-bound ephemeral token │   Railway Post Offices      │
 │ • Apple App Attest         │ • Optional WhatsApp fallback│ • Low-bandwidth web portal  │
 └────────────────────────────┴─────────────────────────────┴─────────────────────────────┘
```

### 2.3 Web Browser Attestation Realities
Web browsers cannot perform hardware-backed DPoP because browser WebCrypto keys are stored in software (IndexedDB / session storage) and can be accessed by privileged browser extensions. To secure web sessions:
- Web sessions enforce `HttpOnly`, `SameSite=Strict`, `Secure` cookies with cryptographic origin binding.
- High-value actions (Tatkal lease acquisition) require a WebAuthn user presence challenge (biometric/PIN) where hardware security keys are supported.

### 2.4 Device Recovery & Lost-Phone Protocol
If a passenger loses their mobile device:
1. They authenticate via the web portal using their Aadhaar Virtual ID (VID) and an OTP sent to their UIDAI-registered mobile number.
2. A 12-hour cooling-off window is initiated during which high-velocity Tatkal bookings are paused on the new device, preventing immediate account hijacking from SIM-swap attacks.
3. Emergency ticket booking remains accessible via physical railway reservation counters upon presenting original photo identification.

---

## 3. Bot Mitigation & Anti-Scalping Architecture

```
  INCOMING TRAFFIC ──► [Layer 1: Sovereign Edge Scrubbing & TLS Fingerprinting]
                              │
                              ▼
                       [Layer 2: Per-User Active Lease Capping] (Max 1 lease)
                              │
                              ▼
                       [Layer 3: Memory-Hard Proof-of-Work (mPoW)] (Under Surge)
                              │
                              ▼
                       [Layer 4: Multi-Dimensional Behavioral Graph]
                              │
                              ▼
                       [ADMITTED TO RESERVATION ENGINE]
```

### 3.1 Layer 1: Edge TLS Fingerprinting (JA4+)
- Analyzes TLS cipher suites, elliptic curves, and HTTP/2 framing characteristics at sovereign domestic edge points of presence.
- Outright blocks automated scripting engines (`python-requests`, `aiohttp`, `curl`, `puppeteer`, `playwright-stealth`) from accessing reservation endpoints.

### 3.2 Layer 2: Strict Atomic Lease Capping
- Regardless of how many browser tabs or mobile instances a user runs, the distributed lock engine enforces:
  $$\text{Active\_Leases}(\text{User\_ID}) \le 1$$
- Attempting to acquire a parallel lease while another is in `HELD` state fails with `HTTP 409 Conflict`, completely preventing automated multi-train hoarding.

### 3.3 Layer 3: Memory-Hard Proof-of-Work (mPoW)
*Why naive 16-bit SHA-256 fails:* A single 16-bit hash puzzle requires $\sim 65,536$ computations, which a consumer GPU solves in microseconds.
*Railway Revamped Approach:* During surge alerts, the gateway injects a **memory-hard cryptographic challenge** (Argon2 / mPoW variant):
- Requires 8 MB of dedicated RAM per computation thread.
- Memory bandwidth constraints throttle parallel GPU/ASIC computation arrays.
- A single human phone solves the memory-hard challenge in ~180ms with negligible battery impact, whereas a bot farm attempting 10,000 concurrent threads is choked by physical memory bandwidth saturation.

### 3.4 Layer 4: Multi-Dimensional Identity Graph (Not Naive UPI Blocks)
*Addressing Family Exemption:* Legitimate household heads frequently pay for children, spouses, and elderly parents using a single UPI ID. Naively blocking shared UPI IDs harms ordinary citizens.

Railway Revamped evaluates a **Multi-Dimensional Behavioral Anomaly Score** ($MAS \in [0.0, 1.0]$):
- **Family Circles (Allowed):** Users can register up to 6 immediate family co-travelers in their DigiLocker-verified household. Shared UPI payments among registered family members incur zero fraud penalty.
- **Tout Patterns (Flagged):** Anomaly flags trigger only when a single payment instrument or device signature:
  1. Pays for dozens of unrelated, non-co-traveler passenger names.
  2. Books across disparate railway zones simultaneously within seconds.
  3. Exhibits mass cancellation patterns post-charting.

---

## 4. Asymmetric Offline QR Ticket Verification

```
 ┌────────────────────────────────────────────────────────────────────────────────────────┐
 │                       ASYMMETRIC TICKET CRYPTOGRAPHY (Ed25519)                         │
 ├──────────────────────────────────────────┬─────────────────────────────────────────────┤
 │      CENTRAL TICKET ISSUANCE SERVER      │          TTE HANDHELD TERMINAL (OFFLINE)    │
 │ Holds PRIVATE Key:                       │ Holds PUBLIC Key:                           │
 │ • Generates Ticket Payload               │ • Scans Dynamic QR Code                     │
 │ • Signs using Ed25519 Private Key        │ • Decrypts & Verifies Signature using Public│
 │ • Emits Encrypted QR Code                │   Key without Network Connectivity          │
 │                                          │ • CANNOT Forge Tickets If Stolen!           │
 └──────────────────────────────────────────┴─────────────────────────────────────────────┘
```

### 4.1 Asymmetric Security vs. Symmetric HMAC
- *The Danger of HMAC:* HMAC uses a single shared secret key. Storing an HMAC secret on tens of thousands of portable Travelling Ticket Examiner (TTE) handheld terminals creates an existential risk: a single stolen, reverse-engineered, or compromised handheld exposes the secret key, allowing touts to forge valid tickets nationwide.
- *The Asymmetric Fix:* Railway Revamped uses **Ed25519 Asymmetric Signatures**:
  - The Private Key remains strictly inside a central sovereign Hardware Security Module (HSM).
  - TTE handhelds store **only the Public Key**.
  - A stolen handheld can only *verify* tickets; it cannot forge a single valid boarding pass.

### 4.2 Dynamic Screen Protection & Dead-Phone Fallback
- **Dynamic Rotation:** In the mobile app, the QR code rotates an ephemeral 60-second time-hash to prevent passengers from reselling static screenshots.
- **Dead-Phone Fallback:** If a passenger's mobile battery dies during travel, the TTE verifies the passenger via:
  1. Physical Government Photo ID (Aadhaar, Voter ID, Passport, Driving License).
  2. The offline passenger manifest synchronized to the TTE terminal prior to station departure.
  3. Printed paper tickets featuring the asymmetric digital signature.

---

## 5. Preserved Voice Helplines & Anti-Phishing Safeguards

### 5.1 Preserving Human Voice Support (Helpline 139)
Deprecating telephone support discriminates against rural, elderly, illiterate, and visually impaired citizens. Railway Revamped retains and hardens **Helpline 139**:
- **TRAI DLT Verified Caller-ID:** All outbound railway calls and SMS alerts utilize verified TRAI DLT cryptographic sender headers, preventing telecom caller-ID spoofing.
- **Automated Anti-Fraud IVR Warnings:** Every inbound and outbound call opens with an unambiguous voice advisory in the caller's regional language: *"Indian Railways will NEVER ask you for an OTP, UPI PIN, or to download apps like AnyDesk or TeamViewer. Do not share codes with anyone."*

### 5.2 Eradicating Search-Engine Refund Scams
- Railway Revamped operates an automated search-engine surveillance bot that flags and issues legal takedown notices against fraudulent sponsored ads and fake customer service numbers under Indian IT Act provisions.
- All digital refund inquiries are resolved inside the verified application interface, with live status updates pushed through verified WhatsApp channels.

---

## 6. Data Localization & DPDP Act Compliance

### 6.1 Sovereign Data Infrastructure
To comply with CERT-In directives, MeitY policies, and RBI payment localization mandates:
- All compute, databases, caches, and storage run exclusively within Indian territorial borders across **MeitY-Empanelled Cloud Service Providers (CSPs)** or National Informatics Centre (NIC) data centers.
- Cryptographic keys for Aadhaar tokenization and ticket signing reside inside domestic **FIPS 140-2 Level 3 Hardware Security Modules (HSMs)** operated by C-DAC / certified national trust authorities.

### 6.2 Data Retention vs. Right-to-Erasure (DPDP Act, 2023 & Rules, 2025)
Railway Revamped reconciles citizen privacy rights under the **DPDP Act, 2023 and DPDP Rules, 2025** with statutory record-keeping duties:

| Data Category | Retention Period | Statutory Justification | Right-to-Erasure Eligibility |
| :--- | :--- | :--- | :--- |
| **Aadhaar Numbers** | **0 seconds (Never Stored)** | Aadhaar Act & UIDAI Regulations | N/A (Only irreversible HMAC token retained) |
| **Active Profile Data** | Duration of Account Active | User Profile Management | **Eligible (Scrubs within 72 hours)** |
| **Marketing Preferences** | Duration of Consent | DPDP Section 6 Consent Framework | **Eligible (Immediate Opt-Out)** |
| **Completed Journey PII** | **12 Months** | Railways Act 1989 & Financial Audit Rules | Retained in cold encrypted archive for claims/disputes, then anonymized |
| **Financial Ledger Records** | **7 Years** | Companies Act & Tax Accounting Laws | Non-erasable statutory accounting data |
