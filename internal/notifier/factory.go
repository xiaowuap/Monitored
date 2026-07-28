package notifier

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/monitored-io/monitored/internal/extension"
)

func Build(kind string, config, secret map[string]any) (extension.Notifier, error) {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	switch kind {
	case "webhook":
		return Webhook{URL: text(config, "url"), Secret: text(secret, "secret"), Client: httpClient}, require(config, "url")
	case "telegram":
		return Telegram{
			Token: text(secret, "token"), ChatID: text(config, "chatId"),
			BaseURL: text(config, "baseUrl"), Client: httpClient,
		}, requireMerged(config, secret, "chatId", "token")
	case "feishu":
		return Feishu{
			WebhookURL: text(config, "webhookUrl"), Secret: text(secret, "secret"), Client: httpClient,
		}, require(config, "webhookUrl")
	case "bark":
		server := text(config, "server")
		if server == "" {
			server = "https://api.day.app"
		}
		return Bark{Server: server, DeviceKey: text(secret, "deviceKey"), Client: httpClient}, require(secret, "deviceKey")
	case "smtp":
		recipients := stringSlice(config["to"])
		return SMTP{
			Address:  fmt.Sprintf("%s:%d", text(config, "host"), integer(config, "port", 587)),
			Username: text(secret, "username"), Password: text(secret, "password"),
			From: text(config, "from"), To: recipients,
			ImplicitTLS: boolean(config, "implicitTls"),
		}, require(config, "host", "from", "to")
	default:
		return nil, fmt.Errorf("unsupported notifier kind %q", kind)
	}
}

func DecodeSecret(value []byte) (map[string]any, error) {
	if len(value) == 0 {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func text(values map[string]any, key string) string {
	if values == nil || values[key] == nil {
		return ""
	}
	return fmt.Sprint(values[key])
}

func integer(values map[string]any, key string, fallback int) int {
	switch value := values[key].(type) {
	case float64:
		return int(value)
	case string:
		result, err := strconv.Atoi(value)
		if err == nil {
			return result
		}
	}
	return fallback
}

func boolean(values map[string]any, key string) bool {
	result, _ := values[key].(bool)
	return result
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
		return result
	default:
		return nil
	}
}

func require(values map[string]any, keys ...string) error {
	for _, key := range keys {
		if key == "to" {
			if len(stringSlice(values[key])) == 0 {
				return errors.New("notification config requires to")
			}
			continue
		}
		if text(values, key) == "" {
			return fmt.Errorf("notification config requires %s", key)
		}
	}
	return nil
}

func requireMerged(config, secret map[string]any, keys ...string) error {
	for _, key := range keys {
		if text(config, key) == "" && text(secret, key) == "" {
			return fmt.Errorf("notification config requires %s", key)
		}
	}
	return nil
}
