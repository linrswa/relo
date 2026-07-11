package domain

import (
	"fmt"
	"strings"
)

const (
	MilestoneStatusPlanned = "planned"
	MilestoneStatusMarked  = "marked"
)

// Milestone is a non-task checkpoint. Its live anchors are replaced by
// immutable snapshots when it is marked.
type Milestone struct {
	ID                         string
	Title                      string
	Reason                     string
	Status                     string
	CreationOrder              int
	NextRecommendationSequence int
	MarkSummary                *string
	Reference                  *string
	CreatedAt                  string
	UpdatedAt                  string
	MarkedAt                   *string
	Anchors                    []MilestoneAnchor
	Recommendations            []Recommendation
	Snapshots                  []MilestoneSnapshot
}

type MilestoneAnchor struct {
	MilestoneID string
	TaskID      string
	CreatedAt   string
}

type Recommendation struct {
	MilestoneID string
	ID          string
	Text        string
	Position    int
	CreatedAt   string
	UpdatedAt   string
}

// MilestoneSnapshot preserves a marked milestone's task scope without a
// foreign-key dependency on the mutable task row.
type MilestoneSnapshot struct {
	MilestoneID       string
	TaskID            string
	TaskTitle         string
	Priority          int
	CreationOrder     int
	ScopePosition     int
	IsAnchor          bool
	AttemptNumber     int
	Status            string
	CompletionSummary *string
	TaskUpdatedAt     string
	CapturedAt        string
}

func MilestoneID(seq int) string      { return fmt.Sprintf("MILESTONE-%03d", seq) }
func RecommendationID(seq int) string { return fmt.Sprintf("REC-%03d", seq) }

func IsCanonicalMilestoneID(id string) bool { return isCanonicalID(id, "MILESTONE-") }
func IsCanonicalRecommendationID(id string) bool {
	return isCanonicalID(id, "REC-")
}

func isCanonicalID(id, prefix string) bool {
	suffix := strings.TrimPrefix(id, prefix)
	if suffix == id || len(suffix) < 3 {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func CanModifyMilestone(status string) bool { return status == MilestoneStatusPlanned }
