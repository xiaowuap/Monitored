package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monitored-io/monitored/internal/config"
	"github.com/monitored-io/monitored/internal/connection"
	"github.com/monitored-io/monitored/internal/extension"
	"github.com/monitored-io/monitored/internal/llm"
	"github.com/monitored-io/monitored/internal/notifier"
	"github.com/monitored-io/monitored/internal/security"
	"github.com/monitored-io/monitored/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.WorkerFromEnvironment()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	connectContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	repository, err := store.Open(connectContext, cfg.DatabaseURL)
	cancel()
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer repository.Close()
	migrateContext, migrateCancel := context.WithTimeout(ctx, 2*time.Minute)
	if err := repository.Migrate(migrateContext); err != nil {
		migrateCancel()
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}
	migrateCancel()
	sealer, sealerErr := security.NewSealer(cfg.MasterKey)
	if sealerErr != nil {
		logger.Warn("notification credentials unavailable", "error", sealerErr)
	}
	connectionPoller := connection.Poller{
		Repository: repository, Sealer: sealer, ProfilesDir: cfg.SNMPProfiles,
		Concurrency: cfg.PollWorkers, Timeout: cfg.PollTimeout,
	}

	run := func() {
		maintenanceContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := repository.RunMaintenance(maintenanceContext); err != nil {
			logger.Error("metric maintenance", "error", err)
		} else {
			logger.Info("metric maintenance completed")
		}
		if err := connectionPoller.Run(maintenanceContext); err != nil {
			logger.Warn("connection polling completed with failures", "error", err)
		}
		runSuggestionJobs(maintenanceContext, repository, cfg, logger)
		runDiagnoses(maintenanceContext, repository, cfg, logger)
		deliveries, err := repository.ClaimNotificationDeliveries(maintenanceContext, 50)
		if err != nil {
			logger.Error("claim notifications", "error", err)
			return
		}
		for _, delivery := range deliveries {
			var secret map[string]any
			if len(delivery.EncryptedSecret) > 0 {
				if sealer == nil {
					_ = repository.FinishNotificationDelivery(maintenanceContext, delivery.ID, sealerErr)
					continue
				}
				plaintext, openErr := sealer.Open(
					delivery.EncryptedSecret,
					"notification:"+delivery.Kind+":"+delivery.ChannelName,
				)
				if openErr != nil {
					_ = repository.FinishNotificationDelivery(maintenanceContext, delivery.ID, openErr)
					continue
				}
				secret, err = notifier.DecodeSecret(plaintext)
				if err != nil {
					_ = repository.FinishNotificationDelivery(maintenanceContext, delivery.ID, err)
					continue
				}
			}
			target, buildErr := notifier.Build(delivery.Kind, delivery.Config, secret)
			if buildErr != nil {
				_ = repository.FinishNotificationDelivery(maintenanceContext, delivery.ID, buildErr)
				continue
			}
			title, body := notificationText(delivery.Payload)
			sendErr := target.Send(maintenanceContext, extension.Notification{
				Title:    title,
				Body:     body,
				Severity: stringValue(delivery.Payload["severity"]),
				DedupKey: delivery.ID, OccurredAt: time.Now().UTC(),
			})
			if finishErr := repository.FinishNotificationDelivery(maintenanceContext, delivery.ID, sendErr); finishErr != nil {
				logger.Error("finish notification", "id", delivery.ID, "error", finishErr)
			}
		}
	}

	run()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func runSuggestionJobs(ctx context.Context, repository *store.Repository, cfg config.Worker, logger *slog.Logger) {
	items, err := repository.ClaimSuggestionJobs(ctx, 5)
	if err != nil {
		logger.Error("claim LLM suggestion jobs", "error", err)
		return
	}
	for _, item := range items {
		request, buildErr := repository.BuildLabelSuggestionRequest(ctx, item)
		if buildErr != nil {
			if finishErr := repository.FinishSuggestionJob(ctx, item, nil, "failed", buildErr); finishErr != nil {
				logger.Error("finish suggestion with invalid evidence", "id", item.ID, "error", finishErr)
			}
			continue
		}
		rawEvidence, _ := json.Marshal(request)
		item.TokenUsage = (len(rawEvidence) + 3) / 4
		item.Context = map[string]any{
			"sensor":          request.Sensor,
			"asset":           request.Asset,
			"adjacentSensors": request.Adjacent,
			"requestContext":  request.Context,
		}
		if item.TokenUsage > cfg.LLMTokenBudget {
			budgetErr := fmt.Errorf("suggestion evidence exceeds the %d token budget", cfg.LLMTokenBudget)
			_ = repository.FinishSuggestionJob(ctx, item, nil, "budget_exceeded", budgetErr)
			continue
		}

		var result llm.LabelResult
		switch item.Provider {
		case "openai-compatible":
			if cfg.OpenAIURL == "" {
				err = fmt.Errorf("MONITORED_OPENAI_BASE_URL is not configured")
			} else {
				result, err = (llm.OpenAICompatible{
					BaseURL: cfg.OpenAIURL, APIKey: cfg.OpenAIKey, Model: item.Model,
				}).SuggestLabel(ctx, request)
			}
		case "ollama":
			if cfg.OllamaURL == "" {
				err = fmt.Errorf("MONITORED_OLLAMA_BASE_URL is not configured")
			} else {
				result, err = (llm.Ollama{BaseURL: cfg.OllamaURL, Model: item.Model}).SuggestLabel(ctx, request)
			}
		default:
			err = fmt.Errorf("unsupported suggestion provider %q", item.Provider)
		}
		if err != nil {
			if finishErr := repository.FinishSuggestionJob(ctx, item, nil, "failed", err); finishErr != nil {
				logger.Error("finish failed suggestion", "id", item.ID, "error", finishErr)
			}
			continue
		}
		suggestion := extension.LabelSuggestion{
			DisplayName: result.DisplayName,
			AssetKind:   result.AssetKind,
			Category:    result.Category,
			Unit:        result.Unit,
			Confidence:  result.Confidence,
			Evidence:    result.Evidence,
		}
		if err := repository.FinishSuggestionJob(ctx, item, &suggestion, "succeeded", nil); err != nil {
			logger.Error("finish LLM suggestion", "id", item.ID, "error", err)
		}
	}
}

