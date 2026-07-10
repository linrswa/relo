package domain

import "fmt"

const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusPassed  = "passed"
	StatusFailed  = "failed"
)

type Project struct {
	Goal             string
	PRDPath          string
	PRDHash          string
	NextTaskSequence int
	CreatedAt        string
	UpdatedAt        string
}

type Task struct {
	ID                    string
	Title                 string
	Objective             string
	Priority              int
	CreationOrder         int
	Status                string
	AttemptCount          int
	LastFailureReason     *string
	LastCompletionSummary *string
	CreatedAt             string
	UpdatedAt             string
	AcceptanceCriteria    []AcceptanceCriterion
	Dependencies          []Dependency
}

type AcceptanceCriterion struct {
	ID       string
	Text     string
	Position int
}

type Dependency struct {
	TaskID       string
	DependencyID string
	Reason       string
	CreatedAt    string
	Status       string
	Title        string
}

func TaskID(seq int) string { return fmt.Sprintf("TASK-%03d", seq) }
func ACID(seq int) string   { return fmt.Sprintf("AC-%03d", seq) }

func CanModifyDefinition(status string) bool {
	return status == StatusPending || status == StatusFailed
}
func CanDelete(status string) bool { return status == StatusPending || status == StatusFailed }
