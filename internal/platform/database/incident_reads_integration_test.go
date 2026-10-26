//go:build integration

package database

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/knyongki/opsai-incident-platform/internal/device"
	"github.com/knyongki/opsai-incident-platform/internal/incident"
)

func TestIncidentReadsIntegration(t *testing.T) {
	url := os.Getenv("OPSAI_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set OPSAI_TEST_DATABASE_URL to a migrated local test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("fixture transaction failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := tx.Rollback(cleanup); err != nil {
			t.Error("fixture rollback failed")
		}
	}()
	devices := device.NewService(NewDeviceRepository(tx))
	d, err := devices.Create(ctx, device.Input{Name: device.Change[string]{Set: true, Value: "Read integration fixture"}, Type: device.Change[string]{Set: true, Value: "amr"}})
	if err != nil {
		t.Fatal("device fixture failed")
	}
	other, err := devices.Create(ctx, device.Input{Name: device.Change[string]{Set: true, Value: "Other read fixture"}, Type: device.Change[string]{Set: true, Value: "amr"}})
	if err != nil {
		t.Fatal("other device fixture failed")
	}
	repo := NewIncidentRepository(tx)
	svc := incident.NewService(repo)
	base := time.Date(2026, 10, 25, 10, 0, 0, 123456000, time.UTC)
	alarm := "read-fixture-" + integrationID(t)
	statuses := []string{"open", "analyzing", "analyzed", "analysis_failed", "resolved"}
	severities := []string{"low", "medium", "high", "critical", "high"}
	jobs := []string{"pending", "running", "completed", "failed", "completed"}
	apiJobs := []string{"queued", "running", "completed", "failed", "completed"}
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = integrationID(t)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for i, id := range ids {
		c := incident.Creation{ID: id, JobID: integrationID(t), Input: incident.Input{DeviceID: d.ID, Title: "Read fixture", Description: "Complete synthetic description", Severity: severities[i], RawLogs: []string{"log one", "log two"}, AlarmCode: &alarm, OccurredAt: &base}}
		if i == 4 {
			c.AlarmCode = nil
			c.OccurredAt = nil
			c.RawLogs = []string{}
		}
		if _, err := repo.Create(ctx, c); err != nil {
			t.Fatal("incident fixture creation failed")
		}
		created := base
		if i == 0 {
			created = base.Add(time.Second)
		}
		if i == 4 {
			created = base.Add(-time.Second)
		}
		if _, err := tx.Exec(ctx, "UPDATE incidents SET created_at=$2, updated_at=$2, status=$3 WHERE id=$1", id, created, statuses[i]); err != nil {
			t.Fatal("incident fixture state failed")
		}
		if _, err := tx.Exec(ctx, `UPDATE analysis_jobs SET status=$2,
            claim_token=CASE WHEN $2='running' THEN id ELSE NULL END,
            locked_at=CASE WHEN $2='running' THEN clock_timestamp() ELSE NULL END,
            lease_expires_at=CASE WHEN $2='running' THEN clock_timestamp()+interval '1 minute' ELSE NULL END
            WHERE incident_id=$1`, id, jobs[i]); err != nil {
			t.Fatal("job fixture state failed")
		}
	}
	otherIncident, err := svc.Create(ctx, incident.Input{DeviceID: other.ID, Title: "Other device", Description: "Synthetic", Severity: "high", AlarmCode: &alarm})
	if err != nil {
		t.Fatal("other incident fixture failed")
	}

	t.Run("ordering and UUID ties", func(t *testing.T) {
		result, err := svc.List(ctx, incident.ListOptions{Filters: incident.Filters{DeviceID: d.ID}})
		if err != nil || len(result.Items) != 5 {
			t.Fatal("list fixture rows failed")
		}
		for i, row := range result.Items {
			if row.ID != ids[i] || row.AnalysisStatus != apiJobs[i] {
				t.Fatalf("row %d order/status mismatch", i)
			}
		}
		if result.Page.HasMore || result.Page.NextCursor != nil {
			t.Fatal("unexpected next page")
		}
	})
	t.Run("cursor pages", func(t *testing.T) {
		options := incident.ListOptions{Limit: 2, Filters: incident.Filters{DeviceID: d.ID}}
		var seen []string
		for page := 0; page < 4; page++ {
			result, err := svc.List(ctx, options)
			if err != nil {
				t.Fatal("page query failed")
			}
			for _, row := range result.Items {
				seen = append(seen, row.ID)
			}
			if !result.Page.HasMore {
				if result.Page.NextCursor != nil {
					t.Fatal("final cursor should be null")
				}
				break
			}
			if result.Page.NextCursor == nil || len(result.Items) != 2 {
				t.Fatal("page boundary missing")
			}
			options.Cursor = *result.Page.NextCursor
		}
		if !reflect.DeepEqual(seen, ids) {
			t.Fatalf("missing/duplicated or reordered rows: got %v want %v", seen, ids)
		}
	})
	t.Run("filters", func(t *testing.T) {
		cases := []struct {
			f    incident.Filters
			want []string
		}{
			{incident.Filters{DeviceID: other.ID}, []string{otherIncident.ID}},
			{incident.Filters{DeviceID: d.ID, Status: "analyzing"}, []string{ids[1]}},
			{incident.Filters{DeviceID: d.ID, Severity: "high"}, []string{ids[2], ids[4]}},
			{incident.Filters{DeviceID: d.ID, AlarmCode: &alarm}, ids[:4]},
			{incident.Filters{DeviceID: d.ID, Status: "analyzed", Severity: "high", AlarmCode: &alarm}, []string{ids[2]}},
			{incident.Filters{DeviceID: integrationID(t)}, []string{}},
		}
		for _, tc := range cases {
			result, err := svc.List(ctx, incident.ListOptions{Filters: tc.f})
			if err != nil {
				t.Fatal("filtered list failed")
			}
			got := make([]string, 0, len(result.Items))
			for _, row := range result.Items {
				got = append(got, row.ID)
			}
			if !reflect.DeepEqual(got, tc.want) || result.Items == nil {
				t.Fatalf("filter mismatch: %v want %v", got, tc.want)
			}
		}
		// SQL must treat wildcard-looking text literally, not as LIKE patterns.
		wildcard := "%"
		result, err := svc.List(ctx, incident.ListOptions{Filters: incident.Filters{DeviceID: d.ID, AlarmCode: &wildcard}})
		if err != nil || len(result.Items) != 0 {
			t.Fatal("alarm filter is not exact")
		}
		first, err := svc.List(ctx, incident.ListOptions{Limit: 1, Filters: incident.Filters{DeviceID: d.ID, Severity: "high"}})
		if err != nil || !first.Page.HasMore {
			t.Fatal("filtered first page")
		}
		second, err := svc.List(ctx, incident.ListOptions{Limit: 1, Filters: incident.Filters{DeviceID: d.ID, Severity: "high"}, Cursor: *first.Page.NextCursor})
		if err != nil || len(second.Items) != 1 || second.Items[0].ID != ids[4] || second.Page.HasMore {
			t.Fatal("filtered second page")
		}
	})
	t.Run("complete detail and nulls", func(t *testing.T) {
		detail, err := svc.Get(ctx, ids[0])
		if err != nil || detail.DeviceID != d.ID || detail.Title != "Read fixture" || detail.Description != "Complete synthetic description" || !reflect.DeepEqual(detail.RawLogs, []string{"log one", "log two"}) || detail.AlarmCode == nil || *detail.AlarmCode != alarm || detail.OccurredAt == nil || !detail.OccurredAt.Equal(base) || detail.ResolvedAt != nil || detail.AnalysisStatus != "queued" {
			t.Fatal("complete detail mismatch")
		}
		nullable, err := svc.Get(ctx, ids[4])
		if err != nil || nullable.AlarmCode != nil || nullable.OccurredAt != nil || nullable.ResolvedAt != nil || nullable.RawLogs == nil || len(nullable.RawLogs) != 0 {
			t.Fatal("nullable detail mismatch")
		}
		if _, err := tx.Exec(ctx, "UPDATE incidents SET resolved_at=$2 WHERE id=$1", ids[4], base); err != nil {
			t.Fatal("resolved timestamp fixture failed")
		}
		resolved, err := svc.Get(ctx, ids[4])
		if err != nil || resolved.ResolvedAt == nil || !resolved.ResolvedAt.Equal(base) {
			t.Fatal("resolved timestamp missing")
		}
		if _, err := svc.Get(ctx, integrationID(t)); !errors.Is(err, incident.ErrNotFound) {
			t.Fatal("nonexistent incident mapping")
		}
	})
	t.Run("missing job is an invariant error", func(t *testing.T) {
		// Fixture-only mutation, reverted with the surrounding transaction.
		if _, err := tx.Exec(ctx, "DELETE FROM analysis_jobs WHERE incident_id=$1", otherIncident.ID); err != nil {
			t.Fatal("missing-job fixture failed")
		}
		if _, err := svc.Get(ctx, otherIncident.ID); !errors.Is(err, incident.ErrRepository) {
			t.Fatal("missing job hidden in detail")
		}
		if _, err := svc.List(ctx, incident.ListOptions{Filters: incident.Filters{DeviceID: other.ID}}); !errors.Is(err, incident.ErrRepository) {
			t.Fatal("missing job hidden in list")
		}
	})
}
