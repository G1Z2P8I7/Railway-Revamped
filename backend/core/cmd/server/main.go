package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/railway-revamped/core/pkg/attestation"
	"github.com/railway-revamped/core/pkg/bitmask"
	"github.com/railway-revamped/core/pkg/charting"
	"github.com/railway-revamped/core/pkg/crypto"
	"github.com/railway-revamped/core/pkg/dpdp"
	"github.com/railway-revamped/core/pkg/grievance"
	"github.com/railway-revamped/core/pkg/ingress"
	"github.com/railway-revamped/core/pkg/lease"
	"github.com/railway-revamped/core/pkg/payment"
	"github.com/railway-revamped/core/pkg/pow"
	"github.com/railway-revamped/core/pkg/solver"
	"github.com/railway-revamped/core/pkg/telemetry"
)

type Server struct {
	leaseMgr         *lease.InMemoryLeaseManager
	sweeper          *lease.SweeperDaemon
	solver           *solver.SpatialFamilySolver
	cryptoAuth       *crypto.SovereignTicketAuthority
	lobby            *ingress.VirtualWaitingLobby
	exclusivity      *ingress.ExclusivityManager
	attestMgr        *attestation.AttestationManager
	recoveryEng      *attestation.AccountRecoveryEngine
	powEng           *pow.MemoryHardPoWEngine
	chartDaemon      *charting.ChartPreparationDaemon
	cascadeEng       *charting.CascadeEngine
	wlForecaster     *charting.BayesianWLForecaster
	ledgerMgr        *payment.TransitLedgerManager
	refundDispatcher *payment.RefundDispatcher
	paymentOrch      *payment.PaymentOrchestrator
	reconciler       *payment.ReconciliationPoller
	rtisEng          *telemetry.RTISIngestionEngine
	tdrEng           *grievance.TDREngine
	dpdpEng          *dpdp.ComplianceEngine

	// In-memory PNR & Run registries
	pnrMu   sync.RWMutex
	pnrMap  map[string]*PNRRecord
	coaches map[string][]*solver.CoachLayout // train:date -> coach layouts
	runs    map[string]*charting.ScheduledTrainRun
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
	HasFlexiCancel  bool                     `json:"has_flexi_cancel"`
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

	lobby := ingress.NewVirtualWaitingLobby(500*time.Millisecond, 500000)
	exclusivity := ingress.NewExclusivityManager(nil)
	attestMgr := attestation.NewAttestationManager(nil)
	recoveryEng := attestation.NewAccountRecoveryEngine(attestMgr)
	powEng := pow.NewMemoryHardPoWEngine(8)
	chartDaemon := charting.NewChartPreparationDaemon(5*time.Second, nil)
	cascadeEng := charting.NewCascadeEngine()
	wlForecaster := charting.NewBayesianWLForecaster()

	ledgerMgr := payment.NewTransitLedgerManager()
	refundDispatcher := payment.NewRefundDispatcher(ledgerMgr)
	paymentOrch := payment.NewPaymentOrchestrator(leaseMgr, refundDispatcher)
	reconciler := payment.NewReconciliationPoller(paymentOrch, refundDispatcher, 60*time.Second, nil)

	s := &Server{
		leaseMgr:         leaseMgr,
		sweeper:          sweeper,
		solver:           solv,
		cryptoAuth:       cryptoAuth,
		lobby:            lobby,
		exclusivity:      exclusivity,
		attestMgr:        attestMgr,
		recoveryEng:      recoveryEng,
		powEng:           powEng,
		chartDaemon:      chartDaemon,
		cascadeEng:       cascadeEng,
		wlForecaster:     wlForecaster,
		ledgerMgr:        ledgerMgr,
		refundDispatcher: refundDispatcher,
		paymentOrch:      paymentOrch,
		reconciler:       reconciler,
		rtisEng:          telemetry.NewRTISIngestionEngine(50000, 500, 50*time.Millisecond),
		tdrEng:           grievance.NewTDREngine(),
		pnrMap:           make(map[string]*PNRRecord),
		coaches:          make(map[string][]*solver.CoachLayout),
		runs:             make(map[string]*charting.ScheduledTrainRun),
	}

	dpdpEng, _ := dpdp.NewComplianceEngine(nil)
	s.dpdpEng = dpdpEng

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
		{"13352", "20261105", "3A", "GN", 64},
		{"13352", "20261105", "3A", "TQ", 18},
		{"13352", "20261105", "2A", "GN", 36},
		{"12842", "20261105", "3A", "GN", 72},
		{"12842", "20261105", "2A", "GN", 48},
	}

	for _, t := range trains {
		s.leaseMgr.SetInitialInventory(t.TrainNumber, t.RunDate, t.ClassCode, t.QuotaCode, t.Seats)
		s.sweeper.RegisterRun(t.TrainNumber, t.RunDate)

		runKey := t.TrainNumber + ":" + t.RunDate
		if _, ok := s.coaches[runKey]; !ok {
			c1 := solver.NewStandard3ACoach("B1")
			c2 := solver.NewStandard3ACoach("B2")
			c3 := solver.NewStandard3ACoach("B3")
			s.coaches[runKey] = []*solver.CoachLayout{c1, c2, c3}

			departure := time.Now().Add(6 * time.Hour)
			scheduledRun := &charting.ScheduledTrainRun{
				TrainNumber:        t.TrainNumber,
				TrainName:          "Howrah Rajdhani Express",
				RunDate:            t.RunDate,
				ScheduledDeparture: departure,
				OriginStation:      "NDLS",
				DestStation:        "HWH",
				ChartingState:      charting.ChartPending,
				Coaches:            s.coaches[runKey],
				QuotaPools: map[charting.QuotaCode]int{
					charting.QuotaGeneral:        t.Seats,
					charting.QuotaEmergencyHO:    6,
					charting.QuotaDefence:        4,
					charting.QuotaForeignTourist: 2,
				},
				ConfirmedList: make([]*charting.TicketPassenger, 0),
				RACList:       make([]*charting.TicketPassenger, 0),
				Waitlist:      make([]*charting.TicketPassenger, 0),
			}
			s.runs[runKey] = scheduledRun
			s.chartDaemon.RegisterTrainRun(scheduledRun)
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "UP",
		"service":     "Railway Revamped Reservation System",
		"version":     "2.0.0",
		"timestamp":   time.Now().Unix(),
		"features": []string{
			"64_bit_segment_bitmask",
			"500ms_jitter_lobby",
			"30min_citizen_exclusivity",
			"tiered_pre_attestation",
			"argon2_mpow_surge_shield",
			"spatial_family_solver",
			"8_hour_advance_charting",
			"two_phase_hold_and_authorize",
			"60s_reconciliation_poller",
			"instant_transit_ledger_refund",
			"automated_bank_source_reversal",
			"ed25519_offline_qr",
			"rtis_locomotive_telemetry_stream",
			"automated_1click_tdr_railmadad",
			"dpdp_citizen_privacy_portal",
		},
	})
}

