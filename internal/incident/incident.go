// Package incident owns incident creation rules independently of HTTP and PostgreSQL.
package incident

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid        = errors.New("invalid incident input")
	ErrDeviceNotFound = errors.New("referenced device not found")
	ErrRepository     = errors.New("incident storage failure")
)

type Input struct {
	DeviceID    string
	Title       string
	Description string
	AlarmCode   *string
	Severity    string
	RawLogs     []string
	OccurredAt  *time.Time
}

type Creation struct {
	ID    string
	JobID string
	Input
}

type Result struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	AnalysisStatus string `json:"analysis_status"`
}

// Repository atomically persists an incident and its initial job before returning.
type Repository interface {
	Create(context.Context, Creation) (Result, error)
}

type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(b) == 16
}

func validText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && utf8.RuneCountInString(s) <= max && (!required || strings.TrimSpace(s) != "")
}

func Validate(in Input) error {
	if !validID(in.DeviceID) || !validText(in.Title, 200, true) || !validText(in.Description, 10000, true) {
		return ErrInvalid
	}
	switch in.Severity {
	case "low", "medium", "high", "critical":
	default:
		return ErrInvalid
	}
	if in.AlarmCode != nil && !validText(*in.AlarmCode, 100, false) {
		return ErrInvalid
	}
	if len(in.RawLogs) > 100 {
		return ErrInvalid
	}
	for _, log := range in.RawLogs {
		if !validText(log, 1000, false) {
			return ErrInvalid
		}
	}
	if in.OccurredAt != nil && (in.OccurredAt.Year() < 1 || in.OccurredAt.Year() > 9999) {
		return ErrInvalid
	}
	return nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("cannot generate incident ID")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func (s *Service) Create(ctx context.Context, in Input) (Result, error) {
	if err := Validate(in); err != nil {
		return Result{}, err
	}
	id, err := newID()
	if err != nil {
		return Result{}, err
	}
	jobID, err := newID()
	if err != nil {
		return Result{}, err
	}
	if in.RawLogs == nil {
		in.RawLogs = []string{}
	}
	if in.OccurredAt != nil {
		utc := in.OccurredAt.UTC()
		in.OccurredAt = &utc
	}
	return s.repo.Create(ctx, Creation{ID: id, JobID: jobID, Input: in})
}
