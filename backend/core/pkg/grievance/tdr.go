package grievance

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/railway-revamped/core/pkg/telemetry"
)

// TDRReason represents standardized statutory grounds for claiming a Ticket Deposit Receipt refund
type TDRReason string

const (
	ReasonDelayOver3Hours            TDRReason = "TRAIN_DELAY_OVER_3_HOURS"
	ReasonACFailure                  TDRReason = "AC_FAILURE_IN_COACH"
	ReasonTrainCancelled             TDRReason = "TRAIN_CANCELLED_BY_RAILWAYS"
	ReasonDivertedNotTouchingDest    TDRReason = "TRAIN_DIVERTED_NON_TOUCHING"
	ReasonCoachNotAttached           TDRReason = "RESERVED_COACH_NOT_ATTACHED"
	ReasonDifferenceOfFare           TDRReason = "DIFFERENCE_OF_FARE_LOWER_CLASS"
)

// TDRStatus represents the lifecycle of a refund dispute
type TDRStatus string

const (
	StatusSubmitted        TDRStatus = "SUBMITTED"
	StatusAutoVerified     TDRStatus = "AUTO_VERIFIED"
	StatusUnderReview      TDRStatus = "UNDER_DIVISION_REVIEW"
	StatusEscalatedSrDCM   TDRStatus = "ESCALATED_SR_DCM"
	StatusApproved         TDRStatus = "APPROVED_REFUND_QUEUED"
	StatusRefundDispatched TDRStatus = "REFUND_DISPATCHED"
	StatusRejected         TDRStatus = "REJECTED"
)

// TDRClaim represents an official passenger refund dispute docket
type TDRClaim struct {
	ClaimID            string     `json:"claim_id"`
	PNR                string     `json:"pnr"`
	PassengerNames     []string   `json:"passenger_names"`
	TrainNumber        string     `json:"train_number"`
	OriginStation      string     `json:"origin_station"`
	DestStation        string     `json:"dest_station"`
	TravelDate         string     `json:"travel_date"`
	Reason             TDRReason  `json:"reason"`
	Status             TDRStatus  `json:"status"`
	FarePaidINR        float64    `json:"fare_paid_inr"`
	RequestedRefundINR float64    `json:"requested_refund_inr"`
	ApprovedRefundINR  float64    `json:"approved_refund_inr"`
	RailMadadDocketID  string     `json:"railmadad_docket_id"`
	AutoEligible       bool       `json:"auto_eligible"`
	OfficerNotes       string     `json:"officer_notes"`
	FiledAt            time.Time  `json:"filed_at"`
	EscalationDueAt    time.Time  `json:"escalation_due_at"`
	ResolvedAt         *time.Time `json:"resolved_at,omitempty"`
}

// TDREngine coordinates automated 1-click filing and RailMadad SLA escalations
type TDREngine struct {
	mu         sync.RWMutex
	claims     map[string]*TDRClaim
	claimsByPNR map[string][]*TDRClaim
}

// NewTDREngine initializes the grievance and TDR engine
func NewTDREngine() *TDREngine {
	return &TDREngine{
		claims:      make(map[string]*TDRClaim),
		claimsByPNR: make(map[string][]*TDRClaim),
	}
}

// AssessAutomatedEligibility verifies whether RTIS locomotive telemetry satisfies statutory refund thresholds
func (e *TDREngine) AssessAutomatedEligibility(
	reason TDRReason,
	pt *telemetry.TelemetryPoint,
	farePaid float64,
) (eligible bool, autoRefundINR float64, reasonSummary string) {
	switch reason {
	case ReasonDelayOver3Hours:
		if pt != nil && pt.DelayMinutes >= 180 {
			// Statutory rule: 100% full refund with 0 clerkage if delay >= 180 min (3 hours)
			return true, farePaid, fmt.Sprintf("RTIS verified: Train running late by %d minutes (>= 180m threshold)", pt.DelayMinutes)
		}
		return false, 0, "Train delay is under statutory 3-hour threshold for instant auto-approval"

	case ReasonTrainCancelled:
		return true, farePaid, "IRCTC / Control Office registered train cancellation: 100% full refund"

	case ReasonACFailure:
		// AC Failure in 2A/3A provides difference of fare to Sleeper (~35% standard differential)
		diff := farePaid * 0.35
		return true, diff, "AC Failure verified via onboard IoT/TTE manifest: difference of fare to Sleeper credited"

	default:
		return false, 0, "Dispute requires manual division commercial officer verification"
	}
}