// Ingress: 500ms Jitter Virtual Waiting Lobby & Citizen Exclusivity
type LobbyJoinRequest struct {
	UserID      string             `json:"user_id"`
	ClientType  ingress.ClientType `json:"client_type"`
	TrainNumber string             `json:"train_number"`
	RunDate     string             `json:"run_date"`
	ClassCode   string             `json:"class_code"`
}

func (s *Server) handleLobbyJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LobbyJoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ClientType == "" {
		req.ClientType = ingress.ClientCitizen
	}

	if err := s.exclusivity.CheckAccess(req.ClientType, req.ClassCode); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:exclusivity-violation",
			"title":      "Citizen Exclusivity Window Active",
			"status":     http.StatusForbidden,
			"error_code": "ERR_CITIZEN_EXCLUSIVITY_WINDOW",
			"detail":     err.Error(),
		})
		return
	}

	ticket, err := s.lobby.EnqueueRequest(r.Context(), req.UserID, req.ClientType, req.TrainNumber, req.RunDate, req.ClassCode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ticket)
}

func (s *Server) handlePoWChallenge(w http.ResponseWriter, r *http.Request) {
	ch, token := s.powEng.GenerateChallenge()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"challenge": ch,
		"token":     token,
	})
}

type PoWVerifyRequest struct {
	Token    string       `json:"token"`
	Solution pow.Solution `json:"solution"`
}

func (s *Server) handlePoWVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PoWVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := s.powEng.VerifySolution(req.Token, req.Solution); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    false,
			"error_code": "ERR_MPOW_FAILED",
			"detail":     err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Memory-hard proof-of-work verified successfully",
	})
}

// Auth: Tiered Pre-Attestation
type AttestRequest struct {
	UserID       string                                `json:"user_id"`
	AadhaarToken string                                `json:"aadhaar_token"`
	Tier         attestation.AttestationTier           `json:"tier"`
	Tier1Data    *attestation.Tier1ChallengeResponse   `json:"tier1_data,omitempty"`
	Tier2Data    *attestation.Tier2BudgetDevicePayload `json:"tier2_data,omitempty"`
	Tier3Data    *attestation.Tier3KioskPayload        `json:"tier3_data,omitempty"`
}

func (s *Server) handleAttest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AttestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var session *attestation.AttestedSession
	var err error

	switch req.Tier {
	case attestation.Tier1HighEndHardware:
		if req.Tier1Data == nil {
			http.Error(w, "Missing tier1_data", http.StatusBadRequest)
			return
		}
		session, err = s.attestMgr.AttestTier1Hardware(req.UserID, req.AadhaarToken, *req.Tier1Data)
	case attestation.Tier2BudgetMobile:
		if req.Tier2Data == nil {
			http.Error(w, "Missing tier2_data", http.StatusBadRequest)
			return
		}
		session, err = s.attestMgr.AttestTier2BudgetMobile(req.UserID, req.AadhaarToken, *req.Tier2Data)
	case attestation.Tier3AssistedKiosk:
		if req.Tier3Data == nil {
			http.Error(w, "Missing tier3_data", http.StatusBadRequest)
			return
		}
		session, err = s.attestMgr.AttestTier3Kiosk(req.UserID, req.AadhaarToken, *req.Tier3Data)
	default:
		http.Error(w, "Invalid attestation tier (must be 1, 2, or 3)", http.StatusBadRequest)
		return
	}

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	sessionToken := s.attestMgr.SignSessionToken(session)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"session":       session,
		"session_token": sessionToken,
	})
}

// Auth: Device Recovery Protocol
type RecoverRequest struct {
	UserID               string `json:"user_id"`
	AadhaarVID           string `json:"aadhaar_vid"`
	OTP                  string `json:"otp"`
	NewDeviceFingerprint string `json:"new_device_fingerprint"`
}

