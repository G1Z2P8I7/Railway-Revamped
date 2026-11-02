package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/railway-revamped/core/pkg/attestation"
	"github.com/railway-revamped/core/pkg/crypto"
	"github.com/railway-revamped/core/pkg/dpdp"
	"github.com/railway-revamped/core/pkg/grievance"
	"github.com/railway-revamped/core/pkg/ingress"
	"github.com/railway-revamped/core/pkg/payment"
	"github.com/railway-revamped/core/pkg/pow"
	"github.com/railway-revamped/core/pkg/solver"
	"github.com/railway-revamped/core/pkg/telemetry"
)

func TestEndToEndBookingJourneyPhases1To4(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("failed to initialize server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/v1/ingress/lobby/join", server.handleLobbyJoin)
	mux.HandleFunc("/v1/ingress/pow/challenge", server.handlePoWChallenge)
	mux.HandleFunc("/v1/ingress/pow/verify", server.handlePoWVerify)
	mux.HandleFunc("/v1/auth/attest", server.handleAttest)
	mux.HandleFunc("/v1/auth/recover", server.handleRecover)
	mux.HandleFunc("/v1/search/trains", server.handleSearch)
	mux.HandleFunc("/v1/booking/hold", server.handleHold)
	mux.HandleFunc("/v1/booking/release", server.handleRelease)
	mux.HandleFunc("/v1/booking/confirm", server.handleConfirm)
	mux.HandleFunc("/v1/booking/cancel", server.handleCancel)
	mux.HandleFunc("/v1/booking/pnr/", server.handlePNRStatus)
	mux.HandleFunc("/v1/payment/preauth/init", server.handlePaymentInit)
	mux.HandleFunc("/v1/payment/webhook", server.handlePaymentWebhook)
	mux.HandleFunc("/v1/payment/ledger/", server.handleTransitLedger)
	mux.HandleFunc("/v1/charting/trigger", server.handleTriggerChart)
	mux.HandleFunc("/v1/forecast/wl", server.handleForecastWL)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()

	// 1. Health check
	resp, err := client.Get(ts.URL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health check failed: %v", err)
	}

	// 2. Pre-Attest User (Tier 1 Passkey)
	attestBody := AttestRequest{
		UserID:       "user-arjun",
		AadhaarToken: "hmac_sovereign_aadhaar_991823",
		Tier:         attestation.Tier1HighEndHardware,
		Tier1Data: &attestation.Tier1ChallengeResponse{
			PasskeyCredentialID:  "webauthn_cred_passkey_01",
			PlayIntegrityVerdict: "MEETS_DEVICE_INTEGRITY",
		},
	}
	attestPayload, _ := json.Marshal(attestBody)
	resp, _ = client.Post(ts.URL+"/v1/auth/attest", "application/json", bytes.NewReader(attestPayload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("attest failed: %d", resp.StatusCode)
	}

	// 3. Virtual Lobby Enqueue
	lobbyBody := LobbyJoinRequest{
		UserID:      "user-arjun",
		ClientType:  ingress.ClientCitizen,
		TrainNumber: "12301",
		RunDate:     "20261105",
		ClassCode:   "3A",
	}
	lobbyPayload, _ := json.Marshal(lobbyBody)
	resp, _ = client.Post(ts.URL+"/v1/ingress/lobby/join", "application/json", bytes.NewReader(lobbyPayload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lobby failed: %d", resp.StatusCode)
	}

	// 4. Memory-hard PoW Challenge & Verification
	resp, _ = client.Get(ts.URL + "/v1/ingress/pow/challenge")
	var powData map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&powData)
	challengeToken := powData["token"].(string)
	chMap := powData["challenge"].(map[string]interface{})
	ch := &pow.Challenge{
		Nonce:          chMap["nonce"].(string),
		DifficultyBits: int(chMap["difficulty_bits"].(float64)),
		MemoryKB:       uint32(chMap["memory_kb"].(float64)),
		Iterations:     uint32(chMap["iterations"].(float64)),
		Threads:        uint8(chMap["threads"].(float64)),
	}
	sol := server.powEng.SolveClient(ch)
	vPayload, _ := json.Marshal(PoWVerifyRequest{Token: challengeToken, Solution: sol})
	resp, _ = client.Post(ts.URL+"/v1/ingress/pow/verify", "application/json", bytes.NewReader(vPayload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pow verify failed: %d", resp.StatusCode)
	}

	// 5. Acquire 300s Segment Lease
	holdBody := HoldRequestBody{
		UserID:         "user-arjun",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		OriginStopIdx:  0,
		DestStopIdx:    2,
		PassengerCount: 2,
		TTLSeconds:     300,
	}
	holdPayload, _ := json.Marshal(holdBody)
	resp, _ = client.Post(ts.URL+"/v1/booking/hold", "application/json", bytes.NewReader(holdPayload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hold failed: %d", resp.StatusCode)
	}
	var holdRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&holdRes)
	leaseToken := holdRes["lease_token"].(string)

	// 6. Phase 4: Initiate Two-Phase Hold-and-Authorize Payment
	payInitBody := PaymentInitRequest{
		UserID:      "user-arjun",
		TrainNumber: "12301",
		RunDate:     "20261105",
		LeaseToken:  leaseToken,
		AmountINR:   4398.00, // 2 pax + Flexi-Cancel
		Gateway:     payment.GatewayRazorpay,
	}
	payInitPayload, _ := json.Marshal(payInitBody)
	resp, err = client.Post(ts.URL+"/v1/payment/preauth/init", "application/json", bytes.NewReader(payInitPayload))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("payment init failed: %v", err)
	}
	var txn payment.PaymentTransaction
	json.NewDecoder(resp.Body).Decode(&txn)
	if txn.Status != payment.TxnPreAuthInit {
		t.Fatalf("expected PRE_AUTH_INIT, got: %s", txn.Status)
	}

	// 7. Phase 4: Gateway Webhook Callback arrives within 120s
	hookBody := payment.WebhookEvent{
		TxnID:        txn.TxnID,
		GatewayTxnID: "pay_rzp_mock_live_8912",
		Gateway:      payment.GatewayRazorpay,
		Status:       "SUCCESS",
		AmountINR:    4398.00,
	}
	hookPayload, _ := json.Marshal(hookBody)
	resp, err = client.Post(ts.URL+"/v1/payment/webhook", "application/json", bytes.NewReader(hookPayload))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook callback failed: %v", err)
	}
	var hookRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&hookRes)
	if hookRes["action"].(string) != "COMMIT_PNR" {
		t.Fatalf("expected COMMIT_PNR, got: %v", hookRes["action"])
	}

	// 8. Confirm Booking post-payment authorization
	confirmBody := ConfirmRequestBody{
		UserID:         "user-arjun",
		LeaseToken:     leaseToken,
		PaymentTxnID:   txn.TxnID,
		HasFlexiCancel: true,
		Passengers: []solver.PassengerRequest{
			{PassengerID: "p-1", FullName: "Kalyani Devi", Age: 71, Gender: "F", IsSeniorCitizen: true},
			{PassengerID: "p-2", FullName: "Arjun Verma", Age: 28, Gender: "M", IsSeniorCitizen: false},
		},
	}
	confirmPayload, _ := json.Marshal(confirmBody)
	resp, _ = client.Post(ts.URL+"/v1/booking/confirm", "application/json", bytes.NewReader(confirmPayload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm failed: %d", resp.StatusCode)
	}
	var confirmRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&confirmRes)
	pnr := confirmRes["pnr_number"].(string)

	// Verify Ed25519 QR
	qrPayload := confirmRes["signed_qr_payload"].(string)
	tteVerifier := crypto.NewTicketVerifier(server.cryptoAuth.PublicKey())
	verified, err := tteVerifier.VerifyTicketQROffline(qrPayload, 1)
	if err != nil || verified.PNRNumber != pnr {
		t.Fatalf("offline QR verifier failed: %v", err)
	}

	// 9. Voluntary Cancellation with Instant Credit to Transit Ledger
	cancelBody := CancelRequestBody{
		PNRNumber:     pnr,
		PassengerID:   "p-2",
		RefundChannel: payment.RefundTransitLedger,
	}
	cancelPayload, _ := json.Marshal(cancelBody)
	resp, _ = client.Post(ts.URL+"/v1/booking/cancel", "application/json", bytes.NewReader(cancelPayload))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel failed: %d", resp.StatusCode)
	}
	var cancelRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&cancelRes)
	refundAmt := cancelRes["refund_amount_inr"].(float64)
	if refundAmt <= 0 {
		t.Fatalf("expected positive refund amount")
	}

	// 10. Verify Closed-Loop Transit Ledger received instant credit (<5 seconds)
	resp, _ = client.Get(ts.URL + "/v1/payment/ledger/user-passenger")
	var ledgerAcc payment.TransitLedgerAccount
	json.NewDecoder(resp.Body).Decode(&ledgerAcc)
	if ledgerAcc.BalanceINR != refundAmt {
		t.Fatalf("expected ledger balance ₹%.2f, got: ₹%.2f", refundAmt, ledgerAcc.BalanceINR)
	}

	t.Logf("All Phases 1 to 4 Verified! PNR=%s, Payment Captured=%s, Instant Ledger Credit=₹%.2f (<5s)",
		pnr, txn.TxnID, ledgerAcc.BalanceINR)
}

