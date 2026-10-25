package incident

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type readRepository struct {
	Repository
	query  ListQuery
	id     string
	calls  int
	rows   []Summary
	detail Detail
	err    error
	ctx    context.Context
}

func (r *readRepository) List(ctx context.Context, q ListQuery) ([]Summary, error) {
	r.query, r.ctx = q, ctx
	r.calls++
	return r.rows, r.err
}
func (r *readRepository) Get(ctx context.Context, id string) (Detail, error) {
	r.id, r.ctx = id, ctx
	r.calls++
	return r.detail, r.err
}

func TestListDefaultsAndFilters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := &readRepository{}
	svc := NewService(repo)
	got, err := svc.List(ctx, ListOptions{})
	if err != nil || got.Items == nil || len(got.Items) != 0 || got.Page.HasMore || got.Page.NextCursor != nil || repo.query.Limit != 21 || repo.ctx != ctx {
		t.Fatalf("bad default list: %+v, %v", got, err)
	}
	alarm := "DOCKING"
	f := Filters{DeviceID: strings.ToUpper(validInput().DeviceID), Status: "open", Severity: "high", AlarmCode: &alarm}
	_, err = svc.List(ctx, ListOptions{Filters: f, Limit: 100})
	f.DeviceID = strings.ToLower(f.DeviceID)
	if err != nil || !reflect.DeepEqual(repo.query.Filters, f) || repo.query.Limit != 101 {
		t.Fatal("filters not passed to repository")
	}
	for _, status := range []string{"open", "analyzing", "analyzed", "analysis_failed", "resolved"} {
		if _, err := ValidateList(ListOptions{Filters: Filters{Status: status}}); err != nil {
			t.Fatal(status)
		}
	}
	for _, severity := range []string{"low", "medium", "high", "critical"} {
		if _, err := ValidateList(ListOptions{Filters: Filters{Severity: severity}}); err != nil {
			t.Fatal(severity)
		}
	}
	empty := ""
	if _, err := ValidateList(ListOptions{Filters: Filters{AlarmCode: &empty}}); err != nil {
		t.Fatal("empty exact alarm filter rejected")
	}
}

func TestListRejectsInvalidOptions(t *testing.T) {
	longAlarm, nulAlarm, badUTF8 := strings.Repeat("x", 101), "bad\x00", string([]byte{255})
	for _, o := range []ListOptions{
		{Limit: -1}, {Limit: 101}, {Filters: Filters{DeviceID: "bad"}},
		{Filters: Filters{Status: "queued"}}, {Filters: Filters{Severity: "HIGH"}},
		{Filters: Filters{AlarmCode: &longAlarm}}, {Filters: Filters{AlarmCode: &nulAlarm}},
		{Filters: Filters{AlarmCode: &badUTF8}}, {Cursor: "not-a-cursor"},
		{Cursor: strings.Repeat("A", 2049)},
	} {
		repo := &readRepository{}
		if _, err := NewService(repo).List(context.Background(), o); !errors.Is(err, ErrInvalid) || repo.calls != 0 {
			t.Fatalf("invalid options reached storage: %+v", o)
		}
	}
}

func TestCursorPaginationAndFilterBinding(t *testing.T) {
	now := time.Date(2026, 10, 25, 10, 0, 0, 123456000, time.UTC)
	repo := &readRepository{rows: []Summary{
		{ID: "ffffffff-ffff-4fff-8fff-ffffffffffff", CreatedAt: now},
		{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", CreatedAt: now},
		{ID: "11111111-1111-4111-8111-111111111111", CreatedAt: now.Add(-time.Second)},
	}}
	f := Filters{DeviceID: validInput().DeviceID, Severity: "high"}
	svc := NewService(repo)
	page, err := svc.List(context.Background(), ListOptions{Limit: 2, Filters: f})
	if err != nil || len(page.Items) != 2 || !page.Page.HasMore || page.Page.NextCursor == nil {
		t.Fatalf("missing page: %+v %v", page, err)
	}
	cursor := *page.Page.NextCursor
	if len(cursor) > 2048 {
		t.Fatal("oversized cursor")
	}
	repo.rows = repo.rows[2:]
	last, err := svc.List(context.Background(), ListOptions{Limit: 1, Filters: f, Cursor: cursor})
	if err != nil || len(last.Items) != 1 || last.Page.HasMore || last.Page.NextCursor != nil {
		t.Fatalf("bad final page: %+v %v", last, err)
	}
	if repo.query.Before == nil || repo.query.Before.ID != page.Items[1].ID || !repo.query.Before.CreatedAt.Equal(now) {
		t.Fatal("wrong page boundary")
	}
	f.Severity = "low"
	if _, err := svc.List(context.Background(), ListOptions{Filters: f, Cursor: cursor}); !errors.Is(err, ErrInvalid) {
		t.Fatal("changed filters accepted")
	}
	if _, err := svc.List(context.Background(), ListOptions{Cursor: cursor}); !errors.Is(err, ErrInvalid) {
		t.Fatal("omitted filters accepted")
	}
}

func TestMalformedCursorPayloads(t *testing.T) {
	f := Filters{}
	p := cursorPayload{Version: 1, Position: Position{ID: validInput().DeviceID, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, FilterHash: filterHash(f)}
	raw, _ := json.Marshal(p)
	variants := []string{
		string(raw) + ` {}`, strings.Replace(string(raw), `"v":1`, `"v":2`, 1),
		strings.Replace(string(raw), `"v":1`, `"v":1,"v":1`, 1),
		strings.Replace(string(raw), `"v":1`, `"extra":1,"v":1`, 1),
		strings.Replace(string(raw), p.Position.ID, "bad", 1),
		strings.Replace(string(raw), "2026-01-01T00:00:00Z", "0001-01-01T00:00:00Z", 1),
		strings.Replace(string(raw), "2026-01-01T00:00:00Z", "2026-01-01T00:00:00.000000001Z", 1),
		`{}`, `null`,
	}
	for _, raw := range variants {
		value := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, err := decodeCursor(value, f); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted malformed cursor: %s", raw)
		}
	}
}

func TestReadServiceErrorsAndDetail(t *testing.T) {
	repo := &readRepository{detail: Detail{Summary: Summary{ID: validInput().DeviceID, AnalysisStatus: "queued"}, Description: "Complete description"}}
	svc := NewService(repo)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	detail, err := svc.Get(ctx, strings.ToUpper(validInput().DeviceID))
	if err != nil || detail.Description != "Complete description" || detail.RawLogs == nil || repo.id != validInput().DeviceID || repo.ctx != ctx {
		t.Fatal("detail normalization/context")
	}
	calls := repo.calls
	if _, err := svc.Get(ctx, "bad"); !errors.Is(err, ErrInvalid) || repo.calls != calls {
		t.Fatal("invalid ID reached storage")
	}
	for _, e := range []error{ErrNotFound, ErrRepository, context.Canceled} {
		repo.err = e
		if _, err := svc.Get(ctx, validInput().DeviceID); !errors.Is(err, e) {
			t.Fatal("get error lost")
		}
		if _, err := svc.List(ctx, ListOptions{}); !errors.Is(err, e) {
			t.Fatal("list error lost")
		}
	}
}