func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RecoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	session, err := s.recoveryEng.InitiateDeviceRecovery(req.UserID, req.AadhaarVID, req.OTP, req.NewDeviceFingerprint)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	sessionToken := s.attestMgr.SignSessionToken(session)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":           true,
		"message":           "Device recovery successful. High-velocity Tatkal bookings paused for 12 hours.",
		"cooling_off_until": session.CoolingOffUntil,
		"session":           session,
		"session_token":     sessionToken,
	})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	date := r.URL.Query().Get("date")

	if date == "" {
		date = "20261105"
	} else if strings.Contains(date, "-") {
		parts := strings.Split(date, "-")
		if len(parts) == 3 {
			if len(parts[0]) == 2 { // DD-MM-YYYY -> YYYYMMDD
				date = parts[2] + parts[1] + parts[0]
			} else { // YYYY-MM-DD -> YYYYMMDD
				date = parts[0] + parts[1] + parts[2]
			}
		}
	} else if len(date) == 8 && !strings.HasPrefix(date, "20") { // DDMMYYYY -> YYYYMMDD
		date = date[4:] + date[2:4] + date[:2]
	}

	avail12301 := s.leaseMgr.GetAvailableInventory("12301", date, "3A", "GN")
	avail22436 := s.leaseMgr.GetAvailableInventory("22436", date, "CC", "GN")
	avail13352 := s.leaseMgr.GetAvailableInventory("13352", date, "3A", "GN")
	avail12842 := s.leaseMgr.GetAvailableInventory("12842", date, "3A", "GN")

	fromUpper := strings.ToUpper(from)
	toUpper := strings.ToUpper(to)

	var results []map[string]interface{}

	if (strings.Contains(fromUpper, "MAS") || strings.Contains(fromUpper, "CHENNAI")) &&
		(strings.Contains(toUpper, "BKSC") || strings.Contains(toUpper, "BOKARO")) {
		results = []map[string]interface{}{
			{
				"train_number":   "13352",
				"train_name":     "Dhanbad / Bokaro Express (Alleppey - Bokaro)",
				"train_type":     "MAIL_EXPRESS",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "BKSC (Bokaro Steel City)",
				"departure_time": "22:10",
				"arrival_time":   "10:15 (+2 days)",
				"duration":       "36h 05m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": avail13352,
				"base_fare_inr":  2280.00,
				"confirmation_probability": 0.96,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 128, "status": "AVAILABLE 128", "fare": 610.00, "probability": 0.94},
					{"class_code": "3E", "class_name": "3 AC Economy", "available_seats": 56, "status": "AVAILABLE 56", "fare": 1720.00, "probability": 0.95},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": avail13352, "status": fmt.Sprintf("AVAILABLE %d", avail13352), "fare": 2280.00, "probability": 0.96},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 18, "status": "AVAILABLE 18", "fare": 3150.00, "probability": 0.98},
				},
			},
			{
				"train_number":   "12842",
				"train_name":     "Coromandel Express",
				"train_type":     "SUPERFAST",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "BKSC (via TATA/HWH)",
				"departure_time": "07:00",
				"arrival_time":   "11:40 (+1 day)",
				"duration":       "28h 40m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": avail12842,
				"base_fare_inr":  3150.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 84, "status": "AVAILABLE 84", "fare": 720.00, "probability": 0.92},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": avail12842, "status": fmt.Sprintf("AVAILABLE %d", avail12842), "fare": 1950.00, "probability": 0.97},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 22, "status": "AVAILABLE 22", "fare": 2840.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 6, "status": "AVAILABLE 6", "fare": 4780.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "22643",
				"train_name":     "Patna Superfast Express",
				"train_type":     "SUPERFAST",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "BKSC (Bokaro Steel City)",
				"departure_time": "05:25",
				"arrival_time":   "18:50 (+1 day)",
				"duration":       "37h 25m",
				"runs_on":        []string{"Mon", "Tue"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 38,
				"base_fare_inr":  2340.00,
				"confirmation_probability": 0.95,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 92, "status": "AVAILABLE 92", "fare": 650.00, "probability": 0.91},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 38, "status": "AVAILABLE 38", "fare": 2340.00, "probability": 0.95},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 12, "status": "AVAILABLE 12", "fare": 3260.00, "probability": 0.97},
				},
			},
			{
				"train_number":   "12840",
				"train_name":     "Howrah Mail",
				"train_type":     "SUPERFAST",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "BKSC (via HWH/TATA)",
				"departure_time": "19:00",
				"arrival_time":   "23:00 (+1 day)",
				"duration":       "28h 00m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 52,
				"base_fare_inr":  2150.00,
				"confirmation_probability": 0.96,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 110, "status": "AVAILABLE 110", "fare": 690.00, "probability": 0.93},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 52, "status": "AVAILABLE 52", "fare": 1910.00, "probability": 0.96},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 16, "status": "AVAILABLE 16", "fare": 2780.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 4, "status": "AVAILABLE 4", "fare": 4690.00, "probability": 0.99},
				},
			},
		}
	} else if (strings.Contains(fromUpper, "NDLS") || strings.Contains(fromUpper, "DELHI")) &&
		(strings.Contains(toUpper, "BSB") || strings.Contains(toUpper, "VARANASI")) {
		results = []map[string]interface{}{
			{
				"train_number":   "22436",
				"train_name":     "Vande Bharat Express",
				"train_type":     "VANDE_BHARAT",
				"origin":         "NDLS (New Delhi)",
				"destination":    "BSB (Varanasi Jn)",
				"departure_time": "06:00",
				"arrival_time":   "14:00",
				"duration":       "8h 00m",
				"runs_on":        []string{"Tue", "Wed", "Fri", "Sat", "Sun"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": avail22436,
				"base_fare_inr":  1750.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": avail22436, "status": fmt.Sprintf("AVAILABLE %d", avail22436), "fare": 1750.00, "probability": 0.99},
					{"class_code": "EC", "class_name": "Exec. Chair Car", "available_seats": 14, "status": "AVAILABLE 14", "fare": 3300.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "20504",
				"train_name":     "Rajdhani Express",
				"train_type":     "RAJDHANI",
				"origin":         "NDLS (New Delhi)",
				"destination":    "BSB (Varanasi Jn)",
				"departure_time": "11:25",
				"arrival_time":   "21:30",
				"duration":       "10h 05m",
				"runs_on":        []string{"Mon", "Tue", "Thu", "Fri", "Sat"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 42,
				"base_fare_inr":  2150.00,
				"confirmation_probability": 0.98,
				"classes": []map[string]interface{}{
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 42, "status": "AVAILABLE 42", "fare": 2150.00, "probability": 0.98},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 18, "status": "AVAILABLE 18", "fare": 3050.00, "probability": 0.99},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 8, "status": "AVAILABLE 8", "fare": 4950.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12560",
				"train_name":     "Shiv Ganga Express",
				"train_type":     "SUPERFAST",
				"origin":         "NDLS (New Delhi)",
				"destination":    "BSB (Varanasi Jn)",
				"departure_time": "20:05",
				"arrival_time":   "06:10 (+1 day)",
				"duration":       "10h 05m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 82,
				"base_fare_inr":  1180.00,
				"confirmation_probability": 0.97,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 165, "status": "AVAILABLE 165", "fare": 440.00, "probability": 0.95},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 82, "status": "AVAILABLE 82", "fare": 1180.00, "probability": 0.97},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 34, "status": "AVAILABLE 34", "fare": 1680.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 10, "status": "AVAILABLE 10", "fare": 2840.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12582",
				"train_name":     "Banaras Superfast Express",
				"train_type":     "SUPERFAST",
				"origin":         "NDLS (New Delhi)",
				"destination":    "BSBS (Banaras)",
				"departure_time": "22:50",
				"arrival_time":   "10:00 (+1 day)",
				"duration":       "11h 10m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 65,
				"base_fare_inr":  1180.00,
				"confirmation_probability": 0.96,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 140, "status": "AVAILABLE 140", "fare": 440.00, "probability": 0.94},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 65, "status": "AVAILABLE 65", "fare": 1180.00, "probability": 0.96},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 24, "status": "AVAILABLE 24", "fare": 1680.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 8, "status": "AVAILABLE 8", "fare": 2840.00, "probability": 0.99},
				},
			},
		}
	} else if (strings.Contains(fromUpper, "MAS") || strings.Contains(fromUpper, "CHENNAI")) &&
		(strings.Contains(toUpper, "SBC") || strings.Contains(toUpper, "BENGALURU") || strings.Contains(toUpper, "BANGALORE")) {
		results = []map[string]interface{}{
			{
				"train_number":   "20607",
				"train_name":     "Mysuru Vande Bharat Express",
				"train_type":     "VANDE_BHARAT",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "SBC (KSR Bengaluru)",
				"departure_time": "05:50",
				"arrival_time":   "10:10",
				"duration":       "4h 20m",
				"runs_on":        []string{"Mon", "Tue", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": 110,
				"base_fare_inr":  995.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": 110, "status": "AVAILABLE 110", "fare": 995.00, "probability": 0.99},
					{"class_code": "EC", "class_name": "Exec. Chair Car", "available_seats": 22, "status": "AVAILABLE 22", "fare": 1885.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12007",
				"train_name":     "Chennai Mysuru Shatabdi Express",
				"train_type":     "SHATABDI",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "SBC (KSR Bengaluru)",
				"departure_time": "06:00",
				"arrival_time":   "10:45",
				"duration":       "4h 45m",
				"runs_on":        []string{"Mon", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": 125,
				"base_fare_inr":  910.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": 125, "status": "AVAILABLE 125", "fare": 910.00, "probability": 0.99},
					{"class_code": "EC", "class_name": "Exec. Chair Car", "available_seats": 20, "status": "AVAILABLE 20", "fare": 1750.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12609",
				"train_name":     "Bengaluru Express",
				"train_type":     "SUPERFAST",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "SBC (KSR Bengaluru)",
				"departure_time": "13:35",
				"arrival_time":   "19:45",
				"duration":       "6h 10m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": 90,
				"base_fare_inr":  580.00,
				"confirmation_probability": 0.98,
				"classes": []map[string]interface{}{
					{"class_code": "2S", "class_name": "Second Sitting", "available_seats": 210, "status": "AVAILABLE 210", "fare": 160.00, "probability": 0.96},
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": 90, "status": "AVAILABLE 90", "fare": 580.00, "probability": 0.98},
				},
			},
			{
				"train_number":   "12657",
				"train_name":     "Chennai Bengaluru Mail",
				"train_type":     "SUPERFAST",
				"origin":         "MAS (MGR Chennai CTL)",
				"destination":    "SBC (KSR Bengaluru)",
				"departure_time": "22:50",
				"arrival_time":   "04:30 (+1 day)",
				"duration":       "5h 40m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 68,
				"base_fare_inr":  710.00,
				"confirmation_probability": 0.97,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 140, "status": "AVAILABLE 140", "fare": 265.00, "probability": 0.95},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 68, "status": "AVAILABLE 68", "fare": 710.00, "probability": 0.97},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 30, "status": "AVAILABLE 30", "fare": 1010.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 10, "status": "AVAILABLE 10", "fare": 1690.00, "probability": 0.99},
				},
			},
		}
	} else if (strings.Contains(fromUpper, "HWH") || strings.Contains(fromUpper, "HOWRAH")) &&
		(strings.Contains(toUpper, "NJP") || strings.Contains(toUpper, "JALPAIGURI")) {
		results = []map[string]interface{}{
			{
				"train_number":   "22301",
				"train_name":     "New Jalpaiguri Vande Bharat Express",
				"train_type":     "VANDE_BHARAT",
				"origin":         "HWH (Howrah Jn)",
				"destination":    "NJP (New Jalpaiguri)",
				"departure_time": "05:55",
				"arrival_time":   "13:25",
				"duration":       "7h 30m",
				"runs_on":        []string{"Mon", "Tue", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": 85,
				"base_fare_inr":  1565.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": 85, "status": "AVAILABLE 85", "fare": 1565.00, "probability": 0.99},
					{"class_code": "EC", "class_name": "Exec. Chair Car", "available_seats": 18, "status": "AVAILABLE 18", "fare": 2825.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12041",
				"train_name":     "New Jalpaiguri Shatabdi Express",
				"train_type":     "SHATABDI",
				"origin":         "HWH (Howrah Jn)",
				"destination":    "NJP (New Jalpaiguri)",
				"departure_time": "14:15",
				"arrival_time":   "22:30",
				"duration":       "8h 15m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": 95,
				"base_fare_inr":  1440.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": 95, "status": "AVAILABLE 95", "fare": 1440.00, "probability": 0.99},
					{"class_code": "EC", "class_name": "Exec. Chair Car", "available_seats": 16, "status": "AVAILABLE 16", "fare": 2590.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12343",
				"train_name":     "Darjeeling Mail",
				"train_type":     "SUPERFAST",
				"origin":         "SDAH (Sealdah)",
				"destination":    "NJP (New Jalpaiguri)",
				"departure_time": "22:05",
				"arrival_time":   "08:00 (+1 day)",
				"duration":       "9h 55m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 60,
				"base_fare_inr":  995.00,
				"confirmation_probability": 0.97,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 120, "status": "AVAILABLE 120", "fare": 375.00, "probability": 0.94},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 60, "status": "AVAILABLE 60", "fare": 995.00, "probability": 0.97},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 25, "status": "AVAILABLE 25", "fare": 1415.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 8, "status": "AVAILABLE 8", "fare": 2385.00, "probability": 0.99},
				},
			},
		}
	} else {
		origDisplay := from
		if origDisplay == "" {
			origDisplay = "NDLS (New Delhi)"
		}
		destDisplay := to
		if destDisplay == "" {
			destDisplay = "HWH (Howrah Jn)"
		}

		results = []map[string]interface{}{
			{
				"train_number":   "12301",
				"train_name":     "Howrah Rajdhani Express (via Gaya)",
				"train_type":     "RAJDHANI",
				"origin":         origDisplay,
				"destination":    destDisplay,
				"departure_time": "16:50",
				"arrival_time":   "09:55 (+1 day)",
				"duration":       "17h 05m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": avail12301,
				"base_fare_inr":  2450.00,
				"confirmation_probability": 0.98,
				"classes": []map[string]interface{}{
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": avail12301, "status": fmt.Sprintf("AVAILABLE %d", avail12301), "fare": 2450.00, "probability": 0.98},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 26, "status": "AVAILABLE 26", "fare": 3450.00, "probability": 0.99},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 10, "status": "AVAILABLE 10", "fare": 5650.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12314",
				"train_name":     "Sealdah Rajdhani Express",
				"train_type":     "RAJDHANI",
				"origin":         origDisplay,
				"destination":    destDisplay,
				"departure_time": "16:30",
				"arrival_time":   "10:10 (+1 day)",
				"duration":       "17h 40m",
				"runs_on":        []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 62,
				"base_fare_inr":  2420.00,
				"confirmation_probability": 0.98,
				"classes": []map[string]interface{}{
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 62, "status": "AVAILABLE 62", "fare": 2420.00, "probability": 0.98},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 24, "status": "AVAILABLE 24", "fare": 3420.00, "probability": 0.99},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 8, "status": "AVAILABLE 8", "fare": 5610.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "12304",
				"train_name":     "Poorva Express",
				"train_type":     "SUPERFAST",
				"origin":         origDisplay,
				"destination":    destDisplay,
				"departure_time": "17:40",
				"arrival_time":   "17:00 (+1 day)",
				"duration":       "23h 20m",
				"runs_on":        []string{"Wed", "Thu", "Sat", "Sun"},
				"class_code":     "3A",
				"quota_code":     "GN",
				"available_seats": 55,
				"base_fare_inr":  1650.00,
				"confirmation_probability": 0.96,
				"classes": []map[string]interface{}{
					{"class_code": "SL", "class_name": "Sleeper", "available_seats": 145, "status": "AVAILABLE 145", "fare": 620.00, "probability": 0.94},
					{"class_code": "3A", "class_name": "AC 3 Tier", "available_seats": 55, "status": "AVAILABLE 55", "fare": 1650.00, "probability": 0.96},
					{"class_code": "2A", "class_name": "AC 2 Tier", "available_seats": 20, "status": "AVAILABLE 20", "fare": 2380.00, "probability": 0.98},
					{"class_code": "1A", "class_name": "AC First Class", "available_seats": 6, "status": "AVAILABLE 6", "fare": 4050.00, "probability": 0.99},
				},
			},
			{
				"train_number":   "22436",
				"train_name":     "Vande Bharat Express",
				"train_type":     "VANDE_BHARAT",
				"origin":         origDisplay,
				"destination":    destDisplay,
				"departure_time": "06:00",
				"arrival_time":   "14:00",
				"duration":       "8h 00m",
				"runs_on":        []string{"Tue", "Wed", "Fri", "Sat", "Sun"},
				"class_code":     "CC",
				"quota_code":     "GN",
				"available_seats": avail22436,
				"base_fare_inr":  1750.00,
				"confirmation_probability": 0.99,
				"classes": []map[string]interface{}{
					{"class_code": "CC", "class_name": "AC Chair Car", "available_seats": avail22436, "status": fmt.Sprintf("AVAILABLE %d", avail22436), "fare": 1750.00, "probability": 0.99},
					{"class_code": "EC", "class_name": "Exec. Chair Car", "available_seats": 14, "status": "AVAILABLE 14", "fare": 3300.00, "probability": 0.99},
				},
			},
		}
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

	// Authenticate user session token if provided to prevent lease spoofing / starvation DoS
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != "" {
			authUserID, _, err := s.attestMgr.VerifySessionToken(token)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"type":       "urn:railway:auth-error",
					"title":      "Invalid Authentication Token",
					"status":     http.StatusUnauthorized,
					"error_code": "ERR_INVALID_SESSION_TOKEN",
					"detail":     err.Error(),
				})
				return
			}
			req.UserID = authUserID
		}
	}
	if req.UserID == "" {
		req.UserID = "guest_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}

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
			w.WriteHeader(http.StatusConflict)
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

// Payment Endpoints: Two-Phase Hold-and-Authorize & Webhook
type PaymentInitRequest struct {
	UserID      string                 `json:"user_id"`
	TrainNumber string                 `json:"train_number"`
	RunDate     string                 `json:"run_date"`
	LeaseToken  string                 `json:"lease_token"`
	AmountINR   float64                `json:"amount_inr"`
	Gateway     payment.PaymentGateway `json:"gateway"`
}

func (s *Server) handlePaymentInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req PaymentInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.Gateway == "" {
		req.Gateway = payment.GatewayRazorpay
	}

	txn, err := s.paymentOrch.InitiateHoldAndAuthorize(
		r.Context(),
		req.UserID,
		req.TrainNumber,
		req.RunDate,
		req.LeaseToken,
		req.AmountINR,
		req.Gateway,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(txn)
}

func (s *Server) handlePaymentWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request: failed to read body", http.StatusBadRequest)
		return
	}

	// Webhook HMAC-SHA256 signature verification (VULN-02 remediation)
	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	if webhookSecret == "" {
		webhookSecret = "railpulse_webhook_secret_2026"
	}

	sigHeader := r.Header.Get("X-Webhook-Signature")
	if sigHeader == "" {
		sigHeader = r.Header.Get("X-Razorpay-Signature")
	}
	secretHeader := r.Header.Get("X-Webhook-Secret")

	isVerified := false
	if sigHeader != "" {
		mac := hmac.New(sha256.New, []byte(webhookSecret))
		mac.Write(body)
		expectedSig := hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(sigHeader), []byte(expectedSig)) {
			isVerified = true
		}
	} else if secretHeader == webhookSecret {
		isVerified = true
	} else if auth := r.Header.Get("Authorization"); auth == "Bearer "+webhookSecret {
		isVerified = true
	}

	if !isVerified {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:webhook-auth-error",
			"title":      "Unauthorized Webhook Callback",
			"status":     http.StatusUnauthorized,
			"error_code": "ERR_INVALID_WEBHOOK_SIGNATURE",
			"detail":     "Webhook request missing or failing HMAC-SHA256 signature verification",
		})
		return
	}

	var event payment.WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "Bad Request: invalid json", http.StatusBadRequest)
		return
	}

	action, txn, err := s.paymentOrch.HandleWebhookCallback(r.Context(), event)
	if err != nil && action != payment.ActionRejectAndRefund {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"action":      action,
		"transaction": txn,
	})
}

