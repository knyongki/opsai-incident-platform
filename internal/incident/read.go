package incident

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Summary deliberately excludes description and raw logs.
type Summary struct {
	ID             string     `json:"id"`
	DeviceID       string     `json:"device_id"`
	Title          string     `json:"title"`
	AlarmCode      *string    `json:"alarm_code"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	AnalysisStatus string     `json:"analysis_status"`
	OccurredAt     *time.Time `json:"occurred_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
type Detail struct {
	Summary
	Description string     `json:"description"`
	RawLogs     []string   `json:"raw_logs"`
	ResolvedAt  *time.Time `json:"resolved_at"`
}
type Filters struct {
	DeviceID  string  `json:"device_id"`
	Status    string  `json:"status"`
	Severity  string  `json:"severity"`
	AlarmCode *string `json:"alarm_code"`
}
type ListOptions struct {
	Filters
	Limit  int
	Cursor string
}
type Position struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// ListQuery is validated by the service; Limit includes the look-ahead row.
type ListQuery struct {
	Filters
	Limit  int
	Before *Position
}
type Page struct {
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}
type ListResult struct {
	Items []Summary `json:"items"`
	Page  Page      `json:"page"`
}
type cursorPayload struct {
	Version    int      `json:"v"`
	Position   Position `json:"position"`
	FilterHash string   `json:"filters"`
}

func ValidID(id string) bool { return validID(id) }

func validateFilters(f Filters) error {
	if f.DeviceID != "" && !validID(f.DeviceID) {
		return ErrInvalid
	}
	if f.Status != "" {
		switch f.Status {
		case "open", "analyzing", "analyzed", "analysis_failed", "resolved":
		default:
			return ErrInvalid
		}
	}
	if f.Severity != "" {
		switch f.Severity {
		case "low", "medium", "high", "critical":
		default:
			return ErrInvalid
		}
	}
	if f.AlarmCode != nil && !validText(*f.AlarmCode, 100, false) {
		return ErrInvalid
	}
	return nil
}
func filterHash(f Filters) string {
	// Filters contains only strings/pointers to strings, so marshaling cannot fail.
	data, _ := json.Marshal(f)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func encodeCursor(p Position, f Filters) (string, error) {
	data, err := json.Marshal(cursorPayload{Version: 1, Position: p, FilterHash: filterHash(f)})
	if err != nil {
		return "", ErrRepository
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func decodeCursor(value string, f Filters) (*Position, error) {
	if len(value) > 2048 {
		return nil, ErrInvalid
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, ErrInvalid
	}
	var payload cursorPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	p := payload.Position
	if payload.Version != 1 || payload.FilterHash != filterHash(f) || !validID(p.ID) ||
		p.CreatedAt.IsZero() || p.CreatedAt.Year() < 1 || p.CreatedAt.Year() > 9999 || p.CreatedAt.Nanosecond()%1000 != 0 {
		return nil, ErrInvalid
	}
	// Canonical form also rejects duplicate keys and alternate encodings.
	canonical, err := encodeCursor(p, f)
	if err != nil || canonical != value {
		return nil, ErrInvalid
	}
	return &p, nil
}

// ValidateList normalizes UUID spelling so equivalent filters share a cursor.
func ValidateList(options ListOptions) (ListOptions, error) {
	if options.Limit == 0 {
		options.Limit = 20
	}
	if options.Limit < 1 || options.Limit > 100 {
		return options, ErrInvalid
	}
	options.DeviceID = strings.ToLower(options.DeviceID)
	if err := validateFilters(options.Filters); err != nil {
		return options, err
	}
	if options.Cursor != "" {
		if _, err := decodeCursor(options.Cursor, options.Filters); err != nil {
			return options, err
		}
	}
	return options, nil
}
func (s *Service) List(ctx context.Context, options ListOptions) (ListResult, error) {
	options, err := ValidateList(options)
	if err != nil {
		return ListResult{}, err
	}
	q := ListQuery{Filters: options.Filters, Limit: options.Limit + 1}
	if options.Cursor != "" {
		q.Before, err = decodeCursor(options.Cursor, options.Filters)
		if err != nil {
			return ListResult{}, err
		}
	}
	rows, err := s.repo.List(ctx, q)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{Items: make([]Summary, 0, options.Limit)}
	result.Page.HasMore = len(rows) > options.Limit
	if result.Page.HasMore {
		rows = rows[:options.Limit]
	}
	result.Items = append(result.Items, rows...)
	if result.Page.HasMore {
		last := rows[len(rows)-1]
		cursor, err := encodeCursor(Position{CreatedAt: last.CreatedAt, ID: last.ID}, options.Filters)
		if err != nil {
			return ListResult{}, err
		}
		result.Page.NextCursor = &cursor
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	if !validID(id) {
		return Detail{}, ErrInvalid
	}
	result, err := s.repo.Get(ctx, strings.ToLower(id))
	if err != nil {
		return Detail{}, err
	}
	if result.RawLogs == nil {
		result.RawLogs = []string{}
	}
	return result, nil
}
