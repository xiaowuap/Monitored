// Package notifier contains the five notification transports supported by
// Monitored. All HTTP notifiers use bounded request bodies and timeouts.
package notifier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/extension"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Webhook struct {
	URL    string
	Secret string
	Client HTTPClient
}

func (w Webhook) Name() string { return "webhook" }

func (w Webhook) Send(ctx context.Context, notification extension.Notification) error {
	body, err := json.Marshal(notification)
	if err != nil {
		return err
	}
	headers := map[string]string{}
	if w.Secret != "" {
		mac := hmac.New(sha256.New, []byte(w.Secret))
		_, _ = mac.Write(body)
		headers["X-Monitored-Signature-256"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	return postJSON(ctx, client(w.Client), w.URL, body, headers)
}

type Telegram struct {
	Token   string
	ChatID  string
	Client  HTTPClient
	BaseURL string
}

func (t Telegram) Name() string { return "telegram" }
func (t Telegram) Send(ctx context.Context, item extension.Notification) error {
	base := strings.TrimRight(t.BaseURL, "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id": t.ChatID, "text": render(item), "disable_web_page_preview": true,
	})
	return postJSON(ctx, client(t.Client), base+"/bot"+url.PathEscape(t.Token)+"/sendMessage", body, nil)
}

type Feishu struct {
	WebhookURL string
	Secret     string
	Client     HTTPClient
}

func (f Feishu) Name() string { return "feishu" }
func (f Feishu) Send(ctx context.Context, item extension.Notification) error {
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": render(item)},
	}
	if f.Secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+f.Secret))
		payload["timestamp"] = timestamp
		payload["sign"] = encodeBase64(mac.Sum(nil))
	}
	body, _ := json.Marshal(payload)
	return postJSON(ctx, client(f.Client), f.WebhookURL, body, nil)
}

type Bark struct {
	Server    string
	DeviceKey string
	Client    HTTPClient
}

func (b Bark) Name() string { return "bark" }
func (b Bark) Send(ctx context.Context, item extension.Notification) error {
	body, _ := json.Marshal(map[string]any{
		"device_key": b.DeviceKey, "title": item.Title, "body": item.Body,
		"group": "Monitored", "level": barkLevel(item.Severity),
	})
	return postJSON(ctx, client(b.Client), strings.TrimRight(b.Server, "/")+"/push", body, nil)
}

type SMTP struct {
	Address            string
	Username           string
	Password           string
	From               string
	To                 []string
	ImplicitTLS        bool
	InsecureSkipVerify bool
}

func (s SMTP) Name() string { return "smtp" }
func (s SMTP) Send(ctx context.Context, item extension.Notification) error {
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil {
		return fmt.Errorf("invalid SMTP address: %w", err)
	}
	message := []byte("From: " + s.From + "\r\n" +
		"To: " + strings.Join(s.To, ", ") + "\r\n" +
		"Subject: " + sanitizeHeader(item.Title) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + item.Body + "\r\n")
	dialer := net.Dialer{Timeout: 15 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return err
	}
	if s.ImplicitTLS {
		connection = tls.Client(connection, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.InsecureSkipVerify}) // #nosec G402 -- explicit channel setting
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer client.Close()
	if !s.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.InsecureSkipVerify}); err != nil { // #nosec G402
				return err
			}
		}
	}
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(s.From); err != nil {
		return err
	}
	for _, recipient := range s.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	return writer.Close()
}

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func SendWithRetry(ctx context.Context, target extension.Notifier, notification extension.Notification, policy RetryPolicy) error {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 5
	}
	if policy.BaseDelay <= 0 {
		policy.BaseDelay = time.Second
	}
	if policy.MaxDelay <= 0 {
		policy.MaxDelay = time.Minute
	}
	var errs []error
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if err := target.Send(ctx, notification); err == nil {
			return nil
		} else {
			errs = append(errs, err)
		}
		if attempt+1 == policy.MaxAttempts {
			break
		}
		delay := policy.BaseDelay << attempt
		if delay > policy.MaxDelay {
			delay = policy.MaxDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(append(errs, ctx.Err())...)
		case <-timer.C:
		}
	}
	return errors.Join(errs...)
}

func postJSON(ctx context.Context, httpClient HTTPClient, endpoint string, body []byte, headers map[string]string) error {
	if len(body) > 1<<20 {
		return errors.New("notification payload exceeds 1 MiB")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("notification returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}

func client(value HTTPClient) HTTPClient {
	if value != nil {
		return value
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func render(item extension.Notification) string {
	return fmt.Sprintf("[%s] %s\n%s", strings.ToUpper(item.Severity), item.Title, item.Body)
}

func barkLevel(severity string) string {
	if severity == "critical" {
		return "critical"
	}
	return "active"
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

func encodeBase64(value []byte) string {
	return base64Raw(value)
}
