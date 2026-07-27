package config

import (
	"os"
	"strconv"
	"time"
)

type Server struct {
	HTTPAddr      string
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
	DatabaseURL string
	Interval    time.Duration
}

func WorkerFromEnvironment() Worker {
	interval, err := time.ParseDuration(env("MONITORED_WORKER_INTERVAL", "1m"))
	if err != nil || interval < 5*time.Second {
		interval = time.Minute
	}
	return Worker{
		DatabaseURL: env("MONITORED_DATABASE_URL", "postgres://monitored:monitored@localhost:5432/monitored?sslmode=disable"),
		Interval:    interval,
	}
}

type Agent struct {
	ServerURL string
	Token     string
	Name      string
	Interval  time.Duration
	WALPath   string
	SysRoot   string
}

func AgentFromEnvironment() Agent {
	interval, err := time.ParseDuration(env("MONITORED_AGENT_INTERVAL", "10s"))
	if err != nil || interval < time.Second {
		interval = 10 * time.Second
	}
	return Agent{
		ServerURL: env("MONITORED_SERVER_URL", "http://localhost:8080"),
		Token:     env("MONITORED_AGENT_TOKEN", "local-development-agent-token"),
		Name:      env("MONITORED_AGENT_NAME", hostname()),
		Interval:  interval,
		WALPath:   env("MONITORED_WAL_PATH", "./monitored-agent.wal.ndjson"),
		SysRoot:   env("MONITORED_SYS_ROOT", "/"),
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
