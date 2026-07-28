// Command loadgen drives the public ingestion contract for capacity and
// failure testing. It creates deterministic assets/sensors, then writes at the
// requested sustained sample rate until the duration elapses.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

func main() {
	endpoint := flag.String("endpoint", "http://localhost:8080/api/v1/ingest", "Master ingestion endpoint")
	token := flag.String("token", os.Getenv("MONITORED_AGENT_TOKEN"), "Agent bearer token")
	nodes := flag.Int("nodes", 50, "Number of hosts")
	sensors := flag.Int("sensors", 20_000, "Total sensors")
	rate := flag.Int("rate", 2_000, "Samples per second")
	duration := flag.Duration("duration", 10*time.Minute, "Sustained write duration")
	batchSize := flag.Int("batch", 1_000, "Samples per request")
	flag.Parse()
	if *token == "" || *nodes < 1 || *sensors < 1 || *rate < 1 || *batchSize < 1 || *batchSize > 20_000 {
		log.Fatal("token and positive nodes/sensors/rate/batch are required; batch must be <= 20000")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), *duration+5*time.Minute)
	defer cancel()
	if err := initialize(ctx, client, *endpoint, *token, *nodes, *sensors); err != nil {
		log.Fatal(err)
	}
	started := time.Now()
	deadline := started.Add(*duration)
	interval := time.Duration(float64(time.Second) * float64(*batchSize) / float64(*rate))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sequence := 0
	for time.Now().Before(deadline) {
		<-ticker.C
		batch := domain.IngestBatch{AgentID: "loadgen"}
		now := time.Now().UTC()
		for index := 0; index < *batchSize; index++ {
			sensorIndex := (sequence + index) % *sensors
			value := 40 + float64(sensorIndex%50)/10
			batch.Samples = append(batch.Samples, domain.IngestSample{
				SensorStableID: fmt.Sprintf("load:sensor:%06d", sensorIndex),
				ObservedAt:     now, Value: &value, Source: "loadgen", Quality: "good",
			})
		}
		if err := send(ctx, client, *endpoint, *token, batch); err != nil {
			log.Fatal(err)
		}
		sequence += *batchSize
	}
	elapsed := time.Since(started)
	log.Printf("sent %d samples in %s (%.0f samples/s)", sequence, elapsed.Round(time.Millisecond), float64(sequence)/elapsed.Seconds())
}

func initialize(ctx context.Context, client *http.Client, endpoint, token string, nodes, sensorCount int) error {
	assets := make([]domain.IngestAsset, 0, nodes)
	for index := 0; index < nodes; index++ {
		assets = append(assets, domain.IngestAsset{
			StableID: fmt.Sprintf("load:host:%03d", index), Kind: "host",
			Name: fmt.Sprintf("load-host-%03d", index), Metadata: map[string]any{"fixture": "capacity"},
		})
	}
	if err := send(ctx, client, endpoint, token, domain.IngestBatch{AgentID: "loadgen", Assets: assets}); err != nil {
		return fmt.Errorf("initialize assets: %w", err)
	}
	for offset := 0; offset < sensorCount; offset += 5_000 {
		end := min(offset+5_000, sensorCount)
		batch := domain.IngestBatch{AgentID: "loadgen"}
		for index := offset; index < end; index++ {
			batch.Sensors = append(batch.Sensors, domain.IngestSensor{
				StableID:      fmt.Sprintf("load:sensor:%06d", index),
				AssetStableID: fmt.Sprintf("load:host:%03d", index%nodes),
				Source:        "loadgen", SourcePath: fmt.Sprintf("fixture/%d", index),
				RawName: fmt.Sprintf("temperature_%d", index), Category: "temperature",
				Unit: "Cel", Scale: 1, IntervalSeconds: 10, Quality: "good",
			})
		}
		if err := send(ctx, client, endpoint, token, batch); err != nil {
			return fmt.Errorf("initialize sensors %d-%d: %w", offset, end, err)
		}
	}
	return nil
}

func send(ctx context.Context, client *http.Client, endpoint, token string, batch domain.IngestBatch) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("Master returned %s", response.Status)
	}
	return nil
}
