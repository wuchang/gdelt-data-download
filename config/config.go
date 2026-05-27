// config/config.go — Configuration loading with priority: CLI flag > .env > environment variable.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// DownloadConfig holds runtime configuration for the downloader.
type DownloadConfig struct {
	StartDate   string
	EndDate     string
	Table       string // comma-separated, default: "export,gkg,mentions"
	Output      string // "local" or "minio"
	Flat        bool
	Translation bool
	Watch       bool
	DataDir     string
	Concurrency int

	// MinIO settings (used when output=minio)
	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioSecure    bool
	ZipPrefix      string
}

// envDefault returns the value of the environment variable if set, otherwise the fallback.
func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envDefaultBool parses an env var as bool, returns fallback if unset or invalid.
func envDefaultBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// LoadEnv reads .env file if present. Errors are silently ignored (no .env is fine).
func LoadEnv() {
	// Try .env in current directory
	_ = godotenv.Load()
	// Try .env in the binary's parent directory (common in deployment)
	_ = godotenv.Load("../.env")
}

// MinioEnvConfig reads MinIO config from environment variables (lowest priority).
func MinioEnvConfig() (endpoint, accessKey, secretKey, bucket string, secure bool) {
	return envDefault("MINIO_ENDPOINT", ""),
		envDefault("MINIO_ACCESS_KEY", ""),
		envDefault("MINIO_SECRET_KEY", ""),
		envDefault("MINIO_BUCKET", ""),
		envDefaultBool("MINIO_SECURE", false)
}

// MergeMinioFlags applies non-empty CLI flag values on top of base config.
func MergeMinioFlags(endpoint, accessKey, secretKey, bucket string, secure bool, cliEndpoint, cliAccessKey, cliSecretKey, cliBucket string, cliSecure *bool) (string, string, string, string, bool) {
	if cliEndpoint != "" {
		endpoint = cliEndpoint
	}
	if cliAccessKey != "" {
		accessKey = cliAccessKey
	}
	if cliSecretKey != "" {
		secretKey = cliSecretKey
	}
	if cliBucket != "" {
		bucket = cliBucket
	}
	if cliSecure != nil {
		secure = *cliSecure
	}
	if endpoint == "" {
		endpoint = "localhost:9000"
	}
	if bucket == "" {
		bucket = "gdelt"
	}
	return endpoint, accessKey, secretKey, bucket, secure
}

// ValidateDownloadFlags checks that the combined config is valid.
func ValidateDownloadFlags(cfg *DownloadConfig) error {
	if cfg.StartDate == "" {
		return fmt.Errorf("--start-date is required")
	}
	if len(cfg.StartDate) != 8 {
		return fmt.Errorf("--start-date must be in YYYYMMDD format")
	}
	if _, err := strconv.Atoi(cfg.StartDate); err != nil {
		return fmt.Errorf("--start-date must be a numeric YYYYMMDD")
	}
	if cfg.EndDate != "" {
		if len(cfg.EndDate) != 8 {
			return fmt.Errorf("--end-date must be in YYYYMMDD format")
		}
		if _, err := strconv.Atoi(cfg.EndDate); err != nil {
			return fmt.Errorf("--end-date must be a numeric YYYYMMDD")
		}
	}
	if cfg.Output != "local" && cfg.Output != "minio" {
		return fmt.Errorf("--output must be 'local' or 'minio'")
	}
	if cfg.Concurrency < 1 {
		return fmt.Errorf("--concurrency must be >= 1")
	}
	return nil
}

// DefaultZipPrefix returns the default value for MinIO ZIP prefix.
const DefaultZipPrefix = "gdelt-zip"
