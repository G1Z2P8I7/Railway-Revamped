package solver

import (
	"testing"
)

func TestFamilyCohesionAndSeniorPriority(t *testing.T) {
	coachB1 := NewStandard3ACoach("B1")
	coachB2 := NewStandard3ACoach("B2")
	coaches := []*CoachLayout{coachB1, coachB2}

	solver := NewSpatialFamilySolver()

	// Family of 4: Grandmother (71), Father (45), Mother (42), Child (10)
	passengers := []PassengerRequest{
		{PassengerID: "p1", FullName: "Kalyani Devi", Age: 71, Gender: "F", IsSeniorCitizen: true},
		{PassengerID: "p2", FullName: "Rajesh Kumar", Age: 45, Gender: "M", IsSeniorCitizen: false},
		{PassengerID: "p3", FullName: "Anita Kumar", Age: 42, Gender: "F", IsSeniorCitizen: false},
		{PassengerID: "p4", FullName: "Rohan Kumar", Age: 10, Gender: "M", IsSeniorCitizen: false},
	}

	allocations, err := solver.AllocateFamily(coaches, passengers)
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}

	if len(allocations) != 4 {
		t.Fatalf("expected 4 allocations, got: %d", len(allocations))
	}

	// 1. Verify all 4 passengers are allocated to the SAME coach
	coachID := allocations[0].CoachID
	bayNumber := allocations[0].BayNumber
	for _, a := range allocations {
		if a.CoachID != coachID {
			t.Fatalf("family split across different coaches: %s vs %s", a.CoachID, coachID)
		}
		// They should be in the exact same bay or adjacent bay
		if a.BayNumber != bayNumber && a.BayNumber != bayNumber+1 && a.BayNumber != bayNumber-1 {
			t.Fatalf("family split across distant bays: %d vs %d", a.BayNumber, bayNumber)
		}
	}

	// 2. Verify Senior Citizen got Lower Berth (LB)
	seniorAlloc := allocations[0]
	if seniorAlloc.BerthType != BerthLower {
		t.Fatalf("senior citizen must be assigned Lower Berth (LB), got: %s (Berth %d)", seniorAlloc.BerthType, seniorAlloc.BerthNumber)
	}

	t.Logf("Family allocated to Coach %s, Bay %d:", coachID, bayNumber)
	for _, a := range allocations {
		t.Logf("  - %s: Berth %d (%s), Status: %s", a.FullName, a.BerthNumber, a.BerthType, a.Status)
	}
}
