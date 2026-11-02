package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/railway-revamped/core/pkg/lease"
)

type WebhookCommitAction string

const (
	ActionCommitPNR       WebhookCommitAction = "COMMIT_PNR"
	ActionRejectAndRefund WebhookCommitAction = "REJECT_AND_REFUND"
	ActionIgnoreFailed    WebhookCommitAction = "IGNORE_FAILED"
)

type PaymentOrchestrator struct {
	mu           sync.RWMutex
	leaseMgr     lease.SegmentLeaseManager
	dispatcher   *RefundDispatcher
	transactions map[string]*PaymentTransaction // txnID -> txn
}

func NewPaymentOrchestrator(leaseMgr lease.SegmentLeaseManager, dispatcher *RefundDispatcher) *PaymentOrchestrator {
	return &PaymentOrchestrator{
		leaseMgr:     leaseMgr,
		dispatcher:   dispatcher,
		transactions: make(map[string]*PaymentTransaction),
	}
}

// InitiateHoldAndAuthorize creates a pre-authorization hold tied to an active segment lease.
func (p *PaymentOrchestrator) InitiateHoldAndAuthorize(
	ctx context.Context,
	userID string,
	trainNumber string,
	runDate string,
	leaseToken string,
	amountINR float64,
	gateway PaymentGateway,
) (*PaymentTransaction, error) {
	// 1. Verify lease is active
	meta, err := p.leaseMgr.GetLease(ctx, trainNumber, runDate, leaseToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLeaseToken, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	txnID := "txn_" + hex.EncodeToString(tokenBytes)
	intentToken := "pi_" + hex.EncodeToString(tokenBytes[:8])

	txn := &PaymentTransaction{
		TxnID:              txnID,
		UserID:             userID,
		TrainNumber:        trainNumber,
		RunDate:            runDate,
		LeaseToken:         leaseToken,
		AmountINR:          amountINR,
		Gateway:            gateway,
		Status:             TxnPreAuthInit,
		PaymentIntentToken: intentToken,
		CreatedAt:          now,
		UpdatedAt:          now,
		LeaseExpiresAt:     time.Unix(meta.ExpiresAt, 0),
	}

	p.transactions[txnID] = txn
	return txn, nil
}

// HandleWebhookCallback processes gateway callback within the 120s webhook window.
func (p *PaymentOrchestrator) HandleWebhookCallback(ctx context.Context, event WebhookEvent) (WebhookCommitAction, *PaymentTransaction, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	txn, exists := p.transactions[event.TxnID]
	if !exists {
		return "", nil, ErrTransactionNotFound
	}

	now := time.Now()
	txn.UpdatedAt = now
	txn.GatewayTxnID = event.GatewayTxnID

	// If gateway reported explicit failure
	if event.Status == "FAILED" {
		txn.Status = TxnFailed
		return ActionIgnoreFailed, txn, nil
	}

	// 2. Validate Segment Lease validity
	meta, err := p.leaseMgr.GetLease(ctx, txn.TrainNumber, txn.RunDate, txn.LeaseToken)
	if err != nil || meta == nil || meta.ExpiresAt <= now.Unix() {
		// CRITICAL SAFETY INVARIANT: Money debited after lease expired!
		// Pre-auth void or automatic refund MUST be triggered immediately.
		txn.Status = TxnVoided
		log.Printf("[PAYMENT-ORCHESTRATOR] SAFETY TRIGGERED: Late webhook for expired lease %s! Voiding payment / Queuing refund.",
			txn.LeaseToken)

		// Auto-queue refund reversal
		_, _ = p.dispatcher.DispatchRefund(ctx, txn, RefundSourceBank, txn.AmountINR, "Automatic reversal: segment lease expired before payment webhook")

		return ActionRejectAndRefund, txn, ErrLeaseExpiredOnWebhook
	}

	// 3. Normal Path: Lease is valid! Capture payment and commit PNR
	txn.Status = TxnCaptured
	log.Printf("[PAYMENT-ORCHESTRATOR] Payment ₹%.2f captured successfully for lease %s. Authorizing PNR commit.",
		txn.AmountINR, txn.LeaseToken)

	return ActionCommitPNR, txn, nil
}

func (p *PaymentOrchestrator) GetTransaction(txnID string) (*PaymentTransaction, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	txn, exists := p.transactions[txnID]
	if !exists {
		return nil, ErrTransactionNotFound
	}
	cpy := *txn
	return &cpy, nil
}

func (p *PaymentOrchestrator) GetPendingTransactions(staleDuration time.Duration) []*PaymentTransaction {
	p.mu.RLock()
	defer p.mu.RUnlock()

	now := time.Now()
	var pending []*PaymentTransaction
	for _, txn := range p.transactions {
		if txn.Status == TxnPreAuthInit || txn.Status == TxnAuthorized {
			if now.Sub(txn.CreatedAt) >= staleDuration {
				cpy := *txn
				pending = append(pending, &cpy)
			}
		}
	}
	return pending
}