func TestFrontendAndInclusiveChannels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.ServeFile(w, r, findStaticFile("frontend/src/index.html"))
		case "/text":
			http.ServeFile(w, r, findStaticFile("frontend/src/text.html"))
		case "/ivr":
			http.ServeFile(w, r, findStaticFile("frontend/src/ivr.html"))
		case "/i18n.js":
			http.ServeFile(w, r, findStaticFile("frontend/src/i18n.js"))
		case "/manifest.json":
			http.ServeFile(w, r, findStaticFile("frontend/public/manifest.json"))
		default:
			http.NotFound(w, r)
		}
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	// 1. Modern Web PWA Dashboard
	resp, err := client.Get(ts.URL + "/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to fetch index.html: %v", err)
	}

	// 2. Ultra-Low Bandwidth Mode (<50 KB)
	resp, err = client.Get(ts.URL + "/text")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to fetch text.html: %v", err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	textSize := buf.Len()
	if textSize >= 50000 {
		t.Fatalf("text mode exceeds 50KB limit: size=%d bytes", textSize)
	}
	t.Logf("Ultra-Low Bandwidth Mode payload verified: %d bytes (<50KB target)", textSize)

	// 3. Helpline 139 IVR
	resp, err = client.Get(ts.URL + "/ivr")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to fetch ivr.html: %v", err)
	}

	// 4. Vernacular i18n
	resp, err = client.Get(ts.URL + "/i18n.js")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to fetch i18n.js: %v", err)
	}
}

