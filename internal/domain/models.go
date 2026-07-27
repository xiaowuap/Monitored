package domain

import (
	"encoding/json"
	"time"
)

type Asset struct {
	ID         string             `json:"id"`
	StableID   string             `json:"stableId"`
	ParentID   *string            `json:"parentId,omitempty"`
	Kind       string             `json:"kind"`
	Name       string             `json:"name"`
	Vendor     string             `json:"vendor,omitempty"`
	Model      string             `json:"model,omitempty"`
	Health     string             `json:"health"`
	Connection string             `json:"connection"`
	LastSeenAt time.Time          `json:"lastSeenAt"`
	Metadata   map[string]any     `json:"metadata"`
	Summary    AssetSensorSummary `json:"summary"`
}

type AssetSensorSummary struct {
	SensorCount       int      `json:"sensorCount"`
	UnknownCount      int      `json:"unknownCount"`
	HighestTempC      *float64 `json:"highestTempC,omitempty"`
	PowerW            *float64 `json:"powerW,omitempty"`
	GrowingErrorCount int      `json:"growingErrorCount"`
}

type Sensor struct {
	ID                 string         `json:"id"`
	StableID           string         `json:"stableId"`
	AssetID            string         `json:"assetId"`
	AssetName          string         `json:"assetName"`
	Source             string         `json:"source"`
	SourcePath         string         `json:"sourcePath"`
	RawName            string         `json:"rawName"`
	DisplayAlias       *string        `json:"displayAlias,omitempty"`
	Category           string         `json:"category"`
	Unit               string         `json:"unit"`
	Scale              float64        `json:"scale"`
	Monotonic          bool           `json:"monotonic"`
	HardwareWarning    *float64       `json:"hardwareWarning,omitempty"`
	HardwareCritical   *float64       `json:"hardwareCritical,omitempty"`
	UserWarning        *float64       `json:"userWarning,omitempty"`
	UserCritical       *float64       `json:"userCritical,omitempty"`
	IntervalSeconds    int            `json:"intervalSeconds"`
	DiscoveryStatus    string         `json:"discoveryStatus"`
	MissingDiscoveries int            `json:"missingDiscoveries"`
	Quality            string         `json:"quality"`
	LastSeenAt         time.Time      `json:"lastSeenAt"`
	CurrentValue       *float64       `json:"currentValue,omitempty"`
	CurrentText        *string        `json:"currentText,omitempty"`
	ObservedAt         *time.Time     `json:"observedAt,omitempty"`
	Metadata           map[string]any `json:"metadata"`
}

type MetricPoint struct {
	At      time.Time `json:"at"`
	Min     float64   `json:"min"`
	Avg     float64   `json:"avg"`
	Max     float64   `json:"max"`
	Count   int       `json:"count"`
	Quality string    `json:"quality,omitempty"`
	Source  string    `json:"source,omitempty"`
}

