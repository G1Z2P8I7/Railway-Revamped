package payment

import (
	"context"
	"testing"
	"time"

	"github.com/railway-revamped/core/pkg/lease"
)

func TestTwoPhaseHoldAndAuthorizeHappyPath(t *testing.T) {
	ctx := context.Background()
	leaseMgr := lease.NewInMemoryLeaseManager()
	leaseMgr.SetInitialInventory("12301", "20261105", "3A", "GN", 10)

	ledgerMgr := NewTransitLedgerManager()
	dispatcher := NewRefundDispatcher(ledgerMgr)
	orchestrator := NewPaymentOrchestrator(leaseMgr, dispatcher)

	// 1. Acquire active lease
	leaseReq := lease.AcquireLeaseRequest{
		UserID:         "user-buyer",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		PassengerCount: 2,
		TTLSeconds:     300,
	}
	leaseRes, err := leaseMgr.AcquireLease(ctx, leaseReq)
	if err != nil {
		t.Fatalf("lease acquire failed: %v", err)
	}

	// 2. Initiate Hold-and-Authorize
	txn, err := orchestrator.InitiateHoldAndAuthorize(
		ctx,
		"user-buyer",
		"12301",
		"20261105",
		leaseRes.LeaseToken,
		4300.00,
		GatewayRazorpay,
	)
	if err != nil {
		t.Fatalf("initiate payment failed: %v", err)
	}
	if txn.Status != TxnPreAuthInit {
		t.Fatalf("expected PRE_AUTH_INIT, got: %s", txn.Status)
	}

	// 3. Gateway Webhook callback arrives (Success) within 120s window
	event := WebhookEvent{
		TxnID:        txn.TxnID,
		GatewayTxnID: "pay_rzp_ext_77812",
		Gateway:      GatewayRazorpay,
		Status:       "SUCCESS",
		AmountINR:    4300.00,
	}
	action, updatedTxn, err := orchestrator.HandleWebhookCallback(ctx, event)
	if err != nil {
		t.Fatalf("webhook callback failed: %v", err)
	}
	if action != ActionCommitPNR {
		t.Fatalf("expected ActionCommitPNR, got: %s", action)
	}
	if updatedTxn.Status != TxnCaptured {
		t.Fatalf("expected CAPTURED status, got: %s", updatedTxn.Status)
	}
}

func TestLateWebhookRejectionAndAutoRefund(t *testing.T) {
	ctx := context.Background()
	leaseMgr := lease.NewInMemoryLeaseManager()
	leaseMgr.SetInitialInventory("12301", "20261105", "3A", "GN", 5)

	ledgerMgr := NewTransitLedgerManager()
	dispatcher := NewRefundDispatcher(ledgerMgr)
	orchestrator := NewPaymentOrchestrator(leaseMgr, dispatcher)

	// 1. Acquire short lease (TTL = 1s)
	leaseReq := lease.AcquireLeaseRequest{
		UserID:         "user-late-webhook",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		PassengerCount: 1,
		TTLSeconds:     1, // 1 second TTL
	}
	leaseRes, err := leaseMgr.AcquireLease(ctx, leaseReq)
	if err != nil {
		t.Fatal(err)
	}

	txn, err := orchestrator.InitiateHoldAndAuthorize(
		ctx,
		"user-late-webhook",
		"12301",
		"20261105",
		leaseRes.LeaseToken,
		2150.00,
		GatewaySBIEpay,
	)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Simulate 2 seconds elapsing -> lease expires and is swept
	time.Sleep(2 * time.Second)
	_, _ = leaseMgr.SweepExpiredLeases(ctx, "12301", "20261105", time.Now().Unix())

	// 3. Late webhook callback arrives AFTER lease expiration
	event := WebhookEvent{
		TxnID:        txn.TxnID,
		GatewayTxnID: "sbi_late_hook_9918",
		Gateway:      GatewaySBIEpay,
		Status:       "SUCCESS",
		AmountINR:    2150.00,
	}
	action, updatedTxn, err := orchestrator.HandleWebhookCallback(ctx, event)
	if action != ActionRejectAndRefund {
		t.Fatalf("expected ActionRejectAndRefund on expired lease, got: %s", action)
	}
	if err != ErrLeaseExpiredOnWebhook {
		t.Fatalf("expected ErrLeaseExpiredOnWebhook, got: %v", err)
	}
	if updatedTxn.Status != TxnRefundSettling && updatedTxn.Status != TxnVoided {
		t.Fatalf("expected payment to be VOIDED or REFUND_SETTLING, got: %s", updatedTxn.Status)
	}
	if updatedTxn.RefundDispatchedAt == nil {
		t.Fatalf("expected automated refund reversal instruction to be dispatched immediately")
	}

	t.Logf("Safety Invariant Passed: Late webhook rejected with Action=%s, Status=%s, RefundChannel=%s",
		action, updatedTxn.Status, updatedTxn.RefundChannel)
}