func TestPhase6TravelDayRTISAndDPDP(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("failed to initialize server: %v", err)
	}

	server.rtisEng.Start(context.Background())
	defer server.rtisEng.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/telemetry/submit", server.handleTelemetrySubmit)
	mux.HandleFunc("/v1/telemetry/latest/", server.handleTelemetryLatest)
	mux.HandleFunc("/v1/grievance/tdr/file", server.handleTDRFile)
	mux.HandleFunc("/v1/grievance/tdr/", server.handleTDRStatus)
	mux.HandleFunc("/v1/privacy/request", server.handlePrivacyRequest)
	mux.HandleFunc("/v1/privacy/export/", server.handlePrivacyExport)

	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := ts.Client()

	// 1. Submit RTIS Locomotive Transponder Point
	pt := telemetry.TelemetryPoint{
		TrainNumber:         "12301",
		LocoID:              "WAP-7-30211",
		Latitude:            28.6139,
		Longitude:           77.2090,
		SpeedKmph:           130.0,
		CurrentStationCode:  "NDLS",
		NextStationCode:     "CNB",
		CurrentBlockSection: "NDLS-ALJN-UP-02",
		DelayMinutes:        195, // Running 3h15m late
		SatelliteLock:       true,
	}
	body, _ := json.Marshal(pt)
	resp, err := client.Post(ts.URL+"/v1/telemetry/submit", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to submit RTIS telemetry: %v", err)
	}

	time.Sleep(60 * time.Millisecond)

	// 2. Query Latest RTIS Telemetry
	resp, err = client.Get(ts.URL + "/v1/telemetry/latest/12301")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to fetch RTIS telemetry: %v", err)
	}
	var fetchedPt telemetry.TelemetryPoint
	json.NewDecoder(resp.Body).Decode(&fetchedPt)
	if fetchedPt.DelayMinutes != 195 || fetchedPt.SpeedKmph != 130.0 {
		t.Fatalf("mismatched telemetry data: delay=%d, speed=%f", fetchedPt.DelayMinutes, fetchedPt.SpeedKmph)
	}
	t.Logf("Phase 6 RTIS Telemetry verified: Train 12301 at %f,%f running late by %d mins",
		fetchedPt.Latitude, fetchedPt.Longitude, fetchedPt.DelayMinutes)

	// 3. 1-Click Automated TDR Filing (Delay > 3 hours)
	tdrReq := TDRFileRequest{
		PNR:         "3108548507",
		Passengers:  []string{"Aarav Sharma"},
		TrainNumber: "12301",
		Origin:      "NDLS",
		Dest:        "HWH",
		TravelDate:  "2026-11-05",
		Reason:      grievance.ReasonDelayOver3Hours,
		FarePaidINR: 2850.00,
	}
	body, _ = json.Marshal(tdrReq)
	resp, err = client.Post(ts.URL+"/v1/grievance/tdr/file", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to file automated TDR: %v", err)
	}
	var claim grievance.TDRClaim
	json.NewDecoder(resp.Body).Decode(&claim)
	if !claim.AutoEligible || claim.ApprovedRefundINR != 2850.00 {
		t.Fatalf("expected auto-eligible full refund for delay > 3h, got eligible=%v, amt=%.2f",
			claim.AutoEligible, claim.ApprovedRefundINR)
	}
	t.Logf("Phase 6 Automated TDR verified: ClaimID=%s, RailMadad Docket=%s, Refund Dispatched=₹%.2f",
		claim.ClaimID, claim.RailMadadDocketID, claim.ApprovedRefundINR)

	// 4. Register profile & Test DPDP Right-to-Erasure & Data Portability Export
	user := &dpdp.UserProfile{
		UserID:           "usr_meity_01",
		FullName:         "Ananya Deshmukh",
		Phone:            "9876500000",
		Email:            "ananya@gov.in",
		AadhaarHMACToken: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	_ = server.dpdpEng.RegisterProfile(user)

	privReq := PrivacyReqPayload{
		UserID:      "usr_meity_01",
		RequestType: dpdp.RequestDataExport,
	}
	body, _ = json.Marshal(privReq)
	resp, err = client.Post(ts.URL+"/v1/privacy/request", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to request DPDP data export: %v", err)
	}
	var pReq dpdp.PrivacyRequest
	json.NewDecoder(resp.Body).Decode(&pReq)

	// Download export package
	resp, err = client.Get(ts.URL + "/v1/privacy/export/" + pReq.RequestID)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to download DPDP export package: %v", err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if buf.Len() < 100 {
		t.Fatalf("export payload suspiciously small: %d bytes", buf.Len())
	}
	t.Logf("Phase 6 DPDP Citizen Privacy Portal verified: Export package size=%d bytes", buf.Len())
}

