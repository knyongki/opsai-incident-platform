package incident

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func validInput() Input {
	return Input{DeviceID: "12345678-1234-4234-8234-123456789abc", Title: "Docking failed", Description: "Stopped before docking", Severity: "high"}
}

type fakeRepository struct {
	calls    int
	creation Creation
	err      error
}

func (r *fakeRepository) Create(ctx context.Context, c Creation) (Result, error) {
	r.calls++
	r.creation = c
	return Result{ID: c.ID, Status: "open", AnalysisStatus: "queued"}, r.err
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Input)
	}{
		{"UUID", func(i *Input) { i.DeviceID = "bad" }},
		{"title required", func(i *Input) { i.Title = "  " }},
		{"description required", func(i *Input) { i.Description = "" }},
		{"title length", func(i *Input) { i.Title = strings.Repeat("界", 201) }},
		{"description length", func(i *Input) { i.Description = strings.Repeat("x", 10001) }},
		{"severity", func(i *Input) { i.Severity = "urgent" }},
		{"alarm length", func(i *Input) { s := strings.Repeat("x", 101); i.AlarmCode = &s }},
		{"log count", func(i *Input) { i.RawLogs = make([]string, 101) }},
		{"log length", func(i *Input) { i.RawLogs = []string{strings.Repeat("x", 1001)} }},
		{"NUL", func(i *Input) { i.RawLogs = []string{"a\x00b"} }},
		{"UTF8", func(i *Input) { i.Title = string([]byte{0xff}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.change(&in)
			repo := &fakeRepository{}
			_, err := NewService(repo).Create(context.Background(), in)
			if !errors.Is(err, ErrInvalid) || repo.calls != 0 {
				t.Fatal("invalid input reached repository")
			}
		})
	}
	in := validInput()
	in.Title = strings.Repeat("界", 200)
	in.Description = strings.Repeat("x", 10000)
	in.RawLogs = make([]string, 100)
	for n := range in.RawLogs {
		in.RawLogs[n] = strings.Repeat("x", 1000)
	}
	if err := Validate(in); err != nil {
		t.Fatal("boundary values rejected")
	}
}

func TestServiceCreation(t *testing.T) {
	repo := &fakeRepository{}
	in := validInput()
	occurred := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("offset", 7*3600))
	in.OccurredAt = &occurred
	result, err := NewService(repo).Create(context.Background(), in)
	if err != nil || repo.calls != 1 || result.Status != "open" || result.AnalysisStatus != "queued" {
		t.Fatal("creation failed")
	}
	c := repo.creation
	if !validID(c.ID) || !validID(c.JobID) || c.ID == c.JobID || c.ID[14] != '4' || c.JobID[14] != '4' {
		t.Fatal("invalid generated UUIDs")
	}
	if c.RawLogs == nil || c.OccurredAt.Location() != time.UTC || !c.OccurredAt.Equal(occurred) {
		t.Fatal("normalization failed")
	}
	for _, want := range []error{ErrDeviceNotFound, ErrRepository} {
		repo.err = want
		if _, err := NewService(repo).Create(context.Background(), validInput()); !errors.Is(err, want) {
			t.Fatal("repository error lost")
		}
	}
}
