package dpdp

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDPDPComplianceEngine(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	engine, err := NewComplianceEngine(key)
	if err != nil {
		t.Fatalf("failed to init compliance engine: %v", err)
	}

	// 1. Plaintext Aadhaar should be rejected
	err = engine.RegisterProfile(&UserProfile{
		UserID:           "usr_9988",
		FullName:         "Sunita Rao",
		Phone:            "9876543210",
		Email:            "sunita@example.in",
		AadhaarHMACToken: "1234", // Plaintext/short invalid token
	})
	if err == nil {
		t.Fatalf("expected error for un-tokenized short Aadhaar")
	}

	// 2. Valid tokenized profile registration
	validToken := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	user := &UserProfile{
		UserID:           "usr_1001",
		FullName:         "Vikram Malhotra",
		Phone:            "9811223344",
		Email:            "vikram@example.in",
		AadhaarHMACToken: validToken,
		Consent: ConsentSettings{
			MarketingPromotionsEmail: true,
			MarketingPromotionsSMS:   false,
		},
	}
	if err := engine.RegisterProfile(user); err != nil {
		t.Fatalf("failed to register valid profile: %v", err)
	}

	// 3. Archive travel history: one recent journey (3 months ago), one old journey (14 months ago)
	now := time.Now()
	recentTravel := now.Add(-90 * 24 * time.Hour)
	oldTravel := now.Add(-420 * 24 * time.Hour)

	err = engine.ArchiveCompletedJourney("2109876543", "usr_1001", "12951", recentTravel, map[string]interface{}{
		"passenger": "Vikram Malhotra",
		"coach":     "B3",
		"berth":     24,
	})
	if err != nil {
		t.Fatalf("failed to archive recent journey: %v", err)
	}

	err = engine.ArchiveCompletedJourney("1109876543", "usr_1001", "12002", oldTravel, map[string]interface{}{
		"passenger": "Vikram Malhotra",
		"coach":     "C2",
		"berth":     12,
	})
	if err != nil {
		t.Fatalf("failed to archive old journey: %v", err)
	}

	// 4. Test Data Portability Export
	expReq, err := engine.SubmitPrivacyRequest("usr_1001", RequestDataExport)
	if err != nil {
		t.Fatalf("failed to submit export request: %v", err)
	}
	exportBytes, err := engine.GenerateDataPortabilityExport(expReq.RequestID)
	if err != nil {
		t.Fatalf("failed to generate export payload: %v", err)
	}

	var parsedExport map[string]interface{}
	if err := json.Unmarshal(exportBytes, &parsedExport); err != nil {
		t.Fatalf("failed to parse export JSON: %v", err)
	}
	t.Logf("DPDP Data Portability Export verified: Generated %d bytes payload for user %s",
		len(exportBytes), user.UserID)

	// 5. Test Right-to-Erasure Pipeline
	eraseReq, err := engine.SubmitPrivacyRequest("usr_1001", RequestRightToErasure)
	if err != nil {
		t.Fatalf("failed to submit erasure request: %v", err)
	}

	if err := engine.ExecuteRightToErasure(eraseReq.RequestID); err != nil {
		t.Fatalf("failed to execute right-to-erasure: %v", err)
	}

	// Verify active profile is scrubbed
	if user.FullName != "ERASED_DATA_PRINCIPAL" || !user.IsErased {
		t.Fatalf("expected profile to be scrubbed and marked IsErased")
	}
	if user.Consent.MarketingPromotionsEmail != false {
		t.Fatalf("expected marketing consent to be revoked")
	}

	// Verify statutory retention boundaries:
	// Journey 14 months ago (> 12 months) must have its PII scrubbed
	// Journey 3 months ago (< 12 months) must be preserved in encrypted cold storage under Railways Act 1989
	recentRec := engine.travelRecords["2109876543"]
	oldRec := engine.travelRecords["1109876543"]

	if recentRec.IsPIIAnonymized {
		t.Fatalf("journey within 12 months must be preserved for statutory audit")
	}
	if !oldRec.IsPIIAnonymized || oldRec.EncryptedPII != "[SCRUBBED_POST_12_MONTHS_STATUTORY_RETENTION]" {
		t.Fatalf("journey older than 12 months must have PII scrubbed upon erasure")
	}

	t.Logf("DPDP Statutory Retention verified: <12m cold encrypted record preserved; >12m record scrubbed")

	// 6. Test Periodic Retention Enforcement Sweeper
	// Run retention sweeper as of 10 months in future (making the 3-month-old record now 13 months old)
	futureDate := now.Add(300 * 24 * time.Hour)
	scrubbed := engine.EnforceRetentionPolicy(futureDate)
	if scrubbed != 1 {
		t.Fatalf("expected 1 record to be scrubbed by periodic retention policy, got %d", scrubbed)
	}
	if !recentRec.IsPIIAnonymized {
		t.Fatalf("expected recent record to now be anonymized after reaching 12 months")
	}
	t.Logf("Periodic Retention Policy Sweeper verified: Automated scrubber executed successfully")
}
