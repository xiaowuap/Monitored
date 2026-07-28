package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

const (
	maxWALBytes = int64(1 << 30)
	maxWALAge   = 24 * time.Hour
)

type queuedBatch struct {
	QueuedAt time.Time          `json:"queuedAt"`
	Batch    domain.IngestBatch `json:"batch"`
}

type WAL struct {
	path string
}

type Gap struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Reason string    `json:"reason"`
}

func NewWAL(path string) *WAL {
	return &WAL{path: path}
}

func (w *WAL) Append(batch domain.IngestBatch) error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o750); err != nil && filepath.Dir(w.path) != "." {
		return fmt.Errorf("create WAL directory: %w", err)
	}
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open WAL: %w", err)
	}
	encoder := json.NewEncoder(file)
	encodeErr := encoder.Encode(queuedBatch{QueuedAt: time.Now().UTC(), Batch: batch})
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode WAL entry: %w", encodeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("sync WAL: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close WAL: %w", closeErr)
	}
	return w.prune()
}

func (w *WAL) Replay(send func(domain.IngestBatch) error) (int, error) {
	file, err := os.Open(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open WAL for replay: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	remaining := make([]queuedBatch, 0)
	replayed := 0
	now := time.Now()
	blocked := false
	for scanner.Scan() {
		var entry queuedBatch
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if now.Sub(entry.QueuedAt) > maxWALAge {
			continue
		}
		if blocked {
			remaining = append(remaining, entry)
			continue
		}
		if err := send(entry.Batch); err != nil {
			blocked = true
			remaining = append(remaining, entry)
			continue
		}
		replayed++
	}
	if err := scanner.Err(); err != nil {
		return replayed, fmt.Errorf("scan WAL: %w", err)
	}
	if err := w.replace(remaining); err != nil {
		return replayed, err
	}
	if blocked {
		return replayed, errors.New("WAL replay paused because the server is unavailable")
	}
	return replayed, nil
}

func (w *WAL) Stats() (bytes int64, oldest time.Time, err error) {
	file, err := os.Open(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, time.Time{}, err
	}
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		var entry queuedBatch
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			oldest = entry.QueuedAt
		}
	}
	return info.Size(), oldest, nil
}

func (w *WAL) Gap() (Gap, error) {
	body, err := os.ReadFile(w.path + ".gap.json")
	if errors.Is(err, os.ErrNotExist) {
		return Gap{}, nil
	}
	if err != nil {
		return Gap{}, err
	}
	var gap Gap
	if err := json.Unmarshal(body, &gap); err != nil {
		return Gap{}, err
	}
	return gap, nil
}

func (w *WAL) ClearGap() error {
	if err := os.Remove(w.path + ".gap.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (w *WAL) prune() error {
	info, err := os.Stat(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	file, err := os.Open(w.path)
	if err != nil {
		return err
	}
	defer file.Close()
	targetBytes := maxWALBytes * 9 / 10
	enforceSize := info.Size() > maxWALBytes
	rewrite := enforceSize
	now := time.Now()
	entries := make([]queuedBatch, 0)
	var dropped []queuedBatch
	var retainedBytes int64
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(line) == 0 {
			break
		}
		var entry queuedBatch
		if json.Unmarshal(line, &entry) == nil {
			if now.Sub(entry.QueuedAt) > maxWALAge {
				rewrite = true
				dropped = append(dropped, entry)
				if errors.Is(readErr, io.EOF) {
					break
				}
				continue
			}
			entries = append(entries, entry)
			retainedBytes += int64(len(line))
			for enforceSize && retainedBytes > targetBytes && len(entries) > 1 {
				encoded, _ := json.Marshal(entries[0])
				retainedBytes -= int64(len(encoded) + 1)
				dropped = append(dropped, entries[0])
				entries = entries[1:]
				rewrite = true
			}
		} else {
			rewrite = true
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if !rewrite {
		return nil
	}
	if len(dropped) > 0 {
		reason := "WAL retention exceeded"
		if enforceSize {
			reason = "WAL size limit exceeded"
		}
		if err := w.recordGap(Gap{From: dropped[0].QueuedAt, To: dropped[len(dropped)-1].QueuedAt, Reason: reason}); err != nil {
			return err
		}
	}
	return w.replace(entries)
}

func (w *WAL) recordGap(gap Gap) error {
	if existing, err := w.Gap(); err == nil && !existing.From.IsZero() {
		if existing.From.Before(gap.From) {
			gap.From = existing.From
		}
		if existing.To.After(gap.To) {
			gap.To = existing.To
		}
		gap.Reason = existing.Reason + "; " + gap.Reason
	}
	body, err := json.Marshal(gap)
	if err != nil {
		return err
	}
	return os.WriteFile(w.path+".gap.json", body, 0o600)
}

func (w *WAL) replace(entries []queuedBatch) error {
	if len(entries) == 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clear WAL: %w", err)
		}
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(w.path), ".monitored-wal-*")
	if err != nil {
		return fmt.Errorf("create temporary WAL: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	encoder := json.NewEncoder(temp)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			temp.Close()
			return fmt.Errorf("rewrite WAL: %w", err)
		}
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, w.path); err != nil {
		return fmt.Errorf("replace WAL: %w", err)
	}
	return nil
}
