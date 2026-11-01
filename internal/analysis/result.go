package analysis

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

const SchemaVersion = 1
const MaxJSONBytes = 64 * 1024

var ErrInvalid = errors.New("invalid analysis result")

type Result struct {
	Summary               string     `json:"summary"`
	PrimarySuspectedCause string     `json:"primary_suspected_cause"`
	ContributingFactors   []string   `json:"contributing_factors"`
	ObservedConditions    []string   `json:"observed_conditions"`
	RecommendedActions    []Action   `json:"recommended_actions"`
	SafetyNotes           []string   `json:"safety_notes"`
	Confidence            float64    `json:"confidence"`
	Evidence              []Evidence `json:"evidence"`
}

type Action struct {
	Order       int    `json:"order"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

type Evidence struct {
	SourceID string `json:"source_id"`
	Claim    string `json:"claim"`
}

// Validate must be called again at persistence boundaries because Result is mutable.
// Valid structure and incident references do not establish factual correctness.
func Validate(r Result, incidentID string) error {
	if !validID(incidentID) || !validText(r.Summary, 2000) || !validText(r.PrimarySuspectedCause, 1000) ||
		!validStrings(r.ContributingFactors, 0) || !validStrings(r.ObservedConditions, 1) || !validStrings(r.SafetyNotes, 1) ||
		r.RecommendedActions == nil || len(r.RecommendedActions) > 10 ||
		r.Evidence == nil || len(r.Evidence) < 1 || len(r.Evidence) > 10 ||
		math.IsNaN(r.Confidence) || math.IsInf(r.Confidence, 0) || r.Confidence < 0 || r.Confidence > 1 {
		return ErrInvalid
	}
	for i, action := range r.RecommendedActions {
		if action.Order != i+1 || !validText(action.Description, 1000) || !validText(action.Reason, 1000) {
			return ErrInvalid
		}
	}
	for _, evidence := range r.Evidence {
		if !validID(evidence.SourceID) || !strings.EqualFold(evidence.SourceID, incidentID) || !validText(evidence.Claim, 1000) {
			return ErrInvalid
		}
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxJSONBytes {
		return ErrInvalid
	}
	return nil
}

func validText(value string, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) &&
		strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= max
}

func validStrings(values []string, min int) bool {
	if values == nil || len(values) < min || len(values) > 10 {
		return false
	}
	for _, value := range values {
		if !validText(value, 1000) {
			return false
		}
	}
	return true
}

func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(decoded) == 16
}