func runDiagnoses(ctx context.Context, repository *store.Repository, cfg config.Worker, logger *slog.Logger) {
	items, err := repository.ClaimDiagnoses(ctx, 5)
	if err != nil {
		logger.Error("claim diagnoses", "error", err)
		return
	}
	for _, item := range items {
		request, buildErr := repository.BuildDiagnosisRequest(ctx, item)
		if buildErr != nil {
			if finishErr := repository.FinishDiagnosis(ctx, item, "failed", buildErr); finishErr != nil {
				logger.Error("finish diagnosis with invalid evidence", "id", item.ID, "error", finishErr)
			}
			continue
		}
		rawEvidence, _ := json.Marshal(request)
		item.TokenUsage = (len(rawEvidence) + 3) / 4
		tokenBudget := cfg.LLMTokenBudget
		if requested := intValue(item.Context["tokenBudget"]); requested >= 256 && requested < tokenBudget {
			tokenBudget = requested
		}
		if item.TokenUsage > tokenBudget {
			err := fmt.Errorf("diagnosis evidence exceeds the %d token budget", tokenBudget)
			_ = repository.FinishDiagnosis(ctx, item, "budget_exceeded", err)
			continue
		}
		var result llm.Result
		switch item.Provider {
		case "openai-compatible":
			if cfg.OpenAIURL == "" {
				err = fmt.Errorf("MONITORED_OPENAI_BASE_URL is not configured")
			} else {
				result, err = (llm.OpenAICompatible{
					BaseURL: cfg.OpenAIURL, APIKey: cfg.OpenAIKey, Model: item.Model,
				}).DiagnoseStructured(ctx, request)
			}
		case "ollama":
			if cfg.OllamaURL == "" {
				err = fmt.Errorf("MONITORED_OLLAMA_BASE_URL is not configured")
			} else {
				result, err = (llm.Ollama{BaseURL: cfg.OllamaURL, Model: item.Model}).DiagnoseStructured(ctx, request)
			}
		default:
			err = fmt.Errorf("unsupported diagnosis provider %q", item.Provider)
		}
		if err != nil {
			if finishErr := repository.FinishDiagnosis(ctx, item, "failed", err); finishErr != nil {
				logger.Error("finish failed diagnosis", "id", item.ID, "error", finishErr)
			}
			continue
		}
		item.Summary, item.Hypotheses, item.Evidence = result.Summary, result.Hypotheses, result.Evidence
		item.DataGaps, item.Checks, item.Actions = result.DataGaps, result.Checks, result.Actions
		item.Rollback, item.Confidence = result.Rollback, result.Confidence
		if err := repository.FinishDiagnosis(ctx, item, "succeeded", nil); err != nil {
			logger.Error("finish diagnosis", "id", item.ID, "error", err)
		}
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}

func notificationText(payload map[string]any) (string, string) {
	state := stringValue(payload["state"])
	summary := stringValue(payload["summary"])
	if stringValue(payload["locale"]) == "zh-CN" {
		label := map[string]string{
			"Firing": "告警触发", "Resolved": "告警恢复", "Test": "通知测试",
		}[state]
		if label == "" {
			label = state
		}
		return "Monitored · " + label, summary
	}
	return "Monitored · " + state, summary
}
