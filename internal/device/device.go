// Package device owns device data and business rules independently of PostgreSQL and HTTP.
package device

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid    = errors.New("invalid device input")
	ErrNotFound   = errors.New("device not found")
	ErrConflict   = errors.New("device conflict")
	ErrRepository = errors.New("device storage failure")
)

type Device struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      string          `json:"type"`
	Location  *string         `json:"location"`
	Status    string          `json:"status"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Change separates omission from explicit null, needed for nullable location.
type Change[T any] struct {
	Set   bool
	Value T
}
type Input struct {
	Name     Change[string]
	Type     Change[string]
	Location Change[*string]
	Status   Change[string]
	Metadata Change[json.RawMessage]
}

type Repository interface {
	Create(context.Context, Device) (Device, error)
	List(context.Context) ([]Device, error)
	Get(context.Context, string) (Device, error)
	Update(context.Context, string, Input) (Device, error)
	Delete(context.Context, string) error
}

type Service struct{ repo Repository }

func NewService(repo Repository) *Service { return &Service{repo: repo} }

func ValidID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(decoded) == 16
}

func validate(in Input, create bool) error {
	if create && (!in.Name.Set || !in.Type.Set) {
		return ErrInvalid
	}
	if !create && !in.Name.Set && !in.Type.Set && !in.Location.Set && !in.Status.Set && !in.Metadata.Set {
		return ErrInvalid
	}
	for _, field := range []struct {
		set   bool
		value string
		max   int
	}{{in.Name.Set, in.Name.Value, 200}, {in.Type.Set, in.Type.Value, 50}, {in.Status.Set, in.Status.Value, 50}} {
		if field.set && (strings.TrimSpace(field.value) == "" || utf8.RuneCountInString(field.value) > field.max || strings.ContainsRune(field.value, 0) || !utf8.ValidString(field.value)) {
			return ErrInvalid
		}
	}
	if in.Location.Set && in.Location.Value != nil && (utf8.RuneCountInString(*in.Location.Value) > 500 || strings.ContainsRune(*in.Location.Value, 0) || !utf8.ValidString(*in.Location.Value)) {
		return ErrInvalid
	}
	if in.Metadata.Set {
		var object map[string]json.RawMessage
		if len(in.Metadata.Value) > 16*1024 || json.Unmarshal(in.Metadata.Value, &object) != nil || object == nil {
			return ErrInvalid
		}
		// PostgreSQL JSONB cannot represent a NUL character, even when JSON-escaped.
		var value any
		if json.Unmarshal(in.Metadata.Value, &value) != nil || hasNUL(value) {
			return ErrInvalid
		}
	}
	return nil
}
func hasNUL(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.ContainsRune(x, 0)
	case []any:
		for _, e := range x {
			if hasNUL(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range x {
			if strings.ContainsRune(k, 0) || hasNUL(e) {
				return true
			}
		}
	}
	return false
}
func (s *Service) Create(ctx context.Context, in Input) (Device, error) {
	if err := validate(in, true); err != nil {
		return Device{}, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Device{}, errors.New("cannot generate device ID")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	d := Device{ID: id, Name: in.Name.Value, Type: in.Type.Value, Location: in.Location.Value, Status: "unknown", Metadata: json.RawMessage(`{}`)}
	if in.Status.Set {
		d.Status = in.Status.Value
	}
	if in.Metadata.Set {
		d.Metadata = in.Metadata.Value
	}
	return s.repo.Create(ctx, d)
}
func (s *Service) List(ctx context.Context) ([]Device, error) {
	ds, err := s.repo.List(ctx)
	if ds == nil && err == nil {
		ds = []Device{}
	}
	return ds, err
}
func (s *Service) Get(ctx context.Context, id string) (Device, error) {
	if !ValidID(id) {
		return Device{}, ErrInvalid
	}
	return s.repo.Get(ctx, id)
}
func (s *Service) Update(ctx context.Context, id string, in Input) (Device, error) {
	if !ValidID(id) {
		return Device{}, ErrInvalid
	}
	if err := validate(in, false); err != nil {
		return Device{}, err
	}
	return s.repo.Update(ctx, id, in)
}
func (s *Service) Delete(ctx context.Context, id string) error {
	if !ValidID(id) {
		return ErrInvalid
	}
	return s.repo.Delete(ctx, id)
}
