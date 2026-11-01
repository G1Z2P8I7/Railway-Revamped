package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/railway-revamped/core/pkg/bitmask"
	"github.com/railway-revamped/core/pkg/crypto"
	"github.com/railway-revamped/core/pkg/lease"
	"github.com/railway-revamped/core/pkg/solver"
)

type Server struct {
	leaseMgr   *lease.InMemoryLeaseManager
	sweeper    *lease.SweeperDaemon
	solver     *solver.SpatialFamilySolver
	cryptoAuth *crypto.SovereignTicketAuthority

	// In-memory PNR registry for fast demo and testing
	pnrMu   sync.RWMutex
	pnrMap  map[string]*PNRRecord
	coaches map[string][]*solver.CoachLayout // train:date -> coach layouts
}

type PNRRecord struct {
	PNRNumber       string                   `json:"pnr_number"`
	TrainNumber     string                   `json:"train_number"`
	TrainName       string                   `json:"train_name"`
	RunDate         string                   `json:"run_date"`
	ClassCode       string                   `json:"class_code"`
	QuotaCode       string                   `json:"quota_code"`
	OriginStopIdx   int                      `json:"origin_stop_index"`
	DestStopIdx     int                      `json:"dest_stop_index"`
	Status          string                   `json:"status"`
	TotalFare       float64                  `json:"total_fare"`
	Allocations     []solver.BerthAllocation `json:"allocations"`
	SignedQRPayload string                   `json:"signed_qr_payload"`
	CreatedAt       int64                    `json:"created_at"`
}

func NewServer() (*Server, error) {
	leaseMgr := lease.NewInMemoryLeaseManager()
	sweeper := lease.NewSweeperDaemon(leaseMgr, 1*time.Second)
	solv := solver.NewSpatialFamilySolver()

	cryptoAuth, err := crypto.GenerateSovereignKeypair(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ticket crypto authority: %w", err)
	}

	s := &Server{
		leaseMgr:   leaseMgr,
		sweeper:    sweeper,
		solver:     solv,
		cryptoAuth: cryptoAuth,
		pnrMap:     make(map[string]*PNRRecord),
		coaches:    make(map[string][]*solver.CoachLayout),
	}

	// Seed sample train inventory for 12301 (Rajdhani) and 22436 (Vande Bharat)
	s.seedSampleInventory()

	return s, nil
}

