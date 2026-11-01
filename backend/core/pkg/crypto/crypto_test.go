package crypto

import (
	"testing"
	"time"
)

func TestEd25519AsymmetricVerification(t *testing.T) {
	// Central issuance server has private key
	centralAuthority, err := GenerateSovereignKeypair(nil)
	if err != nil {
		t.Fatal(err)
	}

	// TTE handheld terminal receives ONLY public key
	tteTerminal := NewTicketVerifier(centralAuthority.PublicKey())

	// 1. Issue signed QR ticket
	ticket := TicketPayload{
		PNRNumber:     "2458901234",
		TrainNumber:   "12301",
		RunDate:       "20261105",
		Origin:        "NDLS",
		Destination:   "HWH",
		ClassCode:     "3A",
		CoachID:       "B1",
		BerthNumber:   21,
		PassengerName: "Arjun Verma",
	}

	qrString, err := centralAuthority.IssueSignedTicketQR(ticket)
	if err != nil {
		t.Fatalf("failed to issue signed ticket: %v", err)
	}

	// 2. Offline TTE verifies ticket with zero internet connectivity
	verified, err := tteTerminal.VerifyTicketQROffline(qrString, 1)
	if err != nil {
		t.Fatalf("offline verification failed: %v", err)
	}

	if verified.PNRNumber != "2458901234" || verified.BerthNumber != 21 {
		t.Fatalf("ticket payload mismatch: %+v", verified)
	}

	// 3. Compromised / Stolen TTE terminal attempts to issue a forged ticket
	_, err = tteTerminal.IssueSignedTicketQR(ticket)
	if err == nil {
		t.Fatalf("TTE terminal without private key should NOT be able to issue tickets!")
	}

	// 4. Test screenshot replay expiration (ticket with old time window)
	oldTicket := ticket
	oldTicket.IssueTimeUnix = time.Now().Unix() - 600
	oldTicket.TimeWindow = oldTicket.IssueTimeUnix / 60
	oldQR, err := centralAuthority.IssueSignedTicketQR(oldTicket)
	if err != nil {
		t.Fatal(err)
	}

	// Scanning an old QR with tolerantWindowDelta = 1 (max 60-120s) should be rejected
	_, err = tteTerminal.VerifyTicketQROffline(oldQR, 1)
	if err == nil {
		t.Fatalf("stale screenshot QR code should be rejected by anti-replay dynamic window")
	}

	// But for paper tickets (tolerantWindowDelta = -1), static verification passes
	paperVerified, err := tteTerminal.VerifyTicketQROffline(oldQR, -1)
	if err != nil {
		t.Fatalf("printed static paper ticket verification failed: %v", err)
	}
	if paperVerified.PNRNumber != "2458901234" {
		t.Fatalf("paper ticket pnr mismatch")
	}
}

func TestAadhaarHMACIrreversibility(t *testing.T) {
	secret := []byte("central-sovereign-hsm-pepper-key-01")
	auth, err := GenerateSovereignKeypair(secret)
	if err != nil {
		t.Fatal(err)
	}

	aadhaar := "987654321098"
	token1 := auth.TokenizeAadhaar(aadhaar)
	token2 := auth.TokenizeAadhaar(aadhaar)

	// Token must be deterministic for deduplication
	if token1 != token2 {
		t.Fatalf("tokens must match for identical Aadhaar")
	}

	// Plaintext Aadhaar must never be in token
	if token1 == aadhaar {
		t.Fatalf("token must be a cryptographic hash")
	}

	// Different Aadhaar yields different token
	tokenDiff := auth.TokenizeAadhaar("123456789012")
	if token1 == tokenDiff {
		t.Fatalf("different aadhaars must yield distinct tokens")
	}
}