// Transit Ledger Endpoints
func (s *Server) handleTransitLedger(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimPrefix(r.URL.Path, "/v1/payment/ledger/")
	if userID == "" {
		http.Error(w, "User ID required", http.StatusBadRequest)
		return
	}

	// BOLA / IDOR Protection (VULN-03 remediation)
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:auth-error",
			"title":      "Authentication Required",
			"status":     http.StatusUnauthorized,
			"error_code": "ERR_AUTH_REQUIRED",
			"detail":     "Bearer session token required to view transit ledger",
		})
		return
	}

	authUserID, _, err := s.attestMgr.VerifySessionToken(token)
	if err != nil {
		adminToken := os.Getenv("ADMIN_TOKEN")
		if adminToken == "" {
			adminToken = "dev_admin_token"
		}
		if token != adminToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"type":       "urn:railway:auth-error",
				"title":      "Invalid Authentication Token",
				"status":     http.StatusUnauthorized,
				"error_code": "ERR_INVALID_SESSION_TOKEN",
				"detail":     err.Error(),
			})
			return
		}
		authUserID = userID
	}

	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		adminToken = "dev_admin_token"
	}
	if authUserID != userID && token != adminToken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:auth-error",
			"title":      "Forbidden: Access Denied",
			"status":     http.StatusForbidden,
			"error_code": "ERR_FORBIDDEN_LEDGER_ACCESS",
			"detail":     "Access denied: you cannot view another user's transit ledger",
		})
		return
	}

	acc := s.ledgerMgr.GetAccount(userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(acc)
}

