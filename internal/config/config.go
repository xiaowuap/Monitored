package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	HTTPAddr      string
	GRPCAddr      string
	GRPCCertFile  string
	GRPCKeyFile   string
	GRPCClientCA  string
	GRPCInsecure  bool
	DatabaseURL   string
	RedisURL      string
	PublicURL     string
	AgentToken    string
	MasterKey     string
	AdminEmail    string
	AdminPassword string
	AuthDisabled  bool
	AutoMigrate   bool
}

func ServerFromEnvironment() Server {
	return Server{
		HTTPAddr:      env("MONITORED_HTTP_ADDR", ":8080"),
		GRPCAddr:      env("MONITORED_GRPC_ADDR", ""),
		GRPCCertFile:  env("MONITORED_GRPC_CERT_FILE", ""),
		GRPCKeyFile:   env("MONITORED_GRPC_KEY_FILE", ""),
		GRPCClientCA:  env("MONITORED_GRPC_CLIENT_CA_FILE", ""),
		GRPCInsecure:  envBool("MONITORED_GRPC_INSECURE", false),
		DatabaseURL:   env("MONITORED_DATABASE_URL", "postgres://monitored:monitored@localhost:5432/monitored?sslmode=disable"),
		RedisURL:      env("MONITORED_REDIS_URL", "redis://localhost:6379/0"),
		PublicURL:     env("MONITORED_PUBLIC_URL", "http://localhost:3000"),
		AgentToken:    env("MONITORED_AGENT_TOKEN", "local-development-agent-token"),
		MasterKey:     env("MONITORED_MASTER_KEY", "local-development-master-key-change-me"),
		AdminEmail:    env("MONITORED_ADMIN_EMAIL", "admin@example.com"),
		AdminPassword: env("MONITORED_ADMIN_PASSWORD", "change-me-now"),
		AuthDisabled:  envBool("MONITORED_AUTH_DISABLED", false),
		AutoMigrate:   envBool("MONITORED_AUTO_MIGRATE", true),
	}
}

type Worker struct {
	DatabaseURL    string
	MasterKey      string
	Interval       time.Duration
	SNMPProfiles   string
	PollTimeout    time.Duration
	PollWorkers    int
	OpenAIURL      string
	OpenAIKey      string
	OllamaURL      string
	LLMTokenBudget int
}

func WorkerFromEnvironment() Worker {
	interval, err := time.ParseDuration(env("MONITORED_WORKER_INTERVAL", "1m"))
	if err != nil || interval < 5*time.Second {
		interval = time.Minute
	}
	pollTimeout, err := time.ParseDuration(env("MONITORED_CONNECTION_TIMEOUT", "45s"))
	if err != nil || pollTimeout < time.Second || pollTimeout > 5*time.Minute {
		pollTimeout = 45 * time.Second
	}
	pollWorkers, err := strconv.Atoi(env("MONITORED_CONNECTION_CONCURRENCY", "8"))
	if err != nil || pollWorkers < 1 || pollWorkers > 32 {
		pollWorkers = 8
	}
	tokenBudget, err := strconv.Atoi(env("MONITORED_LLM_TOKEN_BUDGET", "8000"))
	if err != nil || tokenBudget < 256 || tokenBudget > 100000 {
		tokenBudget = 8000
	}
	return Worker{
		DatabaseURL:    env("MONITORED_DATABASE_URL", "postgres://monitored:monitored@localhost:5432/monitored?sslmode=disable"),
		MasterKey:      env("MONITORED_MASTER_KEY", "local-development-master-key-change-me"),
		Interval:       interval,
		SNMPProfiles:   env("MONITORED_SNMP_PROFILE_DIR", "./profiles/snmp"),
		PollTimeout:    pollTimeout,
		PollWorkers:    pollWorkers,
		OpenAIURL:      env("MONITORED_OPENAI_BASE_URL", ""),
		OpenAIKey:      env("MONITORED_OPENAI_API_KEY", ""),
		OllamaURL:      env("MONITORED_OLLAMA_BASE_URL", ""),
		LLMTokenBudget: tokenBudget,
	}
}

type Agent struct {
	ServerURL        string
	Protocol         string
	GRPCTarget       string
	TLSCertFile      string
	TLSKeyFile       string
	TLSCAFile        string
	TLSServerName    string
	GRPCInsecure     bool
	Token            string
	Name             string
	Interval         time.Duration
	SMARTInterval    time.Duration
	WALPath          string
	SysRoot          string
	DeviceRoot       string
	TrueNASEndpoint  string
	TrueNASToken     string
	SystemdUnits     []string
	AllowRootActions bool
}

func AgentFromEnvironment() Agent {
	interval, err := time.ParseDuration(env("MONITORED_AGENT_INTERVAL", "10s"))
	if err != nil || interval < time.Second {
		interval = 10 * time.Second
	}
	smartInterval, err := time.ParseDuration(env("MONITORED_SMART_INTERVAL", "5m"))
	if err != nil || smartInterval < time.Minute {
		smartInterval = 5 * time.Minute
	}
	return Agent{
		ServerURL:        env("MONITORED_SERVER_URL", "http://localhost:8080"),
		Protocol:         env("MONITORED_AGENT_PROTOCOL", "rest"),
		GRPCTarget:       env("MONITORED_GRPC_TARGET", "localhost:9443"),
		TLSCertFile:      env("MONITORED_AGENT_TLS_CERT_FILE", ""),
		TLSKeyFile:       env("MONITORED_AGENT_TLS_KEY_FILE", ""),
		TLSCAFile:        env("MONITORED_AGENT_TLS_CA_FILE", ""),
		TLSServerName:    env("MONITORED_AGENT_TLS_SERVER_NAME", ""),
		GRPCInsecure:     envBool("MONITORED_AGENT_GRPC_INSECURE", false),
		Token:            env("MONITORED_AGENT_TOKEN", "local-development-agent-token"),
		Name:             env("MONITORED_AGENT_NAME", hostname()),
		Interval:         interval,
		SMARTInterval:    smartInterval,
		WALPath:          env("MONITORED_WAL_PATH", "./monitored-agent.wal.ndjson"),
		SysRoot:          env("MONITORED_SYS_ROOT", "/"),
		DeviceRoot:       env("MONITORED_DEVICE_ROOT", "/dev"),
		TrueNASEndpoint:  env("MONITORED_TRUENAS_ENDPOINT", ""),
		TrueNASToken:     env("MONITORED_TRUENAS_TOKEN", ""),
		SystemdUnits:     envList("MONITORED_SYSTEMD_UNITS"),
		AllowRootActions: envBool("MONITORED_ALLOW_ROOT_ACTIONS", false),
	}
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func hostname() string {
	value, err := os.Hostname()
	if err != nil || value == "" {
		return "unknown-host"
	}
	return value
}

func envList(key string) []string {
	var result []string
	for _, value := range strings.Split(os.Getenv(key), ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
