package collector

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monitored-io/monitored/internal/domain"
)

type ZFS struct {
	Host    string
	SysRoot string
	Runner  Runner
}

func (c ZFS) Name() string            { return "zfs" }
func (c ZFS) Interval() time.Duration { return 30 * time.Second }
func (c ZFS) Capability(context.Context) Capability {
	if _, err := c.runner().LookPath("zpool"); err != nil {
		return Capability{Name: c.Name(), State: "unavailable", Detail: "zpool unavailable"}
	}
	return Capability{Name: c.Name(), State: "available", Detail: "Pool/vdev health and errors, scrub/resilver state, datasets, snapshots and ARC/L2ARC"}
}

func (c ZFS) Collect(ctx context.Context) (domain.IngestBatch, error) {
	result, err := c.runner().Run(ctx, "zpool", "list", "-Hp", "-o", "name,size,alloc,free,health,fragmentation")
	if err != nil && len(result.Stdout) == 0 {
		return domain.IngestBatch{AgentID: c.Host}, err
	}
	batch, parseErr := parseZPoolList(result.Stdout, c.Host)
	if parseErr != nil {
		return batch, parseErr
	}
	if status, statusErr := c.runner().Run(ctx, "zpool", "status", "-P", "-L"); statusErr == nil || len(status.Stdout) > 0 {
		batch = Merge(batch, parseZPoolStatus(status.Stdout, c.Host))
	}
	if _, lookErr := c.runner().LookPath("zfs"); lookErr == nil {
		if datasets, datasetErr := c.runner().Run(
			ctx, "zfs", "list", "-Hp", "-t", "filesystem,volume",
			"-o", "name,type,used,available,refer,quota,mountpoint",
		); datasetErr == nil || len(datasets.Stdout) > 0 {
			batch = Merge(batch, parseZFSList(datasets.Stdout, c.Host))
		}
		if snapshots, snapshotErr := c.runner().Run(
			ctx, "zfs", "list", "-Hp", "-t", "snapshot",
			"-o", "name,used,creation",
		); snapshotErr == nil || len(snapshots.Stdout) > 0 {
			batch = Merge(batch, parseZFSSnapshots(snapshots.Stdout, c.Host))
		}
	}
	batch = Merge(batch, c.collectARC())
	return batch, nil
}

func (c ZFS) runner() Runner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

func parseZPoolList(body []byte, host string) (domain.IngestBatch, error) {
	hostID := "host:" + stableHash(host)
	batch := domain.IngestBatch{AgentID: host}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	now := time.Now().UTC()
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		size, sizeErr := strconv.ParseFloat(fields[1], 64)
		allocated, allocatedErr := strconv.ParseFloat(fields[2], 64)
		free, freeErr := strconv.ParseFloat(fields[3], 64)
		if sizeErr != nil || allocatedErr != nil || freeErr != nil {
			continue
		}
		pool := fields[0]
		health := fields[4]
		fragmentation, _ := strconv.ParseFloat(strings.TrimSuffix(fields[5], "%"), 64)
		assetID := "zfs:" + stableHash(host+"|"+pool)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       assetID,
			ParentStableID: hostID,
			Kind:           "pool",
			Name:           pool,
			Vendor:         "OpenZFS",
			Metadata: map[string]any{
				"health":        health,
				"sizeBytes":     size,
				"allocated":     allocated,
				"free":          free,
				"fragmentation": fragmentation,
			},
		})
		for _, metric := range []struct {
			key, label, category, unit string
			value                      float64
		}{
			{"size", "Pool size", "capacity", "By", size},
			{"allocated", "Allocated", "capacity", "By", allocated},
			{"free", "Free", "capacity", "By", free},
			{"used_percent", "Used", "utilization", "%", percent(allocated, size)},
			{"fragmentation", "Fragmentation", "utilization", "%", fragmentation},
			{"health", "Pool health", "health_status", "1", zfsHealthValue(health)},
		} {
			appendNumeric(&batch, assetID, "zfs", "zpool/"+pool+"/"+metric.key,
				metric.key, metric.label, metric.category, metric.unit, false, 30,
				metric.value, now, map[string]any{"pool": pool})
		}
	}
	if err := scanner.Err(); err != nil {
		return batch, fmt.Errorf("scan zpool output: %w", err)
	}
	return batch, nil
}

func percent(part, total float64) float64 {
	if total == 0 {
		return 0
	}
	return part / total * 100
}

func zfsHealthValue(value string) float64 {
	switch strings.ToUpper(value) {
	case "ONLINE":
		return 1
	case "DEGRADED":
		return 0.5
	default:
		return 0
	}
}