type Alert struct {
	ID          string     `json:"id"`
	Fingerprint string     `json:"fingerprint"`
	RuleName    string     `json:"ruleName"`
	AssetID     string     `json:"assetId"`
	AssetName   string     `json:"assetName"`
	SensorID    *string    `json:"sensorId,omitempty"`
	Severity    string     `json:"severity"`
	State       string     `json:"state"`
	Summary     string     `json:"summary"`
	Value       *float64   `json:"value,omitempty"`
	Unit        string     `json:"unit,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

type Incident struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Severity   string     `json:"severity"`
	State      string     `json:"state"`
	AssetCount int        `json:"assetCount"`
	AlertCount int        `json:"alertCount"`
	Diagnosis  *string    `json:"diagnosis,omitempty"`
	OpenedAt   time.Time  `json:"openedAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

type DiscoveryRun struct {
	ID           string         `json:"id"`
	AgentID      *string        `json:"agentId,omitempty"`
	Kind         string         `json:"kind"`
	State        string         `json:"state"`
	RequestedBy  string         `json:"requestedBy"`
	StartedAt    time.Time      `json:"startedAt"`
	FinishedAt   *time.Time     `json:"finishedAt,omitempty"`
	AssetsFound  int            `json:"assetsFound"`
	SensorsFound int            `json:"sensorsFound"`
	UnknownFound int            `json:"unknownFound"`
	Progress     int            `json:"progress"`
	Details      map[string]any `json:"details"`
}

type LLMSuggestion struct {
	ID                 string         `json:"id"`
	SensorID           string         `json:"sensorId"`
	RawName            string         `json:"rawName"`
	AssetName          string         `json:"assetName"`
	SuggestedName      string         `json:"suggestedName"`
	SuggestedAssetKind string         `json:"suggestedAssetKind"`
	SuggestedCategory  string         `json:"suggestedCategory"`
	SuggestedUnit      string         `json:"suggestedUnit"`
	Confidence         float64        `json:"confidence"`
	Evidence           []string       `json:"evidence"`
	Status             string         `json:"status"`
	Provider           string         `json:"provider"`
	Model              string         `json:"model"`
	CreatedAt          time.Time      `json:"createdAt"`
	ReviewedAt         *time.Time     `json:"reviewedAt,omitempty"`
	ReviewedBy         *string        `json:"reviewedBy,omitempty"`
	Context            map[string]any `json:"context"`
}

type ActionRequest struct {
	ID               string         `json:"id"`
	AssetID          string         `json:"assetId"`
	AssetName        string         `json:"assetName"`
	Kind             string         `json:"kind"`
	Title            string         `json:"title"`
	Command          string         `json:"command"`
	WorkingDirectory string         `json:"workingDirectory"`
	Identity         string         `json:"identity"`
	TimeoutSeconds   int            `json:"timeoutSeconds"`
	Risk             string         `json:"risk"`
	State            string         `json:"state"`
	ImmutableHash    string         `json:"immutableHash"`
	RequestedBy      string         `json:"requestedBy"`
	RequestedAt      time.Time      `json:"requestedAt"`
	ApprovedBy       *string        `json:"approvedBy,omitempty"`
	ApprovedAt       *time.Time     `json:"approvedAt,omitempty"`
	Result           map[string]any `json:"result,omitempty"`
}

type AuditEntry struct {
	ID           int64          `json:"id"`
	Actor        string         `json:"actor"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	OccurredAt   time.Time      `json:"occurredAt"`
	RemoteAddr   string         `json:"remoteAddr,omitempty"`
	Details      map[string]any `json:"details"`
	PreviousHash string         `json:"previousHash,omitempty"`
	EntryHash    string         `json:"entryHash"`
}

type Overview struct {
	GeneratedAt        time.Time       `json:"generatedAt"`
	HealthScore        int             `json:"healthScore"`
	AssetCount         int             `json:"assetCount"`
	OnlineAssetCount   int             `json:"onlineAssetCount"`
	SensorCount        int             `json:"sensorCount"`
	UnknownCount       int             `json:"unknownCount"`
	FiringAlertCount   int             `json:"firingAlertCount"`
	PendingActionCount int             `json:"pendingActionCount"`
	IngestRate         int             `json:"ingestRate"`
	StorageStatus      string          `json:"storageStatus"`
	QueueStatus        string          `json:"queueStatus"`
	AssetsByKind       map[string]int  `json:"assetsByKind"`
	TemperatureTrend   []OverviewPoint `json:"temperatureTrend"`
	RecentAlerts       []Alert         `json:"recentAlerts"`
	HotAssets          []Asset         `json:"hotAssets"`
}

type OverviewPoint struct {
	At      time.Time `json:"at"`
	Average float64   `json:"average"`
	Maximum float64   `json:"maximum"`
	PowerW  float64   `json:"powerW"`
}

type Capability struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	State       string `json:"state"`
	Detail      string `json:"detail"`
	Enhancement string `json:"enhancement,omitempty"`
}

type IngestAsset struct {
	StableID       string         `json:"stableId"`
	ParentStableID string         `json:"parentStableId,omitempty"`
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	Vendor         string         `json:"vendor,omitempty"`
	Model          string         `json:"model,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type IngestSensor struct {
	StableID        string         `json:"stableId"`
	AssetStableID   string         `json:"assetStableId"`
	Source          string         `json:"source"`
	SourcePath      string         `json:"sourcePath"`
	RawName         string         `json:"rawName"`
	Category        string         `json:"category"`
	Unit            string         `json:"unit"`
	Scale           float64        `json:"scale"`
	Monotonic       bool           `json:"monotonic"`
	IntervalSeconds int            `json:"intervalSeconds"`
	Quality         string         `json:"quality"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type IngestSample struct {
	SensorStableID string    `json:"sensorStableId"`
	ObservedAt     time.Time `json:"observedAt"`
	Value          *float64  `json:"value,omitempty"`
	TextValue      *string   `json:"textValue,omitempty"`
	Source         string    `json:"source"`
	Quality        string    `json:"quality"`
	Error          string    `json:"error,omitempty"`
}

type IngestBatch struct {
	AgentID string         `json:"agentId"`
	Assets  []IngestAsset  `json:"assets"`
	Sensors []IngestSensor `json:"sensors"`
	Samples []IngestSample `json:"samples"`
}

func JSONMap(value []byte) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(value, &result); err != nil {
		return map[string]any{}
	}
	return result
}
