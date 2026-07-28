package domain

import "time"

type Connection struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Kind           string         `json:"kind"`
	Endpoint       string         `json:"endpoint"`
	AgentID        *string        `json:"agentId,omitempty"`
	Enabled        bool           `json:"enabled"`
	TLSMode        string         `json:"tlsMode"`
	Capability     map[string]any `json:"capability"`
	Status         string         `json:"status"`
	StatusMessage  string         `json:"statusMessage,omitempty"`
	LastTestedAt   *time.Time     `json:"lastTestedAt,omitempty"`
	LastSuccessAt  *time.Time     `json:"lastSuccessAt,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	HasCredentials bool           `json:"hasCredentials"`
}

type Rule struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	SensorID           *string        `json:"sensorId,omitempty"`
	AssetKind          string         `json:"assetKind,omitempty"`
	RuleType           string         `json:"ruleType"`
	Severity           string         `json:"severity"`
	Expression         map[string]any `json:"expression"`
	DurationSeconds    int            `json:"durationSeconds"`
	RecoveryExpression map[string]any `json:"recoveryExpression"`
	MissingDataMode    string         `json:"missingDataMode"`
	MaintenanceWindow  *string        `json:"maintenanceWindowId,omitempty"`
	Enabled            bool           `json:"enabled"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

type MaintenanceWindow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
	AssetID   *string   `json:"assetId,omitempty"`
	RuleID    *string   `json:"ruleId,omitempty"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type NotificationChannel struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Enabled      bool           `json:"enabled"`
	Config       map[string]any `json:"config"`
	HasSecret    bool           `json:"hasSecret"`
	LastTestedAt *time.Time     `json:"lastTestedAt,omitempty"`
	LastStatus   string         `json:"lastStatus"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

type NotificationRoute struct {
	ID                    string   `json:"id"`
	ChannelID             string   `json:"channelId"`
	ChannelName           string   `json:"channelName"`
	MinSeverity           string   `json:"minSeverity"`
	AssetKinds            []string `json:"assetKinds"`
	GroupWindowSeconds    int      `json:"groupWindowSeconds"`
	RepeatIntervalSeconds int      `json:"repeatIntervalSeconds"`
	SendRecovery          bool     `json:"sendRecovery"`
	Locale                string   `json:"locale"`
	Enabled               bool     `json:"enabled"`
}

type NotificationDelivery struct {
	ID              string         `json:"id"`
	ChannelID       string         `json:"channelId"`
	ChannelName     string         `json:"channelName"`
	Kind            string         `json:"kind"`
	Config          map[string]any `json:"config"`
	EncryptedSecret []byte         `json:"-"`
	Payload         map[string]any `json:"payload"`
	Attempt         int            `json:"attempt"`
}

type Diagnosis struct {
	ID          string         `json:"id"`
	IncidentID  *string        `json:"incidentId,omitempty"`
	AssetID     *string        `json:"assetId,omitempty"`
	Status      string         `json:"status"`
	Provider    string         `json:"provider"`
	Model       string         `json:"model"`
	Summary     string         `json:"summary"`
	Hypotheses  []string       `json:"hypotheses"`
	Evidence    []string       `json:"evidence"`
	DataGaps    []string       `json:"dataGaps"`
	Checks      []string       `json:"checks"`
	Actions     []string       `json:"actions"`
	Rollback    string         `json:"rollback"`
	Confidence  float64        `json:"confidence"`
	TokenUsage  int            `json:"tokenUsage"`
	RequestedBy string         `json:"requestedBy"`
	RequestedAt time.Time      `json:"requestedAt"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
	Context     map[string]any `json:"context"`
}

type LLMSuggestionJob struct {
	ID          string         `json:"id"`
	SensorID    string         `json:"sensorId"`
	Provider    string         `json:"provider"`
	Model       string         `json:"model"`
	Status      string         `json:"status"`
	TokenUsage  int            `json:"tokenUsage"`
	RequestedBy string         `json:"requestedBy"`
	RequestedAt time.Time      `json:"requestedAt"`
	CompletedAt *time.Time     `json:"completedAt,omitempty"`
	Context     map[string]any `json:"context"`
	Error       string         `json:"error,omitempty"`
}

type Agent struct {
	ID            string         `json:"id"`
	StableHostID  string         `json:"stableHostId"`
	Hostname      string         `json:"hostname"`
	OS            string         `json:"os"`
	Architecture  string         `json:"architecture"`
	Version       string         `json:"version"`
	State         string         `json:"state"`
	CertificateID string         `json:"certificateId,omitempty"`
	Capabilities  []string       `json:"capabilities"`
	Tools         []string       `json:"tools"`
	Metadata      map[string]any `json:"metadata"`
	LastSeenAt    time.Time      `json:"lastSeenAt"`
	WALBytes      int64          `json:"walBytes"`
	OldestWALAt   *time.Time     `json:"oldestWalAt,omitempty"`
	LastGapFrom   *time.Time     `json:"lastGapFrom,omitempty"`
	LastGapTo     *time.Time     `json:"lastGapTo,omitempty"`
	LastGapReason string         `json:"lastGapReason,omitempty"`
	RegisteredAt  time.Time      `json:"registeredAt"`
}

type AgentTask struct {
	ID             string         `json:"id"`
	AgentID        string         `json:"agentId"`
	Kind           string         `json:"kind"`
	State          string         `json:"state"`
	Payload        map[string]any `json:"payload"`
	ImmutableHash  string         `json:"immutableHash,omitempty"`
	Attempt        int            `json:"attempt"`
	NotBefore      time.Time      `json:"notBefore"`
	ExpiresAt      time.Time      `json:"expiresAt"`
	DispatchedAt   *time.Time     `json:"dispatchedAt,omitempty"`
	AcknowledgedAt *time.Time     `json:"acknowledgedAt,omitempty"`
	FinishedAt     *time.Time     `json:"finishedAt,omitempty"`
	Result         map[string]any `json:"result"`
	CreatedAt      time.Time      `json:"createdAt"`
}

type CapabilityStatus struct {
	ID          string         `json:"id"`
	Label       string         `json:"label"`
	State       string         `json:"state"`
	Detail      string         `json:"detail"`
	Enhancement string         `json:"enhancement,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type User struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	Enabled     bool       `json:"enabled"`
	TOTPEnabled bool       `json:"totpEnabled"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}
