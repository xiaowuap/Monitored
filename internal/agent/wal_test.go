package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

func TestWALReplayPreservesUnsentTail(t *testing.T) {
	wal := NewWAL(t.TempDir() + "/queue.ndjson")
	for _, agentID := range []string{"first", "second", "third"} {
		if err := wal.Append(domain.IngestBatch{AgentID: agentID}); err != nil {
			t.Fatalf("Append(%s) error = %v", agentID, err)
		}
	}

	attempts := 0
	replayed, err := wal.Replay(func(batch domain.IngestBatch) error {
		attempts++
		if batch.AgentID == "second" {
			return errors.New("offline")
		}
		return nil
	})
	if err == nil || replayed != 1 || attempts != 2 {
		t.Fatalf("first replay: replayed=%d attempts=%d err=%v", replayed, attempts, err)
	}

	var remaining []string
	replayed, err = wal.Replay(func(batch domain.IngestBatch) error {
		remaining = append(remaining, batch.AgentID)
		return nil
	})
	if err != nil {
		t.Fatalf("second replay error = %v", err)
	}
	if replayed != 2 || len(remaining) != 2 || remaining[0] != "second" || remaining[1] != "third" {
		t.Fatalf("unexpected replay tail: replayed=%d remaining=%v", replayed, remaining)
	}
	bytes, _, err := wal.Stats()
	if err != nil || bytes != 0 {
		t.Fatalf("WAL not cleared: bytes=%d err=%v", bytes, err)
	}
}

func TestWALAppendPrunesEntriesOlderThanRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.ndjson")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	old := queuedBatch{
		QueuedAt: time.Now().Add(-maxWALAge - time.Minute),
		Batch:    domain.IngestBatch{AgentID: "expired"},
	}
	if err := json.NewEncoder(file).Encode(old); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	wal := NewWAL(path)
	if err := wal.Append(domain.IngestBatch{AgentID: "fresh"}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	var replayedIDs []string
	replayed, err := wal.Replay(func(batch domain.IngestBatch) error {
		replayedIDs = append(replayedIDs, batch.AgentID)
		return nil
	})
	if err != nil || replayed != 1 || len(replayedIDs) != 1 || replayedIDs[0] != "fresh" {
		t.Fatalf("replayed=%d ids=%v err=%v", replayed, replayedIDs, err)
	}
	gap, err := wal.Gap()
	if err != nil || gap.From.IsZero() || gap.Reason == "" {
		t.Fatalf("expired WAL must produce a reportable gap: %#v, %v", gap, err)
	}
	if err := wal.ClearGap(); err != nil {
		t.Fatal(err)
	}
}
