package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Event struct {
	Type string         `json:"type"`
	At   time.Time      `json:"at"`
	Data map[string]any `json:"data"`
}

type Broker struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[chan Event]struct{})}
}

func (b *Broker) Publish(event Event) {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "streaming_unavailable", "Streaming is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	channel := make(chan Event, 16)
	b.mu.Lock()
	b.subscribers[channel] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subscribers, channel)
		b.mu.Unlock()
		close(channel)
	}()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-channel:
			body, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, body)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().Unix())
			flusher.Flush()
		}
	}
}
