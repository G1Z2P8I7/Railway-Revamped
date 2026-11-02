package grievance

import (
	"testing"
	"time"

	"github.com/railway-revamped/core/pkg/telemetry"
)

func TestTDREngineAutomatedFilingAndSLA(t *testing.T) {
	engine := NewTDREngine()

	// Scenario 1: Train running late by 210 minutes (> 3 hours)
	rtisPoint := &telemetry.TelemetryPoint{
		TrainNumber:  "12002",
		DelayMinutes: 210,
	}

	claim, err := engine.FileTDR(
		"8123456789",
		[]string{"Aarav Sharma"},
		"12002",
		"NDLS",
		"BPL",
		"2026-11-02",
		ReasonDelayOver3Hours,
		2450.00,
		rtisPoint,
	)
	if err != nil {
		t.Fatalf("failed to file TDR: %v", err)
	}

	if !claim.AutoEligible || claim.Status != StatusAutoVerified {
		t.Fatalf("expected auto-verified claim for delay > 3 hours, got status=%s", claim.Status)
	}
	if claim.ApprovedRefundINR != 2450.00 {
		t.Fatalf("expected full refund ₹2450.00, got ₹%.2f", claim.ApprovedRefundINR)
	}
	t.Logf("1-Click Automated TDR verified: ClaimID=%s, RailMadad Docket=%s, Full Refund=₹%.2f",
		claim.ClaimID, claim.RailMadadDocketID, claim.ApprovedRefundINR)

	// Scenario 2: Prevent duplicate filing for same reason
	_, err = engine.FileTDR(
		"8123456789",
		[]string{"Aarav Sharma"},
		"12002",
		"NDLS",
		"BPL",
		"2026-11-02",
		ReasonDelayOver3Hours,
		2450.00,
		rtisPoint,
	)
	if err == nil {
		t.Fatalf("expected duplicate TDR filing error")
	}

	// Scenario 3: AC Failure in Coach (Difference of fare auto-credit)
	acClaim, err := engine.FileTDR(
		"9988776655",
		[]string{"Priya Patel"},
		"12952",
		"MMCT",
		"NDLS",
		"2026-11-03",
		ReasonACFailure,
		3000.00,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to file AC failure TDR: %v", err)
	}
	if acClaim.ApprovedRefundINR != 1050.00 { // 35% difference
		t.Fatalf("expected AC difference ₹1050.00, got ₹%.2f", acClaim.ApprovedRefundINR)
	}
	t.Logf("AC Failure difference credit verified: ₹%.2f", acClaim.ApprovedRefundINR)

	// Scenario 4: Manual review case (Coach not attached) & SLA Escalation
	manualClaim, err := engine.FileTDR(
		"4455667788",
		[]string{"Ramesh Kumar"},
		"12301",
		"HWH",
		"NDLS",
		"2026-11-04",
		ReasonCoachNotAttached,
		1800.00,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to file manual claim: %v", err)
	}
	if manualClaim.Status != StatusSubmitted {
		t.Fatalf("expected StatusSubmitted, got %s", manualClaim.Status)
	}

	// Fast-forward 5 hours to trigger SLA auto-escalation
	futureTime := time.Now().Add(5 * time.Hour)
	escalated := engine.EvaluateSLAEscalations(futureTime)
	if len(escalated) == 0 {
		t.Fatalf("expected SLA auto-escalation for overdue claim")
	}

	c, _ := engine.GetClaim(manualClaim.ClaimID)
	if c.Status != StatusEscalatedSrDCM {
		t.Fatalf("expected StatusEscalatedSrDCM, got %s", c.Status)
	}
	t.Logf("RailMadad SLA Auto-Escalation verified: Docket=%s escalated to Sr. DCM after SLA breach",
		c.RailMadadDocketID)

	// Now divisional officer approves
	reviewed, err := engine.ReviewClaim(c.ClaimID, true, 1800.00, "Coach S4 detrained at Kanpur; Approved by Sr. DCM")
	if err != nil {
		t.Fatalf("failed to review claim: %v", err)
	}
	if reviewed.Status != StatusApproved || reviewed.ApprovedRefundINR != 1800.00 {
		t.Fatalf("expected approved claim with ₹1800.00")
	}

	// Dispatch refund
	if err := engine.MarkRefundDispatched(c.ClaimID); err != nil {
		t.Fatalf("failed to dispatch refund: %v", err)
	}
	finalClaim, _ := engine.GetClaim(c.ClaimID)
	if finalClaim.Status != StatusRefundDispatched {
		t.Fatalf("expected StatusRefundDispatched, got %s", finalClaim.Status)
	}
}
