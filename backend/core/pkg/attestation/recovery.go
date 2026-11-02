package attestation

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidVID = errors.New("invalid 16-digit Aadhaar Virtual ID (VID)")
	ErrInvalidOTP = errors.New("invalid or expired UIDAI verification OTP")
)

const CoolingOffDuration = 12 * time.Hour

type AccountRecoveryEngine struct {
	attestMgr *AttestationManager
}

func NewAccountRecoveryEngine(attestMgr *AttestationManager) *AccountRecoveryEngine {
	return &AccountRecoveryEngine{
		attestMgr: attestMgr,
	}
}

// InitiateDeviceRecovery begins recovery on a newly paired device, placing Tatkal bookings
// into a 12-hour cooling-off window while keeping regular General bookings active.
func (e *AccountRecoveryEngine) InitiateDeviceRecovery(
	userID string,
	aadhaarVID string,
	otp string,
	newDeviceFingerprint string,
) (*AttestedSession, error) {
	if len(aadhaarVID) != 16 {
		return nil, ErrInvalidVID
	}
	if len(otp) != 6 {
		return nil, ErrInvalidOTP
	}

	now := time.Now()
	coolingOffUntil := now.Add(CoolingOffDuration).Unix()

	session := &AttestedSession{
		UserID:            userID,
		AadhaarToken:      "rec_" + aadhaarVID[:4] + "****",
		DeviceFingerprint: newDeviceFingerprint,
		Tier:              Tier1HighEndHardware,
		VerifiedAtUnix:    now.Unix(),
		ExpiresAtUnix:     now.Add(30 * 24 * time.Hour).Unix(),
		CoolingOffUntil:   coolingOffUntil,
	}

	return session, nil
}

// EvaluateFraudRiskScore computes Multi-Dimensional Anomaly Score (MAS in [0.0, 1.0]).
// Family circles (up to 6 registered co-travelers) sharing a single UPI ID incur 0 penalty.
func EvaluateFraudRiskScore(
	session *AttestedSession,
	passengerNames []string,
	upiID string,
	recentBookingCount24h int,
) (float64, []string) {
	score := 0.0
	var flags []string

	// 1. Family Circle Exemption Check
	isFamilyMatch := true
	if len(session.FamilyCircle) > 0 {
		familySet := make(map[string]bool)
		for _, name := range session.FamilyCircle {
			familySet[name] = true
		}
		for _, p := range passengerNames {
			if !familySet[p] {
				isFamilyMatch = false
				break
			}
		}
	} else {
		isFamilyMatch = false
	}

	// If legitimate registered family, zero penalty for shared UPI
	if isFamilyMatch {
		return 0.05, []string{"FAMILY_CIRCLE_VERIFIED"}
	}

	// 2. High-velocity multi-name tout pattern
	if recentBookingCount24h > 12 {
		score += 0.45
		flags = append(flags, fmt.Sprintf("HIGH_VELOCITY_24H (%d bookings)", recentBookingCount24h))
	}

	// 3. Unrelated passenger name entropy
	if len(passengerNames) >= 4 && !isFamilyMatch {
		score += 0.20
		flags = append(flags, "UNREGISTERED_NAMES_ON_SHARED_PAYMENT")
	}

	if score > 1.0 {
		score = 1.0
	}

	return score, flags
}