func getTrainName(trainNo string) string {
	switch trainNo {
	case "13352":
		return "Dhanbad / Bokaro Express"
	case "12842":
		return "Coromandel Express"
	case "12840":
		return "Howrah Mail"
	case "22643":
		return "Patna Superfast Express"
	case "22436":
		return "Vande Bharat Express"
	case "20504":
		return "Rajdhani Express"
	case "12560":
		return "Shiv Ganga Express"
	case "12582":
		return "Banaras Superfast Express"
	case "20607":
		return "Mysuru Vande Bharat Express"
	case "12007":
		return "Chennai Mysuru Shatabdi Express"
	case "12609":
		return "Bengaluru Express"
	case "12657":
		return "Chennai Bengaluru Mail"
	case "22301":
		return "New Jalpaiguri Vande Bharat Express"
	case "12041":
		return "New Jalpaiguri Shatabdi Express"
	case "12343":
		return "Darjeeling Mail"
	case "12951":
		return "Tejas Rajdhani Express"
	case "12953":
		return "August Kranti Tejas Rajdhani"
	case "12925":
		return "Paschim Express"
	case "12137":
		return "Punjab Mail"
	case "12314":
		return "Sealdah Rajdhani Express"
	case "12304":
		return "Poorva Express"
	default:
		return "Howrah Rajdhani Express"
	}
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

	// Payment Verification Invariant (VULN-01 remediation)
	if req.PaymentTxnID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:payment-error",
			"title":      "Payment Transaction Required",
			"status":     http.StatusBadRequest,
			"error_code": "ERR_MISSING_PAYMENT_TXN",
			"detail":     "payment_transaction_id is strictly required to confirm booking",
		})
		return
	}

	txn, err := s.paymentOrch.GetTransaction(req.PaymentTxnID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:payment-error",
			"title":      "Invalid Payment Transaction",
			"status":     http.StatusBadRequest,
			"error_code": "ERR_TXN_NOT_FOUND",
			"detail":     fmt.Sprintf("Payment transaction not found: %v", err),
		})
		return
	}

	if txn.Status != payment.TxnCaptured && txn.Status != payment.TxnAuthorized {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:payment-error",
			"title":      "Payment Not Captured",
			"status":     http.StatusPaymentRequired,
			"error_code": "ERR_PAYMENT_NOT_CAPTURED",
			"detail":     fmt.Sprintf("Payment status '%s' is not authorized or captured", txn.Status),
		})
		return
	}

	if txn.LeaseToken != req.LeaseToken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"type":       "urn:railway:payment-error",
			"title":      "Payment Lease Mismatch",
			"status":     http.StatusForbidden,
			"error_code": "ERR_LEASE_MISMATCH",
			"detail":     "Payment transaction is not linked to the specified segment lease token",
		})
		return
	}

	meta, err := s.leaseMgr.GetLease(r.Context(), "", "", req.LeaseToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("Lease invalid or expired: %v", err), http.StatusGone)
		return
	}

	if err := s.leaseMgr.CommitLease(r.Context(), meta.TrainNumber, meta.RunDate, req.LeaseToken); err != nil {
		http.Error(w, fmt.Sprintf("Commit failed: %v", err), http.StatusInternalServerError)
		return
	}

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

	pnr := fmt.Sprintf("%010d", rand.Int63n(9000000000)+1000000000)

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
		TrainName:       getTrainName(meta.TrainNumber),
		RunDate:         meta.RunDate,
		ClassCode:       meta.ClassCode,
		QuotaCode:       meta.QuotaCode,
		OriginStopIdx:   meta.OriginStopIdx,
		DestStopIdx:     meta.DestStopIdx,
		Status:          "CONFIRMED",
		TotalFare:       baseFare,
		HasFlexiCancel:  req.HasFlexiCancel,
		Allocations:     allocations,
		SignedQRPayload: qrPayload,
		CreatedAt:       time.Now().Unix(),
	}

	s.pnrMu.Lock()
	s.pnrMap[pnr] = record
	s.pnrMu.Unlock()

	if run, exists := s.runs[runKey]; exists {
		for _, a := range allocations {
			run.ConfirmedList = append(run.ConfirmedList, &charting.TicketPassenger{
				PassengerID:     a.PassengerID,
				PNRNumber:       pnr,
				FullName:        a.FullName,
				Status:          charting.StatusCNF,
				CoachID:         a.CoachID,
				BerthNumber:     a.BerthNumber,
				BerthType:       a.BerthType,
				IsSeniorCitizen: a.BerthType == solver.BerthLower,
			})
		}
	}

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

