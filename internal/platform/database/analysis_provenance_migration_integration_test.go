//go:build integration

package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func migrationDown(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", name))
	if err != nil {
		t.Fatal("read migration failed")
	}
	parts := strings.Split(string(b), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration has no Down section")
	}
	return parts[1]
}

// Migration 00005 adds provenance for real analyzers without changing legacy fake rows.
func TestAnalysisProvenanceMigrationIntegration(t *testing.T) {
	pool, ctx := workerTestPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin migration test failed")
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Error("migration schema cleanup failed")
		}
	}()
	schema := "provenance_test_" + strings.ReplaceAll(integrationID(t), "-", "")
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema); err != nil {
		t.Fatal("isolated schema failed")
	}
	for _, name := range []string{"00001_create_devices.sql", "00002_create_incidents_and_analysis_jobs.sql",
		"00003_add_analysis_job_leases.sql", "00004_create_incident_analyses.sql"} {
		if _, err := tx.Exec(ctx, migrationUp(t, name)); err != nil {
			t.Fatal("baseline migration failed")
		}
	}
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = integrationID(t)
		for _, q := range []string{
			"INSERT INTO devices(id,name,type) VALUES($1,'synthetic','amr')",
			"INSERT INTO incidents(id,device_id,title,description,severity) VALUES($1,$1,'synthetic','synthetic','high')",
		} {
			if _, err := tx.Exec(ctx, q, ids[i]); err != nil {
				t.Fatal("seed failed")
			}
		}
	}
	legacy := integrationID(t)
	if _, err := tx.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result) VALUES($1,$2,1,'fake','{}')`, legacy, ids[0]); err != nil {
		t.Fatal("legacy row seed failed")
	}
	if _, err := tx.Exec(ctx, migrationUp(t, "00005_add_analysis_provenance.sql")); err != nil {
		t.Fatal("provenance migration failed")
	}
	var model, prompt *string
	if err := tx.QueryRow(ctx, "SELECT model_name,prompt_version FROM incident_analyses WHERE id=$1", legacy).Scan(&model, &prompt); err != nil ||
		model != nil || prompt != nil {
		t.Fatal("legacy fake row changed or gained fabricated provenance")
	}

	insert := func(tx pgx.Tx, incident, source string, model, prompt any) error {
		_, err := tx.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result,model_name,prompt_version)
			VALUES($1,$2,1,$3,'{}',$4,$5)`, integrationID(t), incident, source, model, prompt)
		return err
	}
	if err := insert(tx, ids[1], "claude", "claude-opus-5-5", "incident-analysis-v1"); err != nil {
		t.Fatal("valid claude row rejected")
	}
	if err := insert(tx, ids[2], "fake", nil, nil); err != nil {
		t.Fatal("valid fake row rejected")
	}
	for i, bad := range []struct {
		name          string
		source        string
		model, prompt any
	}{
		{"claude without model", "claude", nil, "incident-analysis-v1"},
		{"claude without prompt version", "claude", "claude-opus-5-5", nil},
		{"claude with blank model", "claude", " ", "incident-analysis-v1"},
		{"claude with blank prompt version", "claude", "claude-opus-5-5", ""},
		{"fake with provenance", "fake", "claude-opus-5-5", "incident-analysis-v1"},
		{"unknown source", "real", "model", "v1"},
	} {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal("begin constraint check failed")
		}
		if err := insert(sp, ids[3+i%5], bad.source, bad.model, bad.prompt); err == nil {
			t.Fatalf("%s accepted", bad.name)
		}
		if err := sp.Rollback(ctx); err != nil {
			t.Fatal("rollback constraint check failed")
		}
	}

	t.Run("down restores the fake-only schema when no claude rows exist", func(t *testing.T) {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal("begin down check failed")
		}
		defer func() {
			if err := sp.Rollback(ctx); err != nil {
				t.Error("rollback down check failed")
			}
		}()
		if _, err := sp.Exec(ctx, "DELETE FROM incident_analyses WHERE source='claude'"); err != nil {
			t.Fatal("down fixture cleanup failed")
		}
		if _, err := sp.Exec(ctx, migrationDown(t, "00005_add_analysis_provenance.sql")); err != nil {
			t.Fatal("down migration failed")
		}
		var columns int
		if err := sp.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema=$1 AND table_name='incident_analyses' AND column_name IN ('model_name','prompt_version')`, schema).Scan(&columns); err != nil || columns != 0 {
			t.Fatal("down left provenance columns")
		}
	})
}

// Migration 00006 adds the openai source; every non-fake source needs provenance.
func TestOpenAIProvenanceMigrationIntegration(t *testing.T) {
	pool, ctx := workerTestPool(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin migration test failed")
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Error("migration schema cleanup failed")
		}
	}()
	schema := "openai_test_" + strings.ReplaceAll(integrationID(t), "-", "")
	if _, err := tx.Exec(ctx, "CREATE SCHEMA "+schema+"; SET LOCAL search_path TO "+schema); err != nil {
		t.Fatal("isolated schema failed")
	}
	for _, name := range []string{"00001_create_devices.sql", "00002_create_incidents_and_analysis_jobs.sql",
		"00003_add_analysis_job_leases.sql", "00004_create_incident_analyses.sql", "00005_add_analysis_provenance.sql"} {
		if _, err := tx.Exec(ctx, migrationUp(t, name)); err != nil {
			t.Fatal("baseline migration failed")
		}
	}
	ids := make([]string, 9)
	for i := range ids {
		ids[i] = integrationID(t)
		for _, q := range []string{
			"INSERT INTO devices(id,name,type) VALUES($1,'synthetic','amr')",
			"INSERT INTO incidents(id,device_id,title,description,severity) VALUES($1,$1,'synthetic','synthetic','high')",
		} {
			if _, err := tx.Exec(ctx, q, ids[i]); err != nil {
				t.Fatal("seed failed")
			}
		}
	}
	insert := func(tx pgx.Tx, incident, source string, model, prompt any) error {
		_, err := tx.Exec(ctx, `INSERT INTO incident_analyses(id,incident_id,schema_version,source,result,model_name,prompt_version)
			VALUES($1,$2,1,$3,'{}',$4,$5)`, integrationID(t), incident, source, model, prompt)
		return err
	}
	if insert(tx, ids[0], "fake", nil, nil) != nil || insert(tx, ids[1], "claude", "claude-opus-5-5", "incident-analysis-v1") != nil {
		t.Fatal("legacy rows rejected before 00006")
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatal("begin pre-check failed")
	}
	if insert(sp, ids[2], "openai", "example-model", "incident-analysis-v1") == nil {
		t.Fatal("openai accepted before 00006")
	}
	if err := sp.Rollback(ctx); err != nil {
		t.Fatal("rollback pre-check failed")
	}
	if _, err := tx.Exec(ctx, migrationUp(t, "00006_allow_openai_analyses.sql")); err != nil {
		t.Fatal("openai migration failed")
	}
	if err := insert(tx, ids[2], "openai", "example-model", "incident-analysis-v1"); err != nil {
		t.Fatal("valid openai row rejected")
	}
	for i, bad := range []struct {
		name          string
		source        string
		model, prompt any
	}{
		{"openai without model", "openai", nil, "incident-analysis-v1"},
		{"openai without prompt version", "openai", "example-model", nil},
		{"openai with blank model", "openai", "  ", "incident-analysis-v1"},
		{"claude without model", "claude", nil, "incident-analysis-v1"},
		{"fake with provenance", "fake", "example-model", "incident-analysis-v1"},
		{"unknown source", "real", "model", "v1"},
	} {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal("begin constraint check failed")
		}
		if err := insert(sp, ids[3+i], bad.source, bad.model, bad.prompt); err == nil {
			t.Fatalf("%s accepted", bad.name)
		}
		if err := sp.Rollback(ctx); err != nil {
			t.Fatal("rollback constraint check failed")
		}
	}
	t.Run("down refuses while openai rows exist", func(t *testing.T) {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal("begin down check failed")
		}
		if _, err := sp.Exec(ctx, migrationDown(t, "00006_allow_openai_analyses.sql")); err == nil {
			t.Fatal("down discarded openai provenance")
		}
		if err := sp.Rollback(ctx); err != nil {
			t.Fatal("rollback down check failed")
		}
		sp, err = tx.Begin(ctx)
		if err != nil {
			t.Fatal("begin down check failed")
		}
		defer func() {
			if err := sp.Rollback(ctx); err != nil {
				t.Error("rollback down check failed")
			}
		}()
		if _, err := sp.Exec(ctx, "DELETE FROM incident_analyses WHERE source='openai'"); err != nil {
			t.Fatal("down fixture cleanup failed")
		}
		if _, err := sp.Exec(ctx, migrationDown(t, "00006_allow_openai_analyses.sql")); err != nil {
			t.Fatal("down failed without openai rows")
		}
	})
}