func (s *Server) seedSampleInventory() {
	trains := []struct {
		TrainNumber string
		RunDate     string
		ClassCode   string
		QuotaCode   string
		Seats       int
	}{
		{"12301", "20261105", "3A", "GN", 72},
		{"12301", "20261105", "3A", "TQ", 24},
		{"12301", "20261105", "2A", "GN", 48},
		{"22436", "20261105", "CC", "GN", 120},
	}

	for _, t := range trains {
		s.leaseMgr.SetInitialInventory(t.TrainNumber, t.RunDate, t.ClassCode, t.QuotaCode, t.Seats)
		s.sweeper.RegisterRun(t.TrainNumber, t.RunDate)

		// Create coach layouts
		runKey := t.TrainNumber + ":" + t.RunDate
		if _, ok := s.coaches[runKey]; !ok {
			s.coaches[runKey] = []*solver.CoachLayout{
				solver.NewStandard3ACoach("B1"),
				solver.NewStandard3ACoach("B2"),
				solver.NewStandard3ACoach("B3"),
			}
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "UP",
		"service":     "Railway Revamped Concurrency Core",
		"version":     "2.0.0",
		"sovereignty": "Domestic MeitY Empanelled Region (India)",
		"timestamp":   time.Now().Unix(),
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	date := r.URL.Query().Get("date")

	if date == "" {
		date = "20261105"
	}

	// Catchment cluster matching
	avail12301 := s.leaseMgr.GetAvailableInventory("12301", date, "3A", "GN")
	avail22436 := s.leaseMgr.GetAvailableInventory("22436", date, "CC", "GN")

	results := []map[string]interface{}{
		{
			"train_number":             "12301",
			"train_name":               "Howrah Rajdhani Express",
			"origin":                   "NDLS (New Delhi)",
			"destination":              "HWH (Howrah)",
			"departure_time":           "16:50",
			"arrival_time":             "09:55",
			"class_code":               "3A",
			"quota_code":               "GN",
			"available_seats":          avail12301,
			"rac_count":                0,
			"waitlist_count":           0,
			"base_fare_inr":            2150.00,
			"confirmation_probability": 0.98,
		},
		{
			"train_number":             "22436",
			"train_name":               "Vande Bharat Express",
			"origin":                   "NDLS (New Delhi)",
			"destination":              "BSB (Varanasi)",
			"departure_time":           "06:00",
			"arrival_time":             "14:00",
			"class_code":               "CC",
			"quota_code":               "GN",
			"available_seats":          avail22436,
			"rac_count":                0,
			"waitlist_count":           0,
			"base_fare_inr":            1750.00,
			"confirmation_probability": 0.99,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"search_criteria": map[string]string{"from": from, "to": to, "date": date},
		"trains":          results,
		"server_time_ms":  time.Now().UnixNano() / int64(time.Millisecond),
	})
}

type HoldRequestBody struct {
	UserID         string `json:"user_id"`
	TrainNumber    string `json:"train_number"`
	RunDate        string `json:"run_date"`
	ClassCode      string `json:"class_code"`
	QuotaCode      string `json:"quota_code"`
	OriginStopIdx  int    `json:"origin_stop_index"`
	DestStopIdx    int    `json:"dest_stop_index"`
	PassengerCount int    `json:"passenger_count"`
	TTLSeconds     int    `json:"lease_ttl_seconds"`
}

func (s *Server) handleHold(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req HoldRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: invalid json", http.StatusBadRequest)
		return
	}

	// Compute 64-bit segment mask
	mask, err := bitmask.SegmentMask(req.OriginStopIdx, req.DestStopIdx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	leaseReq := lease.AcquireLeaseRequest{
		UserID:         req.UserID,
		TrainNumber:    req.TrainNumber,
		RunDate:        req.RunDate,
		ClassCode:      req.ClassCode,
		QuotaCode:      req.QuotaCode,
		OriginStopIdx:  req.OriginStopIdx,
		DestStopIdx:    req.DestStopIdx,
		SegmentMask:    mask,
		PassengerCount: req.PassengerCount,
		TTLSeconds:     req.TTLSeconds,
	}

	res, err := s.leaseMgr.AcquireLease(r.Context(), leaseReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if err == lease.ErrActiveLeaseExists {
			w.WriteHeader(http.StatusConflict) // HTTP 409
		} else {
			w.WriteHeader(http.StatusUnprocessableEntity)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:lease-error",
			"title":      "Lease Acquisition Failed",
			"status":     http.StatusConflict,
			"error_code": res.ErrorCode,
			"detail":     res.ErrorMessage,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":             true,
		"lease_token":         res.LeaseToken,
		"expires_at":          res.ExpiresAt,
		"remaining_inventory": res.RemainingInventory,
		"segment_bitmask":     mask,
		"ttl_seconds":         res.ExpiresAt - time.Now().Unix(),
	})
}

type ReleaseRequestBody struct {
	UserID      string `json:"user_id"`
	TrainNumber string `json:"train_number"`
	RunDate     string `json:"run_date"`
	LeaseToken  string `json:"lease_token"`
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ReleaseRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	err := s.leaseMgr.ReleaseLease(r.Context(), req.TrainNumber, req.RunDate, req.LeaseToken, req.UserID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Lease released and inventory returned to pool",
	})
}

type ConfirmRequestBody struct {
	UserID         string                    `json:"user_id"`
	LeaseToken     string                    `json:"lease_token"`
	PaymentTxnID   string                    `json:"payment_transaction_id"`
	HasFlexiCancel bool                      `json:"has_flexi_cancel"`
	Passengers     []solver.PassengerRequest `json:"passengers"`
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfirmRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 1. Retrieve lease
	meta, err := s.leaseMgr.GetLease(r.Context(), "", "", req.LeaseToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("Lease invalid or expired: %v", err), http.StatusGone)
		return
	}

	// 2. Commit lease (mark seat sold, unlock user)
	if err := s.leaseMgr.CommitLease(r.Context(), meta.TrainNumber, meta.RunDate, req.LeaseToken); err != nil {
		http.Error(w, fmt.Sprintf("Commit failed: %v", err), http.StatusInternalServerError)
		return
	}

	// 3. Run Spatial Family Solver
	runKey := meta.TrainNumber + ":" + meta.RunDate
	coachesList, ok := s.coaches[runKey]
	if !ok {
		coachesList = []*solver.CoachLayout{solver.NewStandard3ACoach("B1")}
	}

	allocations, err := s.solver.AllocateFamily(coachesList, req.Passengers)
	if err != nil {
		http.Error(w, fmt.Sprintf("Berth allocation failed: %v", err), http.StatusInternalServerError)
		return
	}

	// 4. Generate random 10-digit PNR
	pnr := fmt.Sprintf("%010d", rand.Int63n(9000000000)+1000000000)

	// 5. Issue Asymmetric Ed25519 Signed QR Code for the primary passenger
	firstAlloc := allocations[0]
	qrPayload, _ := s.cryptoAuth.IssueSignedTicketQR(crypto.TicketPayload{
		PNRNumber:     pnr,
		TrainNumber:   meta.TrainNumber,
		RunDate:       meta.RunDate,
		Origin:        "NDLS",
		Destination:   "PRYJ",
		ClassCode:     meta.ClassCode,
		CoachID:       firstAlloc.CoachID,
		BerthNumber:   firstAlloc.BerthNumber,
		PassengerName: firstAlloc.FullName,
	})

	baseFare := 2150.00 * float64(len(req.Passengers))
	if req.HasFlexiCancel {
		baseFare += 49.00 * float64(len(req.Passengers))
	}

	record := &PNRRecord{
		PNRNumber:       pnr,
		TrainNumber:     meta.TrainNumber,
		TrainName:       "Howrah Rajdhani Express",
		RunDate:         meta.RunDate,
		ClassCode:       meta.ClassCode,
		QuotaCode:       meta.QuotaCode,
		OriginStopIdx:   meta.OriginStopIdx,
		DestStopIdx:     meta.DestStopIdx,
		Status:          "CONFIRMED",
		TotalFare:       baseFare,
		Allocations:     allocations,
		SignedQRPayload: qrPayload,
		CreatedAt:       time.Now().Unix(),
	}

	s.pnrMu.Lock()
	s.pnrMap[pnr] = record
	s.pnrMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"pnr_number":        pnr,
		"status":            "CONFIRMED",
		"total_fare":        baseFare,
		"passengers":        allocations,
		"signed_qr_payload": qrPayload,
	})
}

func (s *Server) handlePNRStatus(w http.ResponseWriter, r *http.Request) {
	pnr := strings.TrimPrefix(r.URL.Path, "/v1/booking/pnr/")
	s.pnrMu.RLock()
	record, exists := s.pnrMap[pnr]
	s.pnrMu.RUnlock()

	if !exists {
		http.Error(w, "PNR not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(record)
}

func main() {
	server, err := NewServer()
	if err != nil {
		log.Fatalf("Fatal server init: %v", err)
	}

	server.sweeper.Start()
	defer server.sweeper.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/v1/search/trains", server.handleSearch)
	mux.HandleFunc("/v1/booking/hold", server.handleHold)
	mux.HandleFunc("/v1/booking/release", server.handleRelease)
	mux.HandleFunc("/v1/booking/confirm", server.handleConfirm)
	mux.HandleFunc("/v1/booking/pnr/", server.handlePNRStatus)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("[CORE] Railway Revamped Reservation Core listening on http://localhost:%s", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failure: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[CORE] Shutting down reservation core gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
}