// Charting & Cancellation Endpoints
type CancelRequestBody struct {
	PNRNumber     string                `json:"pnr_number"`
	PassengerID   string                `json:"passenger_id"`
	RefundChannel payment.RefundChannel `json:"refund_channel"`
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CancelRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	s.pnrMu.RLock()
	record, exists := s.pnrMap[req.PNRNumber]
	s.pnrMu.RUnlock()

	if !exists {
		http.Error(w, "PNR not found", http.StatusNotFound)
		return
	}

	runKey := record.TrainNumber + ":" + record.RunDate
	run, exists := s.runs[runKey]
	if !exists {
		http.Error(w, "Train run not found", http.StatusNotFound)
		return
	}

	target, err := s.cascadeEng.HandleCancellation(run, req.PNRNumber, req.PassengerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	baseFarePerPax := record.TotalFare / float64(len(record.Allocations))
	refundAmount := baseFarePerPax
	cancellationFee := 0.0

	if !record.HasFlexiCancel {
		cancellationFee = 120.00
		refundAmount -= cancellationFee
		if refundAmount < 0 {
			refundAmount = 0
		}
	}

	if req.RefundChannel == "" {
		req.RefundChannel = payment.RefundTransitLedger
	}

	// Dispatch refund
	mockTxn := &payment.PaymentTransaction{
		TxnID:     "cancel_rfnd_" + req.PNRNumber,
		UserID:    "user-passenger",
		AmountINR: refundAmount,
		Gateway:   payment.GatewayRazorpay,
		Status:    payment.TxnCaptured,
	}
	refundRes, _ := s.refundDispatcher.DispatchRefund(r.Context(), mockTxn, req.RefundChannel, refundAmount, "Ticket Cancellation")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":              true,
		"pnr_number":           req.PNRNumber,
		"passenger_id":         target.PassengerID,
		"passenger_name":       target.FullName,
		"status":               "CANCELLED",
		"refund_amount_inr":    refundAmount,
		"cancellation_fee_inr": cancellationFee,
		"refund_channel":       req.RefundChannel,
		"refund_result":        refundRes,
		"manifest_summary":     s.cascadeEng.GetManifestSummary(run),
	})
}

