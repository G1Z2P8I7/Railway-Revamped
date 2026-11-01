package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/railway-revamped/core/pkg/crypto"
	"github.com/railway-revamped/core/pkg/solver"
)

func TestEndToEndBookingJourney(t *testing.T) {
	server, err := NewServer()
	if err != nil {
		t.Fatalf("failed to initialize server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/v1/search/trains", server.handleSearch)
	mux.HandleFunc("/v1/booking/hold", server.handleHold)
	mux.HandleFunc("/v1/booking/release", server.handleRelease)
	mux.HandleFunc("/v1/booking/confirm", server.handleConfirm)
	mux.HandleFunc("/v1/booking/pnr/", server.handlePNRStatus)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := ts.Client()

	// 1. Check /health
	resp, err := client.Get(ts.URL + "/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("health check failed: status=%v, err=%v", resp.StatusCode, err)
	}

	// 2. Search availability
	resp, err = client.Get(ts.URL + "/v1/search/trains?from=DELHI&to=PATNA&date=20261105")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("search failed: %v", err)
	}
	var searchRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&searchRes)
	trains := searchRes["trains"].([]interface{})
	if len(trains) == 0 {
		t.Fatalf("expected trains in search results")
	}

	// 3. Request Seat Hold (User 101 requests 2 seats on train 12301)
	holdBody := HoldRequestBody{
		UserID:         "user-101",
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
	resp, err = client.Post(ts.URL+"/v1/booking/hold", "application/json", bytes.NewReader(holdPayload))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("hold request failed: %v", err)
	}

	var holdRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&holdRes)
	leaseToken := holdRes["lease_token"].(string)
	if leaseToken == "" {
		t.Fatalf("expected lease_token in hold response")
	}

	// 4. Same user attempts parallel lease -> Must be blocked by Per-User Cap with HTTP 409
	resp2, err := client.Post(ts.URL+"/v1/booking/hold", "application/json", bytes.NewReader(holdPayload))
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected HTTP 409 Conflict for concurrent lease attempt, got: %d", resp2.StatusCode)
	}

	// 5. Confirm Booking with Senior Citizen & Family Member
	confirmBody := ConfirmRequestBody{
		UserID:         "user-101",
		LeaseToken:     leaseToken,
		PaymentTxnID:   "pay_rzp_mock_982341",
		HasFlexiCancel: true,
		Passengers: []solver.PassengerRequest{
			{PassengerID: "p-1", FullName: "Saraswati Sharma", Age: 68, Gender: "F", IsSeniorCitizen: true},
			{PassengerID: "p-2", FullName: "Amit Sharma", Age: 38, Gender: "M", IsSeniorCitizen: false},
		},
	}
	confirmPayload, _ := json.Marshal(confirmBody)
	resp, err = client.Post(ts.URL+"/v1/booking/confirm", "application/json", bytes.NewReader(confirmPayload))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm failed: status=%d, err=%v", resp.StatusCode, err)
	}

	var confirmRes map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&confirmRes)
	pnr := confirmRes["pnr_number"].(string)
	if pnr == "" {
		t.Fatalf("expected pnr_number in confirm response")
	}

	// Verify allocations: senior should have Lower Berth (LB)
	passengersList := confirmRes["passengers"].([]interface{})
	p1Alloc := passengersList[0].(map[string]interface{})
	if p1Alloc["BerthType"] != "LB" {
		t.Fatalf("senior passenger must get LB, got: %v", p1Alloc["BerthType"])
	}

	// Verify Ed25519 Asymmetric QR Ticket can be verified offline by a TTE terminal
	qrPayload := confirmRes["signed_qr_payload"].(string)
	tteVerifier := crypto.NewTicketVerifier(server.cryptoAuth.PublicKey())
	verifiedTicket, err := tteVerifier.VerifyTicketQROffline(qrPayload, 1)
	if err != nil {
		t.Fatalf("offline TTE ticket verification failed: %v", err)
	}
	if verifiedTicket.PNRNumber != pnr {
		t.Fatalf("PNR in QR mismatch: %s vs %s", verifiedTicket.PNRNumber, pnr)
	}

	// 6. Query PNR Status endpoint
	resp, err = client.Get(ts.URL + "/v1/booking/pnr/" + pnr)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get PNR status failed: %v", err)
	}

	var pnrRecord PNRRecord
	json.NewDecoder(resp.Body).Decode(&pnrRecord)
	if pnrRecord.PNRNumber != pnr || pnrRecord.Status != "CONFIRMED" {
		t.Fatalf("invalid PNR record retrieved: %+v", pnrRecord)
	}

	t.Logf("End-to-End Booking Verified successfully! PNR=%s, Coach=%s, Berths: Senior=%d (%s), Co-traveler=%d (%s)",
		pnr, p1Alloc["CoachID"], int(p1Alloc["BerthNumber"].(float64)), p1Alloc["BerthType"],
		int(passengersList[1].(map[string]interface{})["BerthNumber"].(float64)), passengersList[1].(map[string]interface{})["BerthType"])
}
