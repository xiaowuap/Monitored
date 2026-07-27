package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

func NewClient(serverURL, token string) *Client {
	return &Client{
		endpoint: strings.TrimRight(serverURL, "/") + "/api/v1/ingest",
		token:    token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) Send(ctx context.Context, batch domain.IngestBatch) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("encode ingest batch: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ingest request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send ingest batch: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("ingest returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}
