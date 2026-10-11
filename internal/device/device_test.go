package device

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeRepo struct {
	calls int
	saved Device
	patch Input
	err   error
}

func (r *fakeRepo) Create(_ context.Context, d Device) (Device, error) {
	r.calls++
	r.saved = d
	return d, r.err
}
func (r *fakeRepo) List(context.Context) ([]Device, error)      { r.calls++; return nil, r.err }
func (r *fakeRepo) Get(context.Context, string) (Device, error) { r.calls++; return r.saved, r.err }
func (r *fakeRepo) Update(_ context.Context, _ string, in Input) (Device, error) {
	r.calls++
	r.patch = in
	return r.saved, r.err
}
func (r *fakeRepo) Delete(context.Context, string) error { r.calls++; return r.err }
func validInput() Input {
	return Input{Name: Change[string]{true, "AMR-07"}, Type: Change[string]{true, "amr"}}
}

const testID = "6c5738ce-b611-4b6f-9154-3abc9f5b8bb4"

func TestValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Input)
	}{
		{"missing name", func(i *Input) { i.Name.Set = false }},
		{"blank name", func(i *Input) { i.Name.Value = "  " }},
		{"long name", func(i *Input) { i.Name.Value = strings.Repeat("a", 201) }},
		{"missing type", func(i *Input) { i.Type.Set = false }},
		{"long type", func(i *Input) { i.Type.Value = strings.Repeat("a", 51) }},
		{"blank status", func(i *Input) { i.Status = Change[string]{true, ""} }},
		{"long status", func(i *Input) { i.Status = Change[string]{true, strings.Repeat("a", 51)} }},
		{"long location", func(i *Input) { s := strings.Repeat("a", 501); i.Location = Change[*string]{true, &s} }},
		{"NUL", func(i *Input) { i.Name.Value = "a\x00" }},
		{"metadata null", func(i *Input) { i.Metadata = Change[json.RawMessage]{true, json.RawMessage(`null`)} }},
		{"metadata array", func(i *Input) { i.Metadata = Change[json.RawMessage]{true, json.RawMessage(`[]`)} }},
		{"metadata NUL", func(i *Input) { i.Metadata = Change[json.RawMessage]{true, json.RawMessage(`{"x":"\u0000"}`)} }},
		{"large metadata", func(i *Input) {
			i.Metadata = Change[json.RawMessage]{true, json.RawMessage(`{"x":"` + strings.Repeat("a", 16384) + `"}`)}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRepo{}
			in := validInput()
			tt.edit(&in)
			_, err := NewService(r).Create(context.Background(), in)
			if !errors.Is(err, ErrInvalid) || r.calls != 0 {
				t.Fatalf("invalid input reached repository: %v", err)
			}
		})
	}
}
func TestCreateDefaults(t *testing.T) {
	r := &fakeRepo{}
	d, err := NewService(r).Create(context.Background(), validInput())
	if err != nil || !ValidID(d.ID) || d.Status != "unknown" || string(d.Metadata) != "{}" {
		t.Fatalf("unexpected defaults: %+v %v", d, err)
	}
}
func TestUpdatePresence(t *testing.T) {
	r := &fakeRepo{}
	s := NewService(r)
	_, err := s.Update(context.Background(), testID, Input{Location: Change[*string]{Set: true}})
	if err != nil || !r.patch.Location.Set || r.patch.Location.Value != nil || r.patch.Name.Set {
		t.Fatal("patch lost field presence")
	}
	_, err = s.Update(context.Background(), testID, Input{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("empty patch accepted")
	}
}
func TestInvalidIDs(t *testing.T) {
	r := &fakeRepo{}
	s := NewService(r)
	for _, id := range []string{"", "bad", "6c5738ce-b611-4b6f-9154-3abc9f5b8bbz", "--5738ce-b611-4b6f-9154-3abc9f5b8bb4"} {
		if _, err := s.Get(context.Background(), id); !errors.Is(err, ErrInvalid) {
			t.Fatal("get ID")
		}
		if _, err := s.Update(context.Background(), id, validInput()); !errors.Is(err, ErrInvalid) {
			t.Fatal("update ID")
		}
		if err := s.Delete(context.Background(), id); !errors.Is(err, ErrInvalid) {
			t.Fatal("delete ID")
		}
	}
	if r.calls != 0 {
		t.Fatal("invalid ID reached repo")
	}
}
func TestRepositoryErrors(t *testing.T) {
	for _, want := range []error{ErrNotFound, ErrConflict, ErrRepository} {
		r := &fakeRepo{err: want}
		s := NewService(r)
		ctx := context.Background()
		_, e1 := s.Create(ctx, validInput())
		_, e2 := s.List(ctx)
		_, e3 := s.Get(ctx, testID)
		_, e4 := s.Update(ctx, testID, validInput())
		e5 := s.Delete(ctx, testID)
		for _, err := range []error{e1, e2, e3, e4, e5} {
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		}
	}
}
func TestEmptyList(t *testing.T) {
	ds, err := NewService(&fakeRepo{}).List(context.Background())
	if err != nil || ds == nil || len(ds) != 0 {
		t.Fatal("list should be empty array")
	}
}
