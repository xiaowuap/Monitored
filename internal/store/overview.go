package store

import (
	"context"
	"fmt"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

func (r *Repository) Overview(ctx context.Context) (domain.Overview, error) {
	overview := domain.Overview{
		GeneratedAt:   time.Now().UTC(),
		StorageStatus: "healthy",
		QueueStatus:   "healthy",
		AssetsByKind:  map[string]int{},
	}
	err := r.pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE connection = 'online'),
			coalesce(round(100 - (
				count(*) FILTER (WHERE health = 'critical') * 12 +
				count(*) FILTER (WHERE health = 'warning') * 4 +
				count(*) FILTER (WHERE connection = 'offline') * 8
			)::numeric), 0)
		FROM assets`).Scan(&overview.AssetCount, &overview.OnlineAssetCount, &overview.HealthScore)
	if err != nil {
		return domain.Overview{}, fmt.Errorf("query overview asset counts: %w", err)
	}
	err = r.pool.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE display_alias IS NULL),
			(SELECT count(*) FROM alerts WHERE state = 'Firing'),
			(SELECT count(*) FROM action_requests WHERE state = 'pending'),
			coalesce((
				SELECT round(count(*) / 60.0)
				FROM metric_samples
				WHERE recorded_at >= now() - interval '1 minute'
			), 0)
		FROM sensor_definitions`).Scan(
		&overview.SensorCount,
		&overview.UnknownCount,
		&overview.FiringAlertCount,
		&overview.PendingActionCount,
		&overview.IngestRate,
	)
	if err != nil {
		return domain.Overview{}, fmt.Errorf("query overview sensor counts: %w", err)
	}

	rows, err := r.pool.Query(ctx, `SELECT kind, count(*) FROM assets GROUP BY kind ORDER BY kind`)
	if err != nil {
		return domain.Overview{}, fmt.Errorf("query asset kinds: %w", err)
	}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			rows.Close()
			return domain.Overview{}, fmt.Errorf("scan asset kind: %w", err)
		}
		overview.AssetsByKind[kind] = count
	}
	rows.Close()

	trendRows, err := r.pool.Query(ctx, `
		WITH buckets AS (
			SELECT
				date_bin('30 minutes', sm.observed_at, timestamptz '2001-01-01') bucket,
				avg(sm.value) FILTER (WHERE sd.category = 'temperature') average_temp,
				max(sm.value) FILTER (WHERE sd.category = 'temperature') maximum_temp,
				sum(sm.value) FILTER (WHERE sd.category = 'power') total_power
			FROM metric_samples sm
			JOIN metric_series series ON series.id = sm.series_id
			JOIN sensor_definitions sd ON sd.id = series.sensor_id
			WHERE sm.observed_at >= now() - interval '24 hours'
			  AND sm.value IS NOT NULL
			  AND sd.category IN ('temperature', 'power')
			GROUP BY bucket
		)
		SELECT bucket, coalesce(average_temp, 0), coalesce(maximum_temp, 0), coalesce(total_power, 0)
		FROM buckets
		ORDER BY bucket`)
	if err != nil {
		return domain.Overview{}, fmt.Errorf("query overview trend: %w", err)
	}
	for trendRows.Next() {
		var point domain.OverviewPoint
		if err := trendRows.Scan(&point.At, &point.Average, &point.Maximum, &point.PowerW); err != nil {
			trendRows.Close()
			return domain.Overview{}, fmt.Errorf("scan overview trend: %w", err)
		}
		overview.TemperatureTrend = append(overview.TemperatureTrend, point)
	}
	trendRows.Close()

	overview.RecentAlerts, err = r.ListAlerts(ctx, true, 5)
	if err != nil {
		return domain.Overview{}, err
	}
	assets, err := r.ListAssets(ctx, 30)
	if err != nil {
		return domain.Overview{}, err
	}
	for _, asset := range assets {
		if asset.Health == "critical" || asset.Health == "warning" || asset.Summary.HighestTempC != nil {
			overview.HotAssets = append(overview.HotAssets, asset)
		}
		if len(overview.HotAssets) == 5 {
			break
		}
	}
	return overview, nil
}
