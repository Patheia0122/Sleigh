package config

import (
	"os"
	"strconv"
	"time"
)

const (
	defaultHTTPAddr                   = ":10122"
	defaultReadTimeout                = 10 * time.Second
	defaultWriteTimeout               = 10 * time.Second
	defaultShutdownTimeout            = 10 * time.Second
	defaultVersion                    = "dev"
	defaultDBPath                     = "./data/runtime.db"
	defaultMinExpandMB                = 64
	defaultMaxExpandMB                = 4096
	defaultMaxExpandStepMB            = 2048
	defaultExecTTLDays                = 14
	defaultWarmPoolSize               = 1
	defaultWarmPoolImage              = "python:3.11-slim"
	defaultWarmPoolMemory             = 256
	defaultSnapshotRootDir            = "./data/snapshots"
	defaultCursorTokenTTL             = 3600
	defaultExecCleanupIntervalSeconds = 3600
	defaultImagePullTimeoutSeconds    = 120
	defaultSandboxIdleTTLDays         = 14
	defaultMountAllowedRoot           = "/opt/sleigh-runtime/mount-root-default"
	defaultOTELEndpoint               = ""
)

type Config struct {
	HTTPAddr                   string
	ReadTimeout                time.Duration
	WriteTimeout               time.Duration
	ShutdownTimeout            time.Duration
	Version                    string
	DBPath                     string
	MinExpandMB                int64
	MaxExpandMB                int64
	MaxExpandStepMB            int64
	ExecTTLDays                int
	WarmPoolSize               int
	WarmPoolImage              string
	WarmPoolMemoryMB           int64
	SnapshotRootDir            string
	ImagePullTimeoutSeconds    int
	SandboxIdleTTLDays         int
	OTELEndpoint               string
	CursorTokenSecret          string
	CursorTokenTTLSeconds      int
	ExecCleanupIntervalSeconds int
	MountAllowedRoot           string
}

func FromEnv() Config {
	cfg := Config{
		HTTPAddr:                   getEnv("SERVER_ADDR", defaultHTTPAddr),
		ReadTimeout:                defaultReadTimeout,
		WriteTimeout:               defaultWriteTimeout,
		ShutdownTimeout:            defaultShutdownTimeout,
		Version:                    getEnv("SERVER_VERSION", defaultVersion),
		DBPath:                     getEnv("SERVER_DB_PATH", defaultDBPath),
		MinExpandMB:                getEnvInt64("MEMORY_EXPAND_MIN_MB", defaultMinExpandMB),
		MaxExpandMB:                getEnvInt64("MEMORY_EXPAND_MAX_MB", defaultMaxExpandMB),
		MaxExpandStepMB:            getEnvInt64("MEMORY_EXPAND_MAX_STEP_MB", defaultMaxExpandStepMB),
		ExecTTLDays:                getEnvInt("EXEC_TASK_TTL_DAYS", defaultExecTTLDays),
		WarmPoolSize:               getEnvInt("WARM_POOL_SIZE", defaultWarmPoolSize),
		WarmPoolImage:              getEnv("WARM_POOL_IMAGE", defaultWarmPoolImage),
		WarmPoolMemoryMB:           getEnvInt64("WARM_POOL_MEMORY_MB", defaultWarmPoolMemory),
		SnapshotRootDir:            getEnv("SNAPSHOT_ROOT_DIR", defaultSnapshotRootDir),
		ImagePullTimeoutSeconds:    getEnvInt("IMAGE_PULL_TIMEOUT_SECONDS", defaultImagePullTimeoutSeconds),
		SandboxIdleTTLDays:         getEnvInt("SANDBOX_IDLE_TTL_DAYS", defaultSandboxIdleTTLDays),
		OTELEndpoint:               getEnv("SERVER_OTEL_EXPORTER_OTLP_ENDPOINT", defaultOTELEndpoint),
		CursorTokenSecret:          getEnv("CURSOR_TOKEN_SECRET", "dev-cursor-secret"),
		CursorTokenTTLSeconds:      getEnvInt("CURSOR_TOKEN_TTL_SECONDS", defaultCursorTokenTTL),
		ExecCleanupIntervalSeconds: getEnvInt("EXEC_CLEANUP_INTERVAL_SECONDS", defaultExecCleanupIntervalSeconds),
		MountAllowedRoot:           getEnv("SERVER_MOUNT_ALLOWED_ROOT", defaultMountAllowedRoot),
	}

	return cfg
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

func getEnvInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
