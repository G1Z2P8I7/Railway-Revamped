package charting

import (
	"testing"
	"time"

	"github.com/railway-revamped/core/pkg/solver"
)

func TestDeterministicCascadeOnCancellation(t *testing.T) {
	engine := NewCascadeEngine()

	run := &ScheduledTrainRun{
		TrainNumber: "12301",
		TrainName:   "Rajdhani Express",
		RunDate:     "20261105",
		QuotaPools:  map[QuotaCode]int{QuotaGeneral: 0},
		ConfirmedList: []*TicketPassenger{
			{PassengerID: "p-cnf-1", PNRNumber: "PNR_1", FullName: "Confirmed Passenger 1", Status: StatusCNF},
			{PassengerID: "p-cnf-2", PNRNumber: "PNR_2", FullName: "Confirmed Passenger 2", Status: StatusCNF},
		},
		RACList: []*TicketPassenger{
			{PassengerID: "p-rac-1", PNRNumber: "PNR_3", FullName: "RAC Passenger 1", Status: StatusRAC, RACNumber: 1},
			{PassengerID: "p-rac-2", PNRNumber: "PNR_4", FullName: "RAC Passenger 2", Status: StatusRAC, RACNumber: 2},
		},
		Waitlist: []*TicketPassenger{
			{PassengerID: "p-wl-1", PNRNumber: "PNR_5", FullName: "Waitlist Passenger 1", Status: StatusWL, WLNumber: 1},
			{PassengerID: "p-wl-2", PNRNumber: "PNR_6", FullName: "Waitlist Passenger 2", Status: StatusWL, WLNumber: 2},
		},
	}

	// 1. Confirmed Passenger 1 cancels
	cancelled, err := engine.HandleCancellation(run, "PNR_1", "p-cnf-1")
	if err != nil {
		t.Fatalf("cancellation failed: %v", err)
	}
	if cancelled.Status != StatusCAN {
		t.Fatalf("expected status CANCELLED, got: %s", cancelled.Status)
	}

	// Verify RAC 1 was promoted to CNF
	if len(run.ConfirmedList) != 2 {
		t.Fatalf("expected 2 confirmed passengers, got: %d", len(run.ConfirmedList))
	}
	promotedCNF := run.ConfirmedList[1]
	if promotedCNF.PassengerID != "p-rac-1" || promotedCNF.Status != StatusCNF {
		t.Fatalf("expected RAC 1 (p-rac-1) promoted to CNF, got: %+v", promotedCNF)
	}

	// Verify WL 1 was promoted to RAC (now RAC 2)
	if len(run.RACList) != 2 {
		t.Fatalf("expected 2 RAC passengers, got: %d", len(run.RACList))
	}
	promotedRAC := run.RACList[1]
	if promotedRAC.PassengerID != "p-wl-1" || promotedRAC.Status != StatusRAC || promotedRAC.RACNumber != 2 {
		t.Fatalf("expected WL 1 (p-wl-1) promoted to RAC 2, got: %+v", promotedRAC)
	}

	// Verify RAC 2 was promoted to RAC 1
	if run.RACList[0].PassengerID != "p-rac-2" || run.RACList[0].RACNumber != 1 {
		t.Fatalf("expected p-rac-2 renumbered to RAC 1, got: %+v", run.RACList[0])
	}

	// Verify remaining waitlist has exactly 1 passenger (p-wl-2 as WL 1)
	if len(run.Waitlist) != 1 || run.Waitlist[0].PassengerID != "p-wl-2" || run.Waitlist[0].WLNumber != 1 {
		t.Fatalf("expected p-wl-2 renumbered to WL 1, got: %+v", run.Waitlist[0])
	}

	t.Logf("Cancellation cascade succeeded: %s", engine.GetManifestSummary(run))
}

