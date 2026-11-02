package charting

import (
	"math"
)

type ProbabilityBand string

const (
	BandHigh   ProbabilityBand = "HIGH (>75%)"
	BandMedium ProbabilityBand = "MEDIUM (45-75%)"
	BandLow    ProbabilityBand = "LOW (<45%)"
)

type WaitlistForecast struct {
	WLNumber             int             `json:"wl_number"`
	Probability          float64         `json:"probability"`
	ProbabilityBand      ProbabilityBand `json:"probability_band"`
	ExpectedRepoolBerths int             `json:"expected_repool_berths"`
	ExpectedCancels      int             `json:"expected_cancellations"`
	Recommendation       string          `json:"recommendation"`
}

type BayesianWLForecaster struct {
	defaultCancelRate float64 // ~12% baseline cancellations
	repoolLiftFactor  float64 // ~8% berths released from VIP/HO/Defence
}

func NewBayesianWLForecaster() *BayesianWLForecaster {
	return &BayesianWLForecaster{
		defaultCancelRate: 0.12,
		repoolLiftFactor:  0.08,
	}
}

// PredictClearance calculates Bayesian probability of WL confirmation before departure.
func (f *BayesianWLForecaster) PredictClearance(
	wlNumber int,
	totalCapacity int,
	hoursUntilDeparture float64,
	isTatkal bool,
) WaitlistForecast {
	if wlNumber <= 0 {
		return WaitlistForecast{
			WLNumber:        0,
			Probability:     1.0,
			ProbabilityBand: BandHigh,
			Recommendation:  "Confirmed ticket",
		}
	}

	// 1. Time decay factor: More cancellations happen earlier; Tatkal has negligible cancellation (<2%)
	cancelRate := f.defaultCancelRate
	if isTatkal {
		cancelRate = 0.02
	} else if hoursUntilDeparture > 72 {
		cancelRate = 0.15
	} else if hoursUntilDeparture < 8 {
		cancelRate = 0.03
	}

	expectedCancels := int(math.Round(float64(totalCapacity) * cancelRate))

	// 2. Quota re-pooling lift at T-8 hours
	expectedRepool := 0
	if hoursUntilDeparture >= 8 {
		expectedRepool = int(math.Round(float64(totalCapacity) * f.repoolLiftFactor))
	}

	totalExpectedOpenings := expectedCancels + expectedRepool
	if totalExpectedOpenings < 1 {
		totalExpectedOpenings = 1
	}

	// 3. Sigmoid / Logistic Bayesian estimation
	// If wlNumber <= totalExpectedOpenings, probability is high
	k := 0.25 // steepness
	x0 := float64(totalExpectedOpenings)
	z := k * (x0 - float64(wlNumber))

	// Logistic function: 1 / (1 + e^-z)
	prob := 1.0 / (1.0 + math.Exp(-z))

	// Clamp to [0.01, 0.99]
	if prob > 0.99 {
		prob = 0.99
	}
	if prob < 0.01 {
		prob = 0.01
	}

	var band ProbabilityBand
	var rec string
	if prob >= 0.75 {
		band = BandHigh
		rec = "Strong confirmation odds. Berth expected during T-8h charting."
	} else if prob >= 0.45 {
		band = BandMedium
		rec = "Moderate clearance odds. Consider Vikalp alternate train selection."
	} else {
		band = BandLow
		rec = "Low confirmation chance. Consider Tatkal booking or alternate departure date."
	}

	return WaitlistForecast{
		WLNumber:             wlNumber,
		Probability:          math.Round(prob*100) / 100,
		ProbabilityBand:      band,
		ExpectedRepoolBerths: expectedRepool,
		ExpectedCancels:      expectedCancels,
		Recommendation:       rec,
	}
}
