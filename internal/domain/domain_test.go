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
