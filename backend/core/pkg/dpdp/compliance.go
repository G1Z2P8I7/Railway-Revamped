package dpdp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// RequestType represents citizen privacy rights under the DPDP Act 2023 & Rules 2025
type RequestType string

const (
	RequestDataExport       RequestType = "DATA_PORTABILITY_EXPORT"
	RequestRightToErasure   RequestType = "RIGHT_TO_ERASURE"
	RequestConsentRevocation RequestType = "CONSENT_REVOCATION"
)

// RequestStatus tracks the statutory processing lifecycle
type RequestStatus string

const (
	StatusPending    RequestStatus = "PENDING_VERIFICATION"
	StatusProcessing RequestStatus = "PROCESSING"
	StatusCompleted  RequestStatus = "COMPLETED"
	StatusRejected   RequestStatus = "REJECTED"
)

// ConsentSettings models granular citizen consent toggles
type ConsentSettings struct {
	TransactionalAlerts     bool      `json:"transactional_alerts"` // Non-revocable safety transit alerts
	MarketingPromotionsEmail bool      `json:"marketing_promotions_email"`
	MarketingPromotionsSMS   bool      `json:"marketing_promotions_sms"`
	ThirdPartyAnalytics     bool      `json:"third_party_analytics"`
	ConsentVersion          string    `json:"consent_version"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// UserProfile represents the active customer account directory
type UserProfile struct {
	UserID            string          `json:"user_id"`
	FullName          string          `json:"full_name"`
	Phone             string          `json:"phone"`
	Email             string          `json:"email"`
	AadhaarHMACToken  string          `json:"aadhaar_hmac_token"` // Plaintext Aadhaar NEVER stored (0s retention)
	Consent           ConsentSettings `json:"consent"`
	IsErased          bool            `json:"is_erased"`
	ErasedAt          *time.Time      `json:"erased_at,omitempty"`
}

// HistoricalTravelRecord represents cold-archived completed journeys
type HistoricalTravelRecord struct {
	PNR              string     `json:"pnr"`
	UserID           string     `json:"user_id"`
	TrainNumber      string     `json:"train_number"`
	TravelDate       time.Time  `json:"travel_date"`
	EncryptedPII     string     `json:"encrypted_pii"` // AES-256-GCM encrypted
	IsPIIAnonymized  bool       `json:"is_pii_anonymized"`
	AnonymizedAt     *time.Time `json:"anonymized_at,omitempty"`
}

// PrivacyRequest represents a formal citizen request under Section 12 of the DPDP Act
type PrivacyRequest struct {
	RequestID    string        `json:"request_id"`
	UserID       string        `json:"user_id"`
	Type         RequestType   `json:"type"`
	Status       RequestStatus `json:"status"`
	SubmittedAt  time.Time     `json:"submitted_at"`
	SLADueAt     time.Time     `json:"sla_due_at"` // 72-hour statutory SLA
	CompletedAt  *time.Time    `json:"completed_at,omitempty"`
	DownloadURL  string        `json:"download_url,omitempty"`
	AuditLog     string        `json:"audit_log"`
}

// ComplianceEngine orchestrates citizen privacy rights, automated erasure, and retention boundaries
type ComplianceEngine struct {
	mu           sync.RWMutex
	aesKey       []byte // 256-bit sovereign encryption key
	profiles     map[string]*UserProfile
	travelRecords map[string]*HistoricalTravelRecord
	requests     map[string]*PrivacyRequest
}

// NewComplianceEngine initializes the DPDP compliance engine with an AES-256 sovereign key
func NewComplianceEngine(aesKey []byte) (*ComplianceEngine, error) {
	if len(aesKey) != 32 {
		// Generate random 256-bit key if not provided
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		aesKey = key
	}

	return &ComplianceEngine{
		aesKey:        aesKey,
		profiles:      make(map[string]*UserProfile),
		travelRecords: make(map[string]*HistoricalTravelRecord),
		requests:      make(map[string]*PrivacyRequest),
	}, nil
}

// RegisterProfile stores an active user profile (validating zero plaintext Aadhaar storage)
func (e *ComplianceEngine) RegisterProfile(p *UserProfile) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(p.AadhaarHMACToken) < 32 {
		return fmt.Errorf("security violation: invalid or un-tokenized Aadhaar identifier")
	}

	p.Consent.TransactionalAlerts = true // Mandatory for safety & itinerary
	p.Consent.UpdatedAt = time.Now()
	p.Consent.ConsentVersion = "DPDP-V2-2026"
	e.profiles[p.UserID] = p
	return nil
}

// ArchiveCompletedJourney encrypts and retains travel history for 12 months under Railways Act 1989
func (e *ComplianceEngine) ArchiveCompletedJourney(pnr string, userID string, train string, travelDate time.Time, piiData map[string]interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	plaintext, err := json.Marshal(piiData)
	if err != nil {
		return err
	}

	encryptedHex, err := e.encryptAESGCM(plaintext)
	if err != nil {
		return err
	}

	record := &HistoricalTravelRecord{
		PNR:             pnr,
		UserID:          userID,
		TrainNumber:     train,
		TravelDate:      travelDate,
		EncryptedPII:    encryptedHex,
		IsPIIAnonymized: false,
	}

	e.travelRecords[pnr] = record
	return nil
}

// SubmitPrivacyRequest files a citizen data subject access or erasure request
func (e *ComplianceEngine) SubmitPrivacyRequest(userID string, reqType RequestType) (*PrivacyRequest, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	profile, exists := e.profiles[userID]
	if !exists || profile.IsErased {
		return nil, fmt.Errorf("user profile %s not found or already erased", userID)
	}

	reqID := fmt.Sprintf("DPDP-%d-%s", time.Now().Unix(), userID)
	now := time.Now()

	req := &PrivacyRequest{
		RequestID:   reqID,
		UserID:      userID,
		Type:        reqType,
		Status:      StatusPending,
		SubmittedAt: now,
		SLADueAt:    now.Add(72 * time.Hour), // 72-Hour DPDP SLA
		AuditLog:    fmt.Sprintf("Request %s registered under DPDP Act 2023", reqType),
	}

	e.requests[reqID] = req
	return req, nil
}

// ExecuteRightToErasure executes the 72-hour right-to-erasure pipeline
func (e *ComplianceEngine) ExecuteRightToErasure(requestID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	req, exists := e.requests[requestID]
	if !exists {
		return fmt.Errorf("request %s not found", requestID)
	}
	if req.Type != RequestRightToErasure {
		return fmt.Errorf("request %s is not an erasure request", requestID)
	}

	profile, exists := e.profiles[req.UserID]
	if !exists {
		return fmt.Errorf("user profile %s not found", req.UserID)
	}

	now := time.Now()

	// 1. Scrub active profile data (Right-to-Erasure scope)
	profile.FullName = "ERASED_DATA_PRINCIPAL"
	profile.Email = fmt.Sprintf("erased_%d@railway.local", now.Unix())
	profile.Phone = "0000000000"
	profile.Consent.MarketingPromotionsEmail = false
	profile.Consent.MarketingPromotionsSMS = false
	profile.Consent.ThirdPartyAnalytics = false
	profile.IsErased = true
	profile.ErasedAt = &now

	// 2. Completed journeys < 12 months old remain in encrypted cold storage for statutory audit
	// Journeys >= 12 months old have their encrypted PII permanently scrubbed
	for _, rec := range e.travelRecords {
		if rec.UserID == req.UserID {
			if now.Sub(rec.TravelDate) >= 365*24*time.Hour {
				rec.EncryptedPII = "[SCRUBBED_POST_12_MONTHS_STATUTORY_RETENTION]"
				rec.IsPIIAnonymized = true
				rec.AnonymizedAt = &now
			}
		}
	}

	req.Status = StatusCompleted
	req.CompletedAt = &now
	req.AuditLog += " | Active profile scrubbed within 72h SLA. Marketing consent revoked. Completed journeys audited against statutory 12-month retention rule."

	return nil
}

// GenerateDataPortabilityExport compiles all citizen data into a structured portable JSON payload
func (e *ComplianceEngine) GenerateDataPortabilityExport(requestID string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	req, exists := e.requests[requestID]
	if !exists {
		return nil, fmt.Errorf("request %s not found", requestID)
	}

	profile, exists := e.profiles[req.UserID]
	if !exists {
		return nil, fmt.Errorf("user profile not found")
	}

	var userJourneys []HistoricalTravelRecord
	for _, rec := range e.travelRecords {
		if rec.UserID == req.UserID {
			userJourneys = append(userJourneys, *rec)
		}
	}

	exportPayload := map[string]interface{}{
		"export_metadata": map[string]interface{}{
			"docket_id":         req.RequestID,
			"statutory_act":     "Digital Personal Data Protection Act, 2023 (DPDP)",
			"export_timestamp":  time.Now(),
			"sovereign_region":  "India (MeitY Empanelled Core)",
		},
		"profile": profile,
		"consent_framework": profile.Consent,
		"travel_history_records": userJourneys,
	}

	data, err := json.MarshalIndent(exportPayload, "", "  ")
	if err != nil {
		return nil, err
	}

	now := time.Now()
	req.Status = StatusCompleted
	req.CompletedAt = &now
	req.DownloadURL = fmt.Sprintf("https://railway.internal/v1/privacy/download/%s", req.RequestID)
	req.AuditLog += " | Data portability JSON package generated."

	return data, nil
}

// UpdateConsent allows granular updates to marketing and tracking preferences
func (e *ComplianceEngine) UpdateConsent(userID string, consent ConsentSettings) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	profile, exists := e.profiles[userID]
	if !exists || profile.IsErased {
		return fmt.Errorf("user profile %s not found or erased", userID)
	}

	consent.TransactionalAlerts = true // Enforce non-revocable safety communications
	consent.UpdatedAt = time.Now()
	consent.ConsentVersion = "DPDP-V2-2026"
	profile.Consent = consent
	return nil
}

// EnforceRetentionPolicy runs periodically to scrub journey PII exceeding 12 months
func (e *ComplianceEngine) EnforceRetentionPolicy(asOf time.Time) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	scrubbedCount := 0
	for _, rec := range e.travelRecords {
		if !rec.IsPIIAnonymized && asOf.Sub(rec.TravelDate) >= 365*24*time.Hour {
			rec.EncryptedPII = "[SCRUBBED_POST_12_MONTHS_STATUTORY_RETENTION]"
			rec.IsPIIAnonymized = true
			now := asOf
			rec.AnonymizedAt = &now
			scrubbedCount++
		}
	}
	return scrubbedCount
}

func (e *ComplianceEngine) encryptAESGCM(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.aesKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return hex.EncodeToString(ciphertext), nil
}
