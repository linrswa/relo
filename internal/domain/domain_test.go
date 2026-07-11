package domain

import "testing"

func TestGeneratedIDs(t *testing.T) {
	if got := TaskID(7); got != "TASK-007" {
		t.Fatalf("TaskID(7) = %s", got)
	}
	if got := ACID(12); got != "AC-012" {
		t.Fatalf("ACID(12) = %s", got)
	}
}

func TestMilestoneIDsAndCanonicalValidation(t *testing.T) {
	if got := MilestoneID(7); got != "MILESTONE-007" {
		t.Fatalf("MilestoneID(7) = %s", got)
	}
	if got := RecommendationID(1000); got != "REC-1000" {
		t.Fatalf("RecommendationID(1000) = %s", got)
	}
	for _, id := range []string{"MILESTONE-001", "MILESTONE-000", "MILESTONE-1000"} {
		if !IsCanonicalMilestoneID(id) {
			t.Errorf("milestone ID %q should be canonical", id)
		}
	}
	for _, id := range []string{"MILESTONE-1", "MILESTONE-01", "MILESTONE--01", "MILESTONE-abc", "MILESTONE-", "milestone-001"} {
		if IsCanonicalMilestoneID(id) {
			t.Errorf("milestone ID %q should not be canonical", id)
		}
	}
	for _, id := range []string{"REC-001", "REC-1000"} {
		if !IsCanonicalRecommendationID(id) {
			t.Errorf("recommendation ID %q should be canonical", id)
		}
	}
	for _, id := range []string{"REC-01", "REC--01", "REC-a01", "REC-"} {
		if IsCanonicalRecommendationID(id) {
			t.Errorf("recommendation ID %q should not be canonical", id)
		}
	}
}

func TestMilestoneMutationRestrictions(t *testing.T) {
	if !CanModifyMilestone(MilestoneStatusPlanned) {
		t.Fatal("planned milestone should be mutable")
	}
	if CanModifyMilestone(MilestoneStatusMarked) {
		t.Fatal("marked milestone should be immutable")
	}
}

func TestMutationRestrictions(t *testing.T) {
	for _, status := range []string{StatusPending, StatusFailed} {
		if !CanModifyDefinition(status) || !CanDelete(status) {
			t.Fatalf("%s should be mutable/deletable", status)
		}
	}
	for _, status := range []string{StatusRunning, StatusPassed} {
		if CanModifyDefinition(status) || CanDelete(status) {
			t.Fatalf("%s should not be mutable/deletable", status)
		}
	}
}
