package payment

import (
	"context"
	"log"
	"sync"
	"time"
)

type GatewayStatusChecker func(ctx context.Context, txn *PaymentTransaction) (string, error) // returns "SUCCESS", "FAILED", "NOT_FOUND"

type ReconciliationPoller struct {
	orchestrator   *PaymentOrchestrator
	dispatcher     *RefundDispatcher
	interval       time.Duration
	staleThreshold time.Duration
	statusChecker  GatewayStatusChecker
	stopCh         chan struct{}
	wg             sync.WaitGroup
}

func NewReconciliationPoller(
	orchestrator *PaymentOrchestrator,
	dispatcher *RefundDispatcher,
	interval time.Duration,
	statusChecker GatewayStatusChecker,
) *ReconciliationPoller {
	if interval <= 0 {
		interval = 60 * time.Second // Standard 60-second poller
	}
	if statusChecker == nil {
		// Default mock status checker
		statusChecker = func(ctx context.Context, txn *PaymentTransaction) (string, error) {
			return "SUCCESS", nil
		}
	}

	return &ReconciliationPoller{
		orchestrator:   orchestrator,
		dispatcher:     dispatcher,
		interval:       interval,
		staleThreshold: 90 * time.Second,
		statusChecker:  statusChecker,
		stopCh:         make(chan struct{}),
	}
}

func (r *ReconciliationPoller) Start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stopCh:
				return
			case <-ticker.C:
				r.ExecuteReconciliationCycle(ctx)
			}
		}
	}()
}

func (r *ReconciliationPoller) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

// ExecuteReconciliationCycle sweeps hanging transactions older than 90 seconds.
func (r *ReconciliationPoller) ExecuteReconciliationCycle(ctx context.Context) int {
	pending := r.orchestrator.GetPendingTransactions(r.staleThreshold)
	if len(pending) == 0 {
		return 0
	}

	log.Printf("[RECONCILER] Scanning %d stale pending transactions older than %v",
		len(pending), r.staleThreshold)

	resolvedCount := 0
	for _, txn := range pending {
		gatewayStatus, err := r.statusChecker(ctx, txn)
		if err != nil {
			log.Printf("[RECONCILER] Error checking status for txn %s: %v", txn.TxnID, err)
			continue
		}

		event := WebhookEvent{
			TxnID:        txn.TxnID,
			GatewayTxnID: "rec_sync_" + txn.TxnID,
			Gateway:      txn.Gateway,
			Status:       gatewayStatus,
			AmountINR:    txn.AmountINR,
		}

		action, _, err := r.orchestrator.HandleWebhookCallback(ctx, event)
		log.Printf("[RECONCILER] Resolved txn %s with Action: %s (err: %v)", txn.TxnID, action, err)
		resolvedCount++
	}

	return resolvedCount
}