func parseZPoolStatus(body []byte, host string) domain.IngestBatch {
	batch := domain.IngestBatch{AgentID: host}
	now := time.Now().UTC()
	var pool, scan string
	inConfig := false
	parentByIndent := make(map[int]string)
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "pool:"):
			pool = strings.TrimSpace(strings.TrimPrefix(trimmed, "pool:"))
			scan = ""
			inConfig = false
			parentByIndent = make(map[int]string)
		case strings.HasPrefix(trimmed, "scan:"):
			scan = strings.TrimSpace(strings.TrimPrefix(trimmed, "scan:"))
		case trimmed == "config:":
			inConfig = true
		case strings.HasPrefix(trimmed, "errors:"):
			inConfig = false
		}
		if !inConfig || pool == "" || trimmed == "" ||
			strings.HasPrefix(trimmed, "NAME ") || strings.HasPrefix(trimmed, "state:") ||
			strings.HasPrefix(trimmed, "config:") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 5 {
			continue
		}
		readErrors, readErr := strconv.ParseFloat(fields[len(fields)-3], 64)
		writeErrors, writeErr := strconv.ParseFloat(fields[len(fields)-2], 64)
		checksumErrors, checksumErr := strconv.ParseFloat(fields[len(fields)-1], 64)
		if readErr != nil || writeErr != nil || checksumErr != nil {
			continue
		}
		name, state := fields[0], fields[1]
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		poolID := "zfs:" + stableHash(host+"|"+pool)
		if name == pool {
			parentByIndent[indent] = poolID
			continue
		}
		kind := "vdev"
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "gpt/") ||
			strings.HasPrefix(name, "wwn-") || strings.HasPrefix(name, "nvme") ||
			strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "da") {
			kind = "disk"
		}
		assetID := "zfs-vdev:" + stableHash(host+"|"+pool+"|"+name)
		parentID := poolID
		bestIndent := -1
		for candidateIndent, candidateID := range parentByIndent {
			if candidateIndent < indent && candidateIndent > bestIndent {
				parentID, bestIndent = candidateID, candidateIndent
			}
		}
		parentByIndent[indent] = assetID
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: assetID, ParentStableID: parentID, Kind: kind,
			Name: name, Vendor: "OpenZFS",
			Metadata: map[string]any{
				"pool": pool, "state": state, "scan": scan,
				"readErrors": readErrors, "writeErrors": writeErrors,
				"checksumErrors": checksumErrors,
			},
		})
		for _, metric := range []struct {
			key, label string
			value      float64
		}{
			{"read_errors", "Read errors", readErrors},
			{"write_errors", "Write errors", writeErrors},
			{"checksum_errors", "Checksum errors", checksumErrors},
		} {
			appendNumeric(
				&batch, assetID, "zfs", "zpool/"+pool+"/"+name+"/"+metric.key,
				"vdev/"+pool+"/"+name+"/"+metric.key, metric.label,
				"error_count", "1", true, 30, metric.value, now,
				map[string]any{"pool": pool, "vdev": name, "scan": scan},
			)
		}
	}
	return batch
}

func parseZFSList(body []byte, host string) domain.IngestBatch {
	batch := domain.IngestBatch{AgentID: host}
	now := time.Now().UTC()
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 7 {
			fields = strings.Fields(scanner.Text())
		}
		if len(fields) < 7 {
			continue
		}
		name, kind := fields[0], fields[1]
		pool := strings.SplitN(name, "/", 2)[0]
		poolID := "zfs:" + stableHash(host+"|"+pool)
		assetID := "zfs-dataset:" + stableHash(host+"|"+name)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID: assetID, ParentStableID: poolID, Kind: "dataset",
			Name: name, Vendor: "OpenZFS",
			Metadata: map[string]any{
				"type": kind, "mountpoint": fields[6], "pool": pool,
			},
		})
		for offset, metric := range []struct {
			key, label string
		}{{"used", "Used"}, {"available", "Available"}, {"referenced", "Referenced"}, {"quota", "Quota"}} {
			if fields[offset+2] == "-" || fields[offset+2] == "none" {
				continue
			}
			value, err := strconv.ParseFloat(fields[offset+2], 64)
			if err != nil {
				continue
			}
			appendNumeric(
				&batch, assetID, "zfs", "zfs/"+name+"/"+metric.key,
				"dataset/"+name+"/"+metric.key, metric.label, "capacity", "By",
				false, 30, value, now, map[string]any{"dataset": name},
			)
		}
	}
	return batch
}

func parseZFSSnapshots(body []byte, host string) domain.IngestBatch {
	batch := domain.IngestBatch{AgentID: host}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			fields = strings.Fields(scanner.Text())
		}
		if len(fields) < 3 || !strings.Contains(fields[0], "@") {
			continue
		}
		dataset := strings.SplitN(fields[0], "@", 2)[0]
		used, _ := strconv.ParseFloat(fields[1], 64)
		created, _ := strconv.ParseInt(fields[2], 10, 64)
		batch.Assets = append(batch.Assets, domain.IngestAsset{
			StableID:       "zfs-snapshot:" + stableHash(host+"|"+fields[0]),
			ParentStableID: "zfs-dataset:" + stableHash(host+"|"+dataset),
			Kind:           "snapshot",
			Name:           fields[0],
			Vendor:         "OpenZFS",
			Metadata:       map[string]any{"usedBytes": used, "createdUnix": created},
		})
	}
	return batch
}

func (c ZFS) collectARC() domain.IngestBatch {
	root := c.SysRoot
	if root == "" {
		root = "/"
	}
	path := filepath.Join(root, "proc/spl/kstat/zfs/arcstats")
	body, err := os.ReadFile(path)
	if err != nil {
		return domain.IngestBatch{AgentID: c.Host}
	}
	wanted := map[string]struct {
		category  string
		unit      string
		monotonic bool
	}{
		"size":          {"capacity", "By", false},
		"c":             {"capacity", "By", false},
		"arc_meta_used": {"capacity", "By", false},
		"l2_size":       {"capacity", "By", false},
		"hits":          {"throughput", "1", true},
		"misses":        {"error_count", "1", true},
	}
	hostID := "host:" + stableHash(c.Host)
	batch := domain.IngestBatch{AgentID: c.Host}
	now := time.Now().UTC()
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		definition, ok := wanted[fields[0]]
		if !ok {
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[len(fields)-1], 64)
		if parseErr != nil {
			continue
		}
		appendNumeric(
			&batch, hostID, "zfs-arc", "/proc/spl/kstat/zfs/arcstats",
			"arc/"+fields[0], "ARC "+fields[0], definition.category,
			definition.unit, definition.monotonic, 30, value, now, nil,
		)
	}
	return batch
}