func Test8HourChartPreparationDaemon(t *testing.T) {
	// Origin departure scheduled for 6 hours from now (within the 8-hour charting window)
	mockNow := time.Date(2026, 11, 5, 10, 0, 0, 0, time.Local)
	scheduledDeparture := time.Date(2026, 11, 5, 16, 0, 0, 0, time.Local) // T-6h

	daemon := NewChartPreparationDaemon(1*time.Second, func() time.Time { return mockNow })

	coachB1 := solver.NewStandard3ACoach("B1")
	coachB2 := solver.NewStandard3ACoach("B2")

	run := &ScheduledTrainRun{
		TrainNumber:        "12301",
		TrainName:          "Howrah Rajdhani",
		RunDate:            "20261105",
		ScheduledDeparture: scheduledDeparture,
		ChartingState:      ChartPending,
		Coaches:            []*solver.CoachLayout{coachB1, coachB2},
		QuotaPools: map[QuotaCode]int{
			QuotaGeneral:        0, // General full
			QuotaEmergencyHO:    4, // 4 unused VIP berths
			QuotaDefence:        2, // 2 unused Defence berths
			QuotaForeignTourist: 1, // 1 unused Tourist berth
		},
		ConfirmedList: []*TicketPassenger{
			// Family of 2 on PNR_100: Senior Grandmother (72) + Adult (40)
			{PassengerID: "p-fam-1", PNRNumber: "PNR_100", FullName: "Kalyani Devi", Age: 72, Gender: "F", IsSeniorCitizen: true, Status: StatusCNF},
			{PassengerID: "p-fam-2", PNRNumber: "PNR_100", FullName: "Ramesh Sharma", Age: 40, Gender: "M", Status: StatusCNF},
		},
		RACList: []*TicketPassenger{
			{PassengerID: "p-rac-1", PNRNumber: "PNR_200", FullName: "RAC Passenger A", Status: StatusRAC, RACNumber: 1},
			{PassengerID: "p-rac-2", PNRNumber: "PNR_201", FullName: "RAC Passenger B", Status: StatusRAC, RACNumber: 2},
			{PassengerID: "p-rac-3", PNRNumber: "PNR_202", FullName: "RAC Passenger C", Status: StatusRAC, RACNumber: 3},
			{PassengerID: "p-rac-4", PNRNumber: "PNR_203", FullName: "RAC Passenger D", Status: StatusRAC, RACNumber: 4},
		},
		Waitlist: []*TicketPassenger{
			{PassengerID: "p-wl-1", PNRNumber: "PNR_300", FullName: "WL Passenger 1", Status: StatusWL, WLNumber: 1},
		},
	}

	daemon.RegisterTrainRun(run)

	// Execute chart preparation check
	preparedCount := daemon.CheckAndPrepareCharts()
	if preparedCount != 1 {
		t.Fatalf("expected 1 chart prepared, got: %d", preparedCount)
	}

	// 1. Quotas should be swept: 4 (HO) + 2 (DF) + 1 (FT) = 7 berths
	// RAC has 4 passengers, WL has 1 passenger -> total 5 waiting passengers absorbed to CNF!
	// 7 - 5 = 2 unallocated berths remaining -> should trigger CURRENT_BOOKING_OPEN
	if run.ChartingState != ChartCurrentBooking || !run.CurrentBookingOpen {
		t.Fatalf("expected CURRENT_BOOKING_OPEN, got: %s", run.ChartingState)
	}
	if run.QuotaPools[QuotaGeneral] != 2 {
		t.Fatalf("expected 2 berths remaining for Current Booking, got: %d", run.QuotaPools[QuotaGeneral])
	}

	// All 5 waiting passengers should now be CONFIRMED!
	if len(run.ConfirmedList) != 7 { // 2 original + 4 RAC + 1 WL = 7
		t.Fatalf("expected 7 confirmed passengers, got: %d", len(run.ConfirmedList))
	}

	// 2. Physical allocation check: Senior Citizen Kalyani Devi must be assigned Lower Berth (LB)
	var seniorPassenger *TicketPassenger
	for _, p := range run.ConfirmedList {
		if p.PassengerID == "p-fam-1" {
			seniorPassenger = p
			break
		}
	}
	if seniorPassenger == nil || seniorPassenger.BerthType != solver.BerthLower {
		t.Fatalf("senior passenger must be allocated Lower Berth (LB), got: %+v", seniorPassenger)
	}

	t.Logf("8-Hour Chart Preparation Succeeded! Charting State=%s, Current Booking Berths=%d, Senior Berth=%d (%s)",
		run.ChartingState, run.QuotaPools[QuotaGeneral], seniorPassenger.BerthNumber, seniorPassenger.BerthType)
}

func TestBayesianWaitlistPredictor(t *testing.T) {
	forecaster := NewBayesianWLForecaster()

	// Case 1: Early waitlist (WL 3) on a train with 240 capacity, 48 hours to departure
	forecastHigh := forecaster.PredictClearance(3, 240, 48, false)
	if forecastHigh.Probability < 0.75 {
		t.Fatalf("expected high probability for WL 3, got: %f", forecastHigh.Probability)
	}
	if forecastHigh.ProbabilityBand != BandHigh {
		t.Fatalf("expected BandHigh, got: %s", forecastHigh.ProbabilityBand)
	}

	// Case 2: Deep waitlist (WL 80) on a train with 120 capacity, 6 hours to departure (post-charting window)
	forecastLow := forecaster.PredictClearance(80, 120, 6, false)
	if forecastLow.Probability > 0.40 {
		t.Fatalf("expected low probability for WL 80 at T-6h, got: %f", forecastLow.Probability)
	}
	if forecastLow.ProbabilityBand != BandLow {
		t.Fatalf("expected BandLow, got: %s", forecastLow.ProbabilityBand)
	}

	t.Logf("Bayesian Forecast WL 3: %0.2f (%s) - %s", forecastHigh.Probability, forecastHigh.ProbabilityBand, forecastHigh.Recommendation)
	t.Logf("Bayesian Forecast WL 80: %0.2f (%s) - %s", forecastLow.Probability, forecastLow.ProbabilityBand, forecastLow.Recommendation)
}
