package payment

import (
	"errors"
	"time"
)

var (
	ErrInvalidLeaseToken     = errors.New("cannot initiate payment without an active, non-expired segment lease")
	ErrLeaseExpiredOnWebhook = errors.New("segment lease expired before payment authorization arrived: voiding payment")
	ErrTransactionNotFound   = errors.New("payment transaction not found")
	ErrInvalidGatewayWebhook = errors.New("invalid or unverified gateway webhook payload")
	ErrInsufficientLedgerBal = errors.New("insufficient transit ledger balance")
)

type TxnStatus string

const (
	TxnPreAuthInit    TxnStatus = "PRE_AUTH_INIT"
	TxnAuthorized     TxnStatus = "AUTHORIZED"
	TxnCaptured       TxnStatus = "CAPTURED"
	TxnFailed         TxnStatus = "FAILED"
	TxnVoided         TxnStatus = "VOIDED"
	TxnRefundQueued   TxnStatus = "REFUND_QUEUED"
	TxnRefundSettling TxnStatus = "REFUND_SETTLING_BANK"
	TxnRefunded       TxnStatus = "REFUNDED"
	TxnExpired        TxnStatus = "EXPIRED"
)

type PaymentGateway string

const (
	GatewayMockDirect PaymentGateway = "MOCK_GATEWAY"
	GatewayRazorpay   PaymentGateway = "RAZORPAY"
	GatewaySBIEpay    PaymentGateway = "SBI_EPAY"
	GatewayHDFC       PaymentGateway = "HDFC_PG"
	GatewayCashfree   PaymentGateway = "CASHFREE"
)

type RefundChannel string

const (
	RefundTransitLedger RefundChannel = "TRANSIT_LEDGER" // Instant (<5s)
	RefundSourceBank    RefundChannel = "SOURCE_BANK"    // Dispatched <30s, settles 2-4h UPI
)

type PaymentTransaction struct {
	TxnID              string         `json:"txn_id"`
	UserID             string         `json:"user_id"`
	TrainNumber        string         `json:"train_number"`
	RunDate            string         `json:"run_date"`
	LeaseToken         string         `json:"lease_token"`
	AmountINR          float64        `json:"amount_inr"`
	Gateway            PaymentGateway `json:"gateway"`
	GatewayTxnID       string         `json:"gateway_txn_id"`
	Status             TxnStatus      `json:"status"`
	PaymentIntentToken string         `json:"payment_intent_token"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	LeaseExpiresAt     time.Time      `json:"lease_expires_at"`
	RefundChannel      RefundChannel  `json:"refund_channel,omitempty"`
	RefundDispatchedAt *time.Time     `json:"refund_dispatched_at,omitempty"`
	RefundAmountINR    float64        `json:"refund_amount_inr,omitempty"`
}

type TransitLedgerAccount struct {
	UserID      string                `json:"user_id"`
	BalanceINR  float64               `json:"balance_inr"`
	LastUpdated time.Time             `json:"last_updated"`
	Entries     []TransitLedgerEntry  `json:"entries"`
}

type TransitLedgerEntry struct {
	EntryID     string    `json:"entry_id"`
	UserID      string    `json:"user_id"`
	TxnID       string    `json:"txn_id"`
	AmountINR   float64   `json:"amount_inr"`
	EntryType   string    `json:"entry_type"` // "INSTANT_REFUND", "BOOKING_DEBIT", "TOPUP"
	Note        string    `json:"note"`
	Timestamp   time.Time `json:"timestamp"`
}

type WebhookEvent struct {
	GatewayTxnID string         `json:"gateway_txn_id"`
	TxnID        string         `json:"txn_id"`
	Gateway      PaymentGateway `json:"gateway"`
	Status       string         `json:"status"` // "SUCCESS", "FAILED"
	AmountINR    float64        `json:"amount_inr"`
	Signature    string         `json:"signature"`
}