func (s *Server) handleForecastWL(w http.ResponseWriter, r *http.Request) {
	wlStr := r.URL.Query().Get("wl")
	capStr := r.URL.Query().Get("capacity")
	hrsStr := r.URL.Query().Get("hours_left")

	wl, _ := strconv.Atoi(wlStr)
	capacity, _ := strconv.Atoi(capStr)
	hours, _ := strconv.ParseFloat(hrsStr, 64)

	if capacity <= 0 {
		capacity = 240
	}
	if hours <= 0 {
		hours = 24
	}

	forecast := s.wlForecaster.PredictClearance(wl, capacity, hours, false)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(forecast)
}

func (s *Server) handleTriggerChart(w http.ResponseWriter, r *http.Request) {
	trainNumber := r.URL.Query().Get("train")
	runDate := r.URL.Query().Get("date")

	if trainNumber == "" {
		trainNumber = "12301"
	}
	if runDate == "" {
		runDate = "20261105"
	}

	runKey := trainNumber + ":" + runDate
	run, exists := s.runs[runKey]
	if !exists {
		http.Error(w, "Run not found", http.StatusNotFound)
		return
	}

	prepared := s.chartDaemon.CheckAndPrepareCharts()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"charts_prepared_count": prepared,
		"charting_state":        run.ChartingState,
		"confirmed_count":       len(run.ConfirmedList),
		"rac_count":             len(run.RACList),
		"wl_count":              len(run.Waitlist),
		"current_booking_seats": run.QuotaPools[charting.QuotaGeneral],
	})
}

func maskPassengerName(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "***"
	}
	var maskedParts []string
	for _, p := range parts {
		runes := []rune(p)
		if len(runes) <= 1 {
			maskedParts = append(maskedParts, string(runes)+"*")
		} else {
			masked := string(runes[0]) + strings.Repeat("*", len(runes)-1)
			maskedParts = append(maskedParts, masked)
		}
	}
	return strings.Join(maskedParts, " ")
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

	// Check if authenticated owner or admin
	isOwner := false
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != "" {
			if _, _, err := s.attestMgr.VerifySessionToken(token); err == nil {
				isOwner = true
			} else {
				adminToken := os.Getenv("ADMIN_TOKEN")
				if adminToken == "" {
					adminToken = "dev_admin_token"
				}
				if token == adminToken {
					isOwner = true
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if isOwner {
		json.NewEncoder(w).Encode(record)
		return
	}

	// VULN-05: Mask passenger names for unauthenticated public PNR lookup
	maskedAllocations := make([]solver.BerthAllocation, len(record.Allocations))
	for i, a := range record.Allocations {
		maskedAllocations[i] = a
		maskedAllocations[i].FullName = maskPassengerName(a.FullName)
	}

	publicRecord := map[string]interface{}{
		"pnr_number":        record.PNRNumber,
		"train_number":      record.TrainNumber,
		"train_name":        record.TrainName,
		"run_date":          record.RunDate,
		"class_code":        record.ClassCode,
		"quota_code":        record.QuotaCode,
		"origin_stop_index": record.OriginStopIdx,
		"dest_stop_index":   record.DestStopIdx,
		"status":            record.Status,
		"total_fare":        record.TotalFare,
		"has_flexi_cancel":  record.HasFlexiCancel,
		"allocations":       maskedAllocations,
		"created_at":        record.CreatedAt,
	}

	json.NewEncoder(w).Encode(publicRecord)
}

// Phase 6 Handlers: RTIS Telemetry, TDR Grievance & DPDP Compliance
func (s *Server) handleTelemetrySubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var pt telemetry.TelemetryPoint
	if err := json.NewDecoder(r.Body).Decode(&pt); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if pt.Timestamp.IsZero() {
		pt.Timestamp = time.Now()
	}
	if err := s.rtisEng.SubmitTelemetry(pt); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ACCEPTED",
		"message": "Telemetry point ingested into stream partition",
	})
}

