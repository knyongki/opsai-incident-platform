//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/device"
)

func TestDeviceRepositoryIntegration(t *testing.T) {
	url := os.Getenv("OPSAI_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set OPSAI_TEST_DATABASE_URL to a migrated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin transaction failed")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil {
			t.Error("rollback failed")
		}
	}()
	repo := NewDeviceRepository(tx)
	svc := device.NewService(repo)
	location := "test lab"
	d, err := svc.Create(ctx, device.Input{Name: device.Change[string]{Set: true, Value: "integration-device"}, Type: device.Change[string]{Set: true, Value: "amr"}, Location: device.Change[*string]{Set: true, Value: &location}, Metadata: device.Change[json.RawMessage]{Set: true, Value: json.RawMessage(`{"test":true}`)}})
	if err != nil {
		t.Fatal("create failed")
	}
	got, err := svc.Get(ctx, d.ID)
	if err != nil || got.ID != d.ID || got.Location == nil || *got.Location != location || got.Status != "unknown" || got.CreatedAt.IsZero() {
		t.Fatal("get mismatch")
	}
	rows, err := svc.List(ctx)
	if err != nil {
		t.Fatal("list failed")
	}
	found := false
	for _, row := range rows {
		if row.ID == d.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created device absent")
	}
	updated, err := svc.Update(ctx, d.ID, device.Input{Name: device.Change[string]{Set: true, Value: "updated"}, Location: device.Change[*string]{Set: true}})
	if err != nil || updated.Name != "updated" || updated.Location != nil || updated.Type != d.Type || updated.Status != d.Status || !updated.UpdatedAt.After(d.UpdatedAt) {
		t.Fatal("patch mismatch")
	}
	updated, err = svc.Update(ctx, d.ID, device.Input{Type: device.Change[string]{Set: true, Value: "robot"}, Status: device.Change[string]{Set: true, Value: "online"}, Metadata: device.Change[json.RawMessage]{Set: true, Value: json.RawMessage(`{"version":2}`)}})
	if err != nil || updated.Type != "robot" || updated.Status != "online" || updated.Name != "updated" {
		t.Fatal("second patch mismatch")
	}
	if err := svc.Delete(ctx, d.ID); err != nil {
		t.Fatal("delete failed")
	}
	if _, err := svc.Get(ctx, d.ID); !errors.Is(err, device.ErrNotFound) {
		t.Fatal("get after delete")
	}
	if err := svc.Delete(ctx, d.ID); !errors.Is(err, device.ErrNotFound) {
		t.Fatal("delete missing")
	}
	if _, err := svc.Update(ctx, d.ID, device.Input{Name: device.Change[string]{Set: true, Value: "missing"}}); !errors.Is(err, device.ErrNotFound) {
		t.Fatal("update missing")
	}
	// Isolate the intentional uniqueness violation so the outer transaction can roll back normally.
	duplicate, err := repo.Create(ctx, d)
	if err != nil {
		t.Fatal("recreate failed")
	}
	nested, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal("savepoint failed")
	}
	_, conflict := NewDeviceRepository(nested).Create(ctx, duplicate)
	if err := nested.Rollback(ctx); err != nil {
		t.Fatal("savepoint rollback failed")
	}
	if !errors.Is(conflict, device.ErrConflict) {
		t.Fatal("duplicate did not map to conflict")
	}
}