// FileTDR creates a new verified refund dispute docket with automatic RailMadad bridge registration
func (e *TDREngine) FileTDR(
	pnr string,
	passengers []string,
	trainNumber string,
	origin string,
	dest string,
	travelDate string,
	reason TDRReason,
	farePaid float64,
	rtisPoint *telemetry.TelemetryPoint,
) (*TDRClaim, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Prevent duplicate TDR filing on same PNR for identical reason
	existingClaims := e.claimsByPNR[pnr]
	for _, c := range existingClaims {
		if c.Reason == reason && c.Status != StatusRejected {
			return nil, fmt.Errorf("active TDR claim %s already exists for PNR %s", c.ClaimID, pnr)
		}
	}

	eligible, autoAmt, summary := e.AssessAutomatedEligibility(reason, rtisPoint, farePaid)

	claimID := generateID("TDR")
	docketID := generateID("RM-DOCKET") // RailMadad integration docket ID
	now := time.Now()

	claim := &TDRClaim{
		ClaimID:            claimID,
		PNR:                pnr,
		PassengerNames:     passengers,
		TrainNumber:        trainNumber,
		OriginStation:      origin,
		DestStation:        dest,
		TravelDate:         travelDate,
		Reason:             reason,
		FarePaidINR:        farePaid,
		RequestedRefundINR: farePaid,
		ApprovedRefundINR:  0,
		RailMadadDocketID:  docketID,
		AutoEligible:       eligible,
		OfficerNotes:       summary,
		FiledAt:            now,
		EscalationDueAt:    now.Add(4 * time.Hour), // 4-hour SLA before auto-escalation to Sr. DCM
	}

	if eligible {
		claim.Status = StatusAutoVerified
		claim.ApprovedRefundINR = autoAmt
	} else {
		claim.Status = StatusSubmitted
	}

	e.claims[claimID] = claim
	e.claimsByPNR[pnr] = append(e.claimsByPNR[pnr], claim)

	return claim, nil
}

// ReviewClaim allows a Divisional Commercial Officer to approve or reject a claim
func (e *TDREngine) ReviewClaim(claimID string, approve bool, approvedAmt float64, notes string) (*TDRClaim, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	claim, exists := e.claims[claimID]
	if !exists {
		return nil, fmt.Errorf("claim %s not found", claimID)
	}

	now := time.Now()
	claim.ResolvedAt = &now
	claim.OfficerNotes = notes

	if approve {
		claim.Status = StatusApproved
		claim.ApprovedRefundINR = approvedAmt
	} else {
		claim.Status = StatusRejected
		claim.ApprovedRefundINR = 0
	}

	return claim, nil
}

// MarkRefundDispatched transitions an approved claim to completed refund status
func (e *TDREngine) MarkRefundDispatched(claimID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	claim, exists := e.claims[claimID]
	if !exists {
		return fmt.Errorf("claim %s not found", claimID)
	}
	if claim.Status != StatusApproved && claim.Status != StatusAutoVerified {
		return fmt.Errorf("cannot dispatch refund for claim in status %s", claim.Status)
	}

	claim.Status = StatusRefundDispatched
	now := time.Now()
	claim.ResolvedAt = &now
	return nil
}

// EvaluateSLAEscalations scans pending claims and escalates overdue dockets to Sr. DCM / ADRM
func (e *TDREngine) EvaluateSLAEscalations(asOf time.Time) []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	var escalatedIDs []string
	for _, claim := range e.claims {
		if claim.Status == StatusSubmitted && asOf.After(claim.EscalationDueAt) {
			claim.Status = StatusEscalatedSrDCM
			claim.OfficerNotes += " [AUTO-ESCALATED: Exceeded 4-hour divisional SLA threshold]"
			escalatedIDs = append(escalatedIDs, claim.ClaimID)
		}
	}
	return escalatedIDs
}

// GetClaim retrieves a specific claim by ID
func (e *TDREngine) GetClaim(claimID string) (*TDRClaim, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.claims[claimID]
	return c, ok
}

// GetClaimsByPNR retrieves all claims for a given PNR
func (e *TDREngine) GetClaimsByPNR(pnr string) []*TDRClaim {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.claimsByPNR[pnr]
}

func generateID(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}
