package solver

import (
	"errors"
	"math"
)

var (
	ErrNoBerthsAvailable = errors.New("no physical berths available in train coach layout")
	ErrInvalidPassengerCount = errors.New("passenger count exceeds bay capacity or limits")
)

// BerthType represents the physical position of a berth
type BerthType string

const (
	BerthLower     BerthType = "LB"
	BerthMiddle    BerthType = "MB"
	BerthUpper     BerthType = "UB"
	BerthSideLower BerthType = "SL"
	BerthSideUpper BerthType = "SU"
)

type PhysicalBerth struct {
	CoachID     string
	BerthNumber int
	BayNumber   int
	BerthType   BerthType
	Occupied    bool
}

type PassengerRequest struct {
	PassengerID     string
	FullName        string
	Age             int
	Gender          string
	IsSeniorCitizen bool
	Preference      BerthType // optional preference
}

type BerthAllocation struct {
	PassengerID string
	FullName    string
	CoachID     string
	BerthNumber int
	BayNumber   int
	BerthType   BerthType
	Status      string // "CNF", "RAC", "WL"
}

// CoachLayout models a standard coach (e.g. 3A coach with 72 berths across 9 bays).
type CoachLayout struct {
	CoachID     string
	ClassCode   string
	TotalBerths int
	Berths      []PhysicalBerth
}

// NewStandard3ACoach creates a realistic 72-berth AC 3-Tier coach.
func NewStandard3ACoach(coachID string) *CoachLayout {
	totalBerths := 72
	berths := make([]PhysicalBerth, totalBerths)

	for i := 0; i < totalBerths; i++ {
		berthNum := i + 1
		bayNum := i / 8
		mod := berthNum % 8

		var bType BerthType
		switch mod {
		case 1, 4:
			bType = BerthLower
		case 2, 5:
			bType = BerthMiddle
		case 3, 6:
			bType = BerthUpper
		case 7:
			bType = BerthSideLower
		case 0:
			bType = BerthSideUpper
		}

		berths[i] = PhysicalBerth{
			CoachID:     coachID,
			BerthNumber: berthNum,
			BayNumber:   bayNum,
			BerthType:   bType,
			Occupied:    false,
		}
	}

	return &CoachLayout{
		CoachID:     coachID,
		ClassCode:   "3A",
		TotalBerths: totalBerths,
		Berths:      berths,
	}
}

// SpatialFamilySolver groups family members on a single PNR in the same coach and bay,
// while guaranteeing lower berths for seniors.
type SpatialFamilySolver struct{}

func NewSpatialFamilySolver() *SpatialFamilySolver {
	return &SpatialFamilySolver{}
}

func (s *SpatialFamilySolver) AllocateFamily(coaches []*CoachLayout, passengers []PassengerRequest) ([]BerthAllocation, error) {
	if len(passengers) == 0 {
		return nil, nil
	}
	if len(passengers) > 6 {
		return nil, ErrInvalidPassengerCount
	}

	// 1. Separate seniors vs general family members
	var seniorIdxs []int
	var generalIdxs []int
	for i, p := range passengers {
		if p.IsSeniorCitizen || p.Age >= 60 {
			seniorIdxs = append(seniorIdxs, i)
		} else {
			generalIdxs = append(generalIdxs, i)
		}
	}

	// 2. Search for the optimal coach and bay with the lowest cohesion cost
	bestCoachIdx := -1
	bestCost := math.MaxFloat64
	var bestAssignment []int // berth indices within the chosen coach

	for cIdx, coach := range coaches {
		// Group available berths by bay
		bayToFreeBerths := make(map[int][]int)
		for bIdx, b := range coach.Berths {
			if !b.Occupied {
				bayToFreeBerths[b.BayNumber] = append(bayToFreeBerths[b.BayNumber], bIdx)
			}
		}

		// Try each bay as an anchor
		for bay := 0; bay < (coach.TotalBerths / 8); bay++ {
			availableInAndNearBay := findNearbyAvailableBerths(coach, bay, len(passengers))
			if len(availableInAndNearBay) < len(passengers) {
				continue
			}

			// Score this candidate assignment
			cost, assignment := scoreCandidateAssignment(coach, availableInAndNearBay, passengers, seniorIdxs, generalIdxs, bay)
			if cost < bestCost {
				bestCost = cost
				bestCoachIdx = cIdx
				bestAssignment = assignment
			}
		}
	}

	if bestCoachIdx == -1 || len(bestAssignment) < len(passengers) {
		return nil, ErrNoBerthsAvailable
	}

	// 3. Commit allocations
	chosenCoach := coaches[bestCoachIdx]
	allocations := make([]BerthAllocation, len(passengers))

	for i, berthIdx := range bestAssignment {
		b := &chosenCoach.Berths[berthIdx]
		b.Occupied = true
		p := passengers[i]

		allocations[i] = BerthAllocation{
			PassengerID: p.PassengerID,
			FullName:    p.FullName,
			CoachID:     chosenCoach.CoachID,
			BerthNumber: b.BerthNumber,
			BayNumber:   b.BayNumber,
			BerthType:   b.BerthType,
			Status:      "CNF",
		}
	}

	return allocations, nil
}