func TestReconciliationPollerSweepsDroppedWebhook(t *testing.T) {
	ctx := context.Background()
	leaseMgr := lease.NewInMemoryLeaseManager()
	leaseMgr.SetInitialInventory("12301", "20261105", "3A", "GN", 10)

	ledgerMgr := NewTransitLedgerManager()
	dispatcher := NewRefundDispatcher(ledgerMgr)
	orchestrator := NewPaymentOrchestrator(leaseMgr, dispatcher)

	// Acquire lease
	leaseReq := lease.AcquireLeaseRequest{
		UserID:         "user-dropped-webhook",
		TrainNumber:    "12301",
		RunDate:        "20261105",
		ClassCode:      "3A",
		QuotaCode:      "GN",
		PassengerCount: 1,
		TTLSeconds:     300,
	}
	leaseRes, _ := leaseMgr.AcquireLease(ctx, leaseReq)

	txn, _ := orchestrator.InitiateHoldAndAuthorize(
		ctx,
		"user-dropped-webhook",
		"12301",
		"20261105",
		leaseRes.LeaseToken,
		1850.00,
		GatewayHDFC,
	)

	// Simulate dropped webhook: artificially age transaction past stale threshold (e.g. 100s ago)
	txn.CreatedAt = time.Now().Add(-100 * time.Second)

	// Mock gateway status checker confirming user paid at bank
	mockChecker := func(ctx context.Context, t *PaymentTransaction) (string, error) {
		return "SUCCESS", nil
	}

	poller := NewReconciliationPoller(orchestrator, dispatcher, 1*time.Second, mockChecker)
	resolvedCount := poller.ExecuteReconciliationCycle(ctx)

	if resolvedCount != 1 {
		t.Fatalf("expected reconciler to resolve 1 stale transaction, got: %d", resolvedCount)
	}

	updated, _ := orchestrator.GetTransaction(txn.TxnID)
	if updated.Status != TxnCaptured {
		t.Fatalf("expected reconciler to capture transaction, got: %s", updated.Status)
	}

	t.Logf("Reconciler successfully recovered dropped webhook for txn %s!", txn.TxnID)
}

func TestDualRefundMechanism(t *testing.T) {
	ctx := context.Background()
	ledgerMgr := NewTransitLedgerManager()
	dispatcher := NewRefundDispatcher(ledgerMgr)

	// 1. Test Option A: Instant Transit Ledger (< 5 seconds)
	txnLedger := &PaymentTransaction{
		TxnID:     "txn_instant_test",
		UserID:    "user-sunita",
		AmountINR: 1500.00,
		Gateway:   GatewayRazorpay,
		Status:    TxnCaptured,
	}

	resLedger, err := dispatcher.DispatchRefund(ctx, txnLedger, RefundTransitLedger, 1500.00, "Voluntary cancellation")
	if err != nil {
		t.Fatalf("transit ledger refund failed: %v", err)
	}
	if resLedger.Status != TxnRefunded || resLedger.RefundAmountINR != 1500.00 {
		t.Fatalf("unexpected ledger refund result: %+v", resLedger)
	}

	// Balance should be immediately available for rebooking
	bal := ledgerMgr.GetBalance("user-sunita")
	if bal != 1500.00 {
		t.Fatalf("expected ledger balance ₹1500.00, got: ₹%.2f", bal)
	}

	// Rebooking debit test
	debitEntry, err := ledgerMgr.DebitForBooking("user-sunita", "txn_rebook_01", 1200.00)
	if err != nil || debitEntry.AmountINR != -1200.00 {
		t.Fatalf("debit failed: %v", err)
	}
	if ledgerMgr.GetBalance("user-sunita") != 300.00 {
		t.Fatalf("expected remaining balance ₹300.00")
	}

	// 2. Test Option B: Bank Source Reversal (< 30 seconds dispatch)
	txnBank := &PaymentTransaction{
		TxnID:     "txn_bank_test",
		UserID:    "user-priya",
		AmountINR: 2800.00,
		Gateway:   GatewaySBIEpay,
		Status:    TxnCaptured,
	}

	resBank, err := dispatcher.DispatchRefund(ctx, txnBank, RefundSourceBank, 2800.00, "Delayed train > 3 hours")
	if err != nil {
		t.Fatalf("bank reversal dispatch failed: %v", err)
	}
	if resBank.Status != TxnRefundSettling || resBank.GatewayRefundRef == "" {
		t.Fatalf("unexpected bank reversal result: %+v", resBank)
	}

	t.Logf("Dual Refund Verified: Instant Ledger=₹%.2f (<5s), Bank Reversal Ref=%s (<30s dispatch)",
		bal, resBank.GatewayRefundRef)
}
