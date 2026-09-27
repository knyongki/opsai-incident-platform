package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestJSONAndLevelFiltering(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelWarn)
	logger.Info("filtered")
	if output.Len() != 0 {
		t.Fatal("INFO was not filtered")
	}
	logger.Warn("visible", "component", "api")
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["level"] != "WARN" || entry["msg"] != "visible" || entry["component"] != "api" {
		t.Fatalf("unexpected log entry: %v", entry)
	}
}
