package attestation

import (
	"testing"
	"time"
)

func TestTieredAttestationHierarchy(t *testing.T) {
	mgr := NewAttestationManager(nil)

	// 1. Tier 1 High-End Hardware (WebAuthn / Play Integrity Device)
	t1Resp := Tier1ChallengeResponse{
		PasskeyCredentialID:  "fido2_cred_981240129",
		PlayIntegrityVerdict: "MEETS_DEVICE_INTEGRITY",
	}
	session1, err := mgr.AttestTier1Hardware("user-arjun", "hmac_aadhaar_01", t1Resp)
	if err != nil {
		t.Fatalf("tier 1 attestation failed: %v", err)
	}
	if session1.Tier != Tier1HighEndHardware {
		t.Fatalf("expected Tier 1, got: %d", session1.Tier)
	}
	if err := session1.CanBookTatkal(); err != nil {
		t.Fatalf("standard tier 1 session should be able to book tatkal: %v", err)
	}

	// 2. Tier 2 Budget Android Mobile (Ramesh, Persona 3)
	t2Payload := Tier2BudgetDevicePayload{
		DeviceModel:          "Redmi_9A_Android10",
		PlayIntegrityVerdict: "MEETS_BASIC_INTEGRITY",
		AppBoundToken:        "app_bound_token_77182",
	}
	session2, err := mgr.AttestTier2BudgetMobile("user-ramesh", "hmac_aadhaar_02", t2Payload)
	if err != nil {
		t.Fatalf("tier 2 budget mobile attestation failed: %v", err)
	}
	if session2.Tier != Tier2BudgetMobile {
		t.Fatalf("expected Tier 2, got: %d", session2.Tier)
	}

	// 3. Tier 3 Assisted Rural CSC Kiosk
	t3Payload := Tier3KioskPayload{
		OperatorCSCID:    "CSC_ODISHA_768001",
		TerminalID:       "TERM_04",
		BiometricAuthRef: "bio_ref_882194",
	}
	session3, err := mgr.AttestTier3Kiosk("user-assisted-villager", "hmac_aadhaar_03", t3Payload)
	if err != nil {
		t.Fatalf("tier 3 kiosk attestation failed: %v", err)
	}
	if !session3.IsOperatorKiosk || session3.Tier != Tier3AssistedKiosk {
		t.Fatalf("expected Tier 3 operator kiosk session")
	}

	// 4. Session Token Signing & Validation
	signedToken := mgr.SignSessionToken(session1)
	if signedToken == "" {
		t.Fatalf("expected non-empty signed session token")
	}
}

func TestDeviceRecoveryCoolingOffWindow(t *testing.T) {
	mgr := NewAttestationManager(nil)
	engine := NewAccountRecoveryEngine(mgr)

	// User lost device, recovers using 16-digit Aadhaar VID + UIDAI OTP
	session, err := engine.InitiateDeviceRecovery("user-arjun", "1234567890123456", "892134", "new_device_fp_491")
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	// Tatkal bookings must be paused during 12-hour cooling-off window
	err = session.CanBookTatkal()
	if err == nil {
		t.Fatalf("tatkal booking must be blocked during 12h cooling-off window!")
	}

	// Simulate passage of 12 hours
	session.CoolingOffUntil = time.Now().Unix() - 10
	if err := session.CanBookTatkal(); err != nil {
		t.Fatalf("tatkal booking should be allowed after cooling-off elapses: %v", err)
	}
}

func TestFamilyCircleFraudScoreExemption(t *testing.T) {
	session := &AttestedSession{
		UserID:       "user-head-of-household",
		FamilyCircle: []string{"Sunita Verma", "Arjun Verma", "Pooja Verma", "Ramesh Verma"},
	}

	// Family booking: shared payment for registered co-travelers -> 0 fraud penalty
	familyNames := []string{"Sunita Verma", "Arjun Verma"}
	score, flags := EvaluateFraudRiskScore(session, familyNames, "sunita@okhdfcbank", 5)
	if score > 0.10 {
		t.Fatalf("registered family circle should have minimal fraud score, got: %f (flags: %v)", score, flags)
	}

	// Tout booking: high velocity (20 bookings/24h) for unregistered names
	unregisteredNames := []string{"Stranger One", "Stranger Two", "Stranger Three", "Stranger Four"}
	toutScore, toutFlags := EvaluateFraudRiskScore(session, unregisteredNames, "tout_upi@ybl", 25)
	if toutScore < 0.60 {
		t.Fatalf("tout booking should trigger high anomaly score, got: %f (flags: %v)", toutScore, toutFlags)
	}
}