func findNearbyAvailableBerths(coach *CoachLayout, centerBay, needed int) []int {
	var candidates []int
	// Prioritize center bay, then distance 1, 2, etc.
	maxBays := coach.TotalBerths / 8
	for distance := 0; distance < maxBays; distance++ {
		baysToCheck := []int{centerBay + distance}
		if distance > 0 {
			baysToCheck = append(baysToCheck, centerBay - distance)
		}

		for _, b := range baysToCheck {
			if b >= 0 && b < maxBays {
				for bIdx, berth := range coach.Berths {
					if berth.BayNumber == b && !berth.Occupied {
						candidates = append(candidates, bIdx)
						if len(candidates) >= needed*2 {
							return candidates
						}
					}
				}
			}
		}
	}
	return candidates
}

func scoreCandidateAssignment(
	coach *CoachLayout,
	candidateBerthIdxs []int,
	passengers []PassengerRequest,
	seniorIdxs, generalIdxs []int,
	centerBay int,
) (float64, []int) {
	if len(candidateBerthIdxs) < len(passengers) {
		return math.MaxFloat64, nil
	}

	// Try to match seniors to Lower Berths first
	assigned := make([]int, len(passengers))
	usedCandidate := make(map[int]bool)

	cost := 0.0

	// Assign seniors
	for _, sIdx := range seniorIdxs {
		bestCandidate := -1
		for _, cIdx := range candidateBerthIdxs {
			if usedCandidate[cIdx] {
				continue
			}
			b := coach.Berths[cIdx]
			if b.BerthType == BerthLower {
				bestCandidate = cIdx
				break
			}
		}
		// If no LB available, pick the closest available
		if bestCandidate == -1 {
			cost += 500.0 // penalty for missing lower berth for senior
			for _, cIdx := range candidateBerthIdxs {
				if !usedCandidate[cIdx] {
					bestCandidate = cIdx
					break
				}
			}
		}
		if bestCandidate == -1 {
			return math.MaxFloat64, nil
		}
		usedCandidate[bestCandidate] = true
		assigned[sIdx] = bestCandidate
		cost += math.Abs(float64(coach.Berths[bestCandidate].BayNumber - centerBay)) * 10.0
	}

	// Assign general passengers
	for _, gIdx := range generalIdxs {
		bestCandidate := -1
		for _, cIdx := range candidateBerthIdxs {
			if !usedCandidate[cIdx] {
				bestCandidate = cIdx
				break
			}
		}
		if bestCandidate == -1 {
			return math.MaxFloat64, nil
		}
		usedCandidate[bestCandidate] = true
		assigned[gIdx] = bestCandidate
		cost += math.Abs(float64(coach.Berths[bestCandidate].BayNumber - centerBay)) * 10.0
	}

	return cost, assigned
}
