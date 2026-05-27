package downloader

import "log/slog"

// DownloadOptions holds all parameters for a download operation.
type DownloadOptions struct {
	StartDate   string
	EndDate     string
	Table       string   // comma-separated, "" means default tables
	Translation bool
	Output      string // "local" or "minio"
	Flat        bool
	Watch       bool
	DataDir     string
	Concurrency int

	// Resolver results
	BaseURL    string
	HostHeader string

	// MinIO
	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioSecure    bool
	ZipPrefix      string

	Logger *slog.Logger
}
