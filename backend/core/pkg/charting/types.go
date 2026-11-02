package charting

import (
	"time"

	"github.com/railway-revamped/core/pkg/solver"
)

type BookingStatus string

const (
	StatusCNF BookingStatus = "CNF"
	StatusRAC BookingStatus = "RAC"
	StatusWL  BookingStatus = "WL"
	StatusCAN BookingStatus = "CANCELLED"
)

type QuotaCode string

const (
	QuotaGeneral        QuotaCode = "GN"
	QuotaTatkal         QuotaCode = "TQ"
	QuotaPremiumTatkal  QuotaCode = "PT"
	QuotaSeniorCitizen  QuotaCode = "SS"
	QuotaLadies         QuotaCode = "LD"
	QuotaDefence        QuotaCode = "DF" // Swept at T-8h
	QuotaForeignTourist QuotaCode = "FT" // Swept at T-8h
	QuotaEmergencyHO    QuotaCode = "HO" // High Official / VIP (swept at T-8h)
)

type ChartingState string

const (
	ChartPending       ChartingState = "PENDING"
	ChartPrepared      ChartingState = "CHART_PREPARED"
	ChartCurrentBooking ChartingState = "CURRENT_BOOKING_OPEN"
	ChartFinalized     ChartingState = "FINALIZED"
)

type TicketPassenger struct {
	PassengerID     string               `json:"passenger_id"`
	PNRNumber       string               `json:"pnr_number"`
	FullName        string               `json:"full_name"`
	Age             int                  `json:"age"`
	Gender          string               `json:"gender"`
	IsSeniorCitizen bool                 `json:"is_senior_citizen"`
	Preference      solver.BerthType     `json:"preference"`
	Status          BookingStatus        `json:"status"`
	RACNumber       int                  `json:"rac_number,omitempty"`
	WLNumber        int                  `json:"wl_number,omitempty"`
	CoachID         string               `json:"coach_id,omitempty"`
	BerthNumber     int                  `json:"berth_number,omitempty"`
	BerthType       solver.BerthType     `json:"berth_type,omitempty"`
	AllocatedAt     *time.Time           `json:"allocated_at,omitempty"`
}

type ScheduledTrainRun struct {
	TrainNumber        string
	TrainName          string
	RunDate            string
	ScheduledDeparture time.Time
	OriginStation      string
	DestStation        string
	ChartingState      ChartingState
	ChartingTime       time.Time
	CurrentBookingOpen bool

	// Quota seat allocations: QuotaCode -> available berths
	QuotaPools map[QuotaCode]int

	// Master Passenger Queues
	ConfirmedList []*TicketPassenger
	RACList       []*TicketPassenger
	Waitlist      []*TicketPassenger

	// Coach layouts for physical allocation
	Coaches []*solver.CoachLayout
}
