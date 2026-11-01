package crypto

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidSignature = errors.New("ticket ed25519 cryptographic signature verification failed")
	ErrExpiredQRWindow  = errors.New("dynamic ticket qr code window expired (replay or screenshot resale attempt)")
)

type TicketPayload struct {
	PNRNumber     string `json:"pnr"`
	TrainNumber   string `json:"train"`
	RunDate       string `json:"date"`
	Origin        string `json:"orig"`
	Destination   string `json:"dest"`
	ClassCode     string `json:"cls"`
	CoachID       string `json:"coach"`
	BerthNumber   int    `json:"berth"`
	PassengerName string `json:"name"`
	IssueTimeUnix int64  `json:"ts"`
	TimeWindow    int64  `json:"win"` // dynamic 60s window: unix / 60
}

type SignedTicketQR struct {
	PayloadB64   string `json:"p"`
	SignatureB64 string `json:"s"`
}

type SovereignTicketAuthority struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	hmacSecret []byte
}

func GenerateSovereignKeypair(hmacSecret []byte) (*SovereignTicketAuthority, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 keypair: %w", err)
	}
	if len(hmacSecret) == 0 {
		hmacSecret = make([]byte, 32)
		if _, err := rand.Read(hmacSecret); err != nil {
			return nil, err
		}
	}
	return &SovereignTicketAuthority{
		publicKey:  pub,
		privateKey: priv,
		hmacSecret: hmacSecret,
	}, nil
}

func NewTicketVerifier(publicKey ed25519.PublicKey) *SovereignTicketAuthority {
	return &SovereignTicketAuthority{
		publicKey: publicKey,
	}
}

func (a *SovereignTicketAuthority) PublicKey() ed25519.PublicKey {
	return a.publicKey
}

// IssueSignedTicketQR generates a signed offline ticket with dynamic 60s rotation.
func (a *SovereignTicketAuthority) IssueSignedTicketQR(ticket TicketPayload) (string, error) {
	if a.privateKey == nil {
		return "", errors.New("cannot issue ticket without sovereign private key (only available in central server)")
	}

	now := time.Now().Unix()
	if ticket.IssueTimeUnix == 0 {
		ticket.IssueTimeUnix = now
	}
	if ticket.TimeWindow == 0 {
		ticket.TimeWindow = now / 60 // 60s dynamic rotation window
	}

	payloadBytes, err := json.Marshal(ticket)
	if err != nil {
		return "", err
	}

	sig := ed25519.Sign(a.privateKey, payloadBytes)

	qrEnvelope := SignedTicketQR{
		PayloadB64:   base64.URLEncoding.EncodeToString(payloadBytes),
		SignatureB64: base64.URLEncoding.EncodeToString(sig),
	}

	qrBytes, err := json.Marshal(qrEnvelope)
	if err != nil {
		return "", err
	}

	return string(qrBytes), nil
}

// VerifyTicketQROffline validates a ticket on an offline TTE terminal with ONLY the public key.
// tolerantWindowDelta allows up to +/- 1 window (e.g. 60-120s clock skew) for live scanning,
// or -1 to skip time window check (for printed static paper backup tickets).
func (a *SovereignTicketAuthority) VerifyTicketQROffline(qrRaw string, tolerantWindowDelta int) (*TicketPayload, error) {
	var envelope SignedTicketQR
	if err := json.Unmarshal([]byte(qrRaw), &envelope); err != nil {
		return nil, fmt.Errorf("malformed qr envelope: %w", err)
	}

	payloadBytes, err := base64.URLEncoding.DecodeString(envelope.PayloadB64)
	if err != nil {
		return nil, fmt.Errorf("invalid payload base64: %w", err)
	}

	sigBytes, err := base64.URLEncoding.DecodeString(envelope.SignatureB64)
	if err != nil {
		return nil, fmt.Errorf("invalid signature base64: %w", err)
	}

	// 1. Verify asymmetric signature against public key
	if !ed25519.Verify(a.publicKey, payloadBytes, sigBytes) {
		return nil, ErrInvalidSignature
	}

	var ticket TicketPayload
	if err := json.Unmarshal(payloadBytes, &ticket); err != nil {
		return nil, fmt.Errorf("failed to parse ticket payload: %w", err)
	}

	// 2. Dynamic time-window anti-replay check
	if tolerantWindowDelta >= 0 {
		currentWindow := time.Now().Unix() / 60
		diff := currentWindow - ticket.TimeWindow
		if diff < -int64(tolerantWindowDelta) || diff > int64(tolerantWindowDelta) {
			return nil, fmt.Errorf("%w: ticket window=%d, current=%d (skew=%d min)", ErrExpiredQRWindow, ticket.TimeWindow, currentWindow, diff)
		}
	}

	return &ticket, nil
}

// TokenizeAadhaar computes an irreversible pseudonymized HMAC-SHA256 token for DPDP compliance.
// Plaintext Aadhaar number is NEVER stored anywhere in the database.
func (a *SovereignTicketAuthority) TokenizeAadhaar(aadhaarNumber string) string {
	mac := hmac.New(sha256.New, a.hmacSecret)
	mac.Write([]byte(aadhaarNumber))
	return hex.EncodeToString(mac.Sum(nil))
}
