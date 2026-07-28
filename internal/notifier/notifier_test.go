package notifier

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monitored-io/monitored/internal/extension"
)

func TestWebhookSignsExactPayload(t *testing.T) {
	var checked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("secret"))
		_, _ = mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-Monitored-Signature-256") != expected {
			t.Errorf("invalid signature")
		}
		checked = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	err := (Webhook{URL: server.URL, Secret: "secret"}).Send(context.Background(), extension.Notification{
		Title: "Disk hot", Body: "sda reached 55°C", Severity: "critical",
	})
	if err != nil || !checked {
		t.Fatalf("send failed: %v", err)
	}
}

type flakyNotifier struct{ calls atomic.Int32 }

func (f *flakyNotifier) Name() string { return "flaky" }
func (f *flakyNotifier) Send(context.Context, extension.Notification) error {
	if f.calls.Add(1) < 3 {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func TestRetryEventuallySucceeds(t *testing.T) {
	target := &flakyNotifier{}
	err := SendWithRetry(context.Background(), target, extension.Notification{}, RetryPolicy{
		MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond,
	})
	if err != nil || target.calls.Load() != 3 {
		t.Fatalf("retry failed calls=%d err=%v", target.calls.Load(), err)
	}
}