func (s *Server) handleTelemetryLatest(w http.ResponseWriter, r *http.Request) {
	train := strings.TrimPrefix(r.URL.Path, "/v1/telemetry/latest/")
	pt, exists := s.rtisEng.GetLatestTelemetry(train)
	if !exists {
		http.Error(w, "Telemetry not found for train", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pt)
}

type TDRFileRequest struct {
	PNR         string              `json:"pnr"`
	Passengers  []string            `json:"passengers"`
	TrainNumber string              `json:"train_number"`
	Origin      string              `json:"origin"`
	Dest        string              `json:"dest"`
	TravelDate  string              `json:"travel_date"`
	Reason      grievance.TDRReason `json:"reason"`
	FarePaidINR float64             `json:"fare_paid_inr"`
}

func (s *Server) handleTDRFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req TDRFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var rtisPt *telemetry.TelemetryPoint
	if pt, ok := s.rtisEng.GetLatestTelemetry(req.TrainNumber); ok {
		rtisPt = &pt
	}

	claim, err := s.tdrEng.FileTDR(
		req.PNR,
		req.Passengers,
		req.TrainNumber,
		req.Origin,
		req.Dest,
		req.TravelDate,
		req.Reason,
		req.FarePaidINR,
		rtisPt,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	if claim.Status == grievance.StatusAutoVerified && claim.ApprovedRefundINR > 0 {
		_ = s.tdrEng.MarkRefundDispatched(claim.ClaimID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(claim)
}

func (s *Server) handleTDRStatus(w http.ResponseWriter, r *http.Request) {
	claimID := strings.TrimPrefix(r.URL.Path, "/v1/grievance/tdr/")
	claim, exists := s.tdrEng.GetClaim(claimID)
	if !exists {
		http.Error(w, "TDR Claim not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(claim)
}

type PrivacyReqPayload struct {
	UserID      string           `json:"user_id"`
	RequestType dpdp.RequestType `json:"request_type"`
}

func (s *Server) handlePrivacyRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PrivacyReqPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	claim, err := s.dpdpEng.SubmitPrivacyRequest(req.UserID, req.RequestType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.RequestType == dpdp.RequestRightToErasure {
		_ = s.dpdpEng.ExecuteRightToErasure(claim.RequestID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(claim)
}

func (s *Server) handlePrivacyExport(w http.ResponseWriter, r *http.Request) {
	reqID := strings.TrimPrefix(r.URL.Path, "/v1/privacy/export/")
	data, err := s.dpdpEng.GenerateDataPortabilityExport(reqID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=dpdp_export_%s.json", reqID))
	w.Write(data)
}

func findStaticFile(subpath string) string {
	prefix := ""
	for i := 0; i < 5; i++ {
		candidate := filepath.Join(prefix, subpath)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		prefix = filepath.Join("..", prefix)
	}
	return subpath
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://fonts.gstatic.com https://cdn.tailwindcss.com; img-src 'self' data: https:; connect-src 'self';")
		next.ServeHTTP(w, r)
	})
}

func main() {
	server, err := NewServer()
	if err != nil {
		log.Fatalf("Fatal server init: %v", err)
	}

	server.sweeper.Start()
	defer server.sweeper.Stop()

	server.reconciler.Start(context.Background())
	defer server.reconciler.Stop()

	server.rtisEng.Start(context.Background())
	defer server.rtisEng.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)

	// Ingress & Anti-Bot Endpoints
	mux.HandleFunc("/v1/ingress/lobby/join", server.handleLobbyJoin)
	mux.HandleFunc("/v1/ingress/pow/challenge", server.handlePoWChallenge)
	mux.HandleFunc("/v1/ingress/pow/verify", server.handlePoWVerify)

	// Tiered Identity & Pre-Attestation Endpoints
	mux.HandleFunc("/v1/auth/attest", server.handleAttest)
	mux.HandleFunc("/v1/auth/recover", server.handleRecover)

	// Search & Booking Endpoints
	mux.HandleFunc("/v1/search/trains", server.handleSearch)
	mux.HandleFunc("/v1/booking/hold", server.handleHold)
	mux.HandleFunc("/v1/booking/release", server.handleRelease)
	mux.HandleFunc("/v1/booking/confirm", server.handleConfirm)
	mux.HandleFunc("/v1/booking/cancel", server.handleCancel)
	mux.HandleFunc("/v1/booking/pnr/", server.handlePNRStatus)

	// Payment Orchestrator & Transit Ledger Endpoints
	mux.HandleFunc("/v1/payment/preauth/init", server.handlePaymentInit)
	mux.HandleFunc("/v1/payment/webhook", server.handlePaymentWebhook)
	mux.HandleFunc("/v1/payment/ledger/", server.handleTransitLedger)

	// Charting & Forecasting Endpoints
	mux.HandleFunc("/v1/charting/trigger", server.handleTriggerChart)
	mux.HandleFunc("/v1/forecast/wl", server.handleForecastWL)

	// Phase 6: RTIS Telemetry, TDR Grievance & DPDP Compliance Endpoints
	mux.HandleFunc("/v1/telemetry/submit", server.handleTelemetrySubmit)
	mux.HandleFunc("/v1/telemetry/latest/", server.handleTelemetryLatest)
	mux.HandleFunc("/v1/grievance/tdr/file", server.handleTDRFile)
	mux.HandleFunc("/v1/grievance/tdr/", server.handleTDRStatus)
	mux.HandleFunc("/v1/privacy/request", server.handlePrivacyRequest)
	mux.HandleFunc("/v1/privacy/export/", server.handlePrivacyExport)

	// Frontend PWA, Low-Bandwidth Mode (<50KB), & Helpline 139 IVR
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		switch r.URL.Path {
		case "/":
			if r.URL.Query().Get("page") == "trains" || (r.URL.Query().Get("from") != "" && r.URL.Query().Get("to") != "") {
				http.ServeFile(w, r, findStaticFile("frontend/src/trains.html"))
				return
			}
			http.ServeFile(w, r, findStaticFile("frontend/src/index.html"))
		case "/trains", "/trains/", "/trains.html":
			http.ServeFile(w, r, findStaticFile("frontend/src/trains.html"))
		case "/text":
			http.ServeFile(w, r, findStaticFile("frontend/src/text.html"))
		case "/ivr":
			http.ServeFile(w, r, findStaticFile("frontend/src/ivr.html"))
		case "/i18n.js":
			http.ServeFile(w, r, findStaticFile("frontend/src/i18n.js"))
		case "/manifest.json":
			http.ServeFile(w, r, findStaticFile("frontend/public/manifest.json"))
		case "/sw.js":
			http.ServeFile(w, r, findStaticFile("frontend/public/sw.js"))
		case "/icon.svg":
			http.ServeFile(w, r, findStaticFile("frontend/public/icon.svg"))
		default:
			http.NotFound(w, r)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	httpServer := &http.Server{
		Addr:         ":" + port,
		Handler:      securityHeadersMiddleware(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("[CORE] Railway Revamped Core listening on http://localhost:%s", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failure: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[CORE] Shutting down reservation core gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
}
