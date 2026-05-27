package downloader

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wuchang/gdelt-data-download/minio"
)

const watchInterval = 5 * time.Minute

// WatchMode continuously monitors GDELT for new 15-minute time slices.
// It runs in a loop, checking every 5 minutes for new data.
func WatchMode(ctx context.Context, opts *DownloadOptions) error {
	logger := opts.Logger
	logger.Info("进入实时监测模式", "轮询间隔", watchInterval)

	tables := ResolveTableList(opts.Table, opts.Translation)

	// Prepare MinIO client if needed
	var mc *minio.Client
	if opts.Output == "minio" {
		var err error
		mc, err = minio.NewClient(ctx, opts.MinioEndpoint, opts.MinioAccessKey,
			opts.MinioSecretKey, opts.MinioBucket, opts.MinioSecure, logger)
		if err != nil {
			return fmt.Errorf("minio client: %w", err)
		}
	}

	httpClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:       opts.Concurrency * 2,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: false,
		},
	}

	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	var cycleCount atomic.Int64

	// Do an immediate check on start
	if err := watchCycle(ctx, httpClient, mc, opts, tables); err != nil {
		logger.Warn("初始监测周期失败", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("实时监测模式已退出")
			return nil
		case <-ticker.C:
			cycle := cycleCount.Add(1)
			logger.Debug("开始监测周期", "cycle", cycle)
			if err := watchCycle(ctx, httpClient, mc, opts, tables); err != nil {
				logger.Warn("监测周期失败", "cycle", cycle, "error", err)
			}
		}
	}
}

// watchCycle performs one round of checking and downloading new time slices.
func watchCycle(ctx context.Context, httpClient *http.Client, mc *minio.Client, opts *DownloadOptions, tables []string) error {
	logger := opts.Logger
	now := time.Now().UTC()
	today := now.Format("20060102")

	// Generate time slices for today
	timeSlices := TimeSlices(today)

	// Determine what we already have
	var existingFiles map[string]struct{}
	var err error

	if opts.Output == "local" {
		existingFiles, err = ScanLocalDir(opts.DataDir, tables, opts.Flat)
	} else {
		existingFiles, err = scanMinioBucket(ctx, mc, tables, opts.ZipPrefix, opts.Flat)
	}
	if err != nil {
		return fmt.Errorf("scan existing files: %w", err)
	}

	// Find the latest complete time slice on GDELT server
	latestTS := LatestTimeSlice()

	// Build worker pool for this cycle
	taskCh := make(chan downloadTask, opts.Concurrency*2)
	var wg sync.WaitGroup
	results := make(chan downloadResult, opts.Concurrency*2)

	for range opts.Concurrency {
		wg.Add(1)
		go worker(ctx, httpClient, mc, opts, taskCh, results, &wg)
	}

	// Producer: generate tasks for missing time slices
	go func() {
		defer close(taskCh)
		for _, table := range tables {
			for _, ts := range timeSlices {
				if ts > latestTS {
					break
				}

				// Check if we already have it
				var key string
				if opts.Flat {
					key = table + "/" + ts
				} else {
					key = table + "/" + ts[:8] + "/" + ts
				}
				if _, exists := existingFiles[key]; exists {
					continue
				}

				url := URLForTimestamp(opts.BaseURL, table, ts)
				task := downloadTask{
					url:       url,
					table:     table,
					date:      today,
					timestamp: ts,
				}

				select {
				case taskCh <- task:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var downloaded, skipped, notFound, errors int
	for r := range results {
		switch {
		case r.err != nil:
			errors++
			logger.Error("下载失败", "url", r.url, "error", r.err)
		case r.notFound:
			notFound++
		case r.skipped:
			skipped++
		default:
			downloaded++
		}
	}

	if downloaded > 0 {
		logger.Info("监测周期：下载了新数据",
			"新下载", downloaded,
			"跳过", skipped,
			"无数据", notFound,
		)
	} else {
		logger.Debug("监测周期：无新数据", "跳过", skipped, "无数据", notFound)
	}

	return nil
}

func scanMinioBucket(ctx context.Context, mc *minio.Client, tables []string, prefix string, flat bool) (map[string]struct{}, error) {
	existing := make(map[string]struct{})

	for _, table := range tables {
		objectPrefix := fmt.Sprintf("%s/%s", prefix, table)
		objects, err := mc.ListPrefixNames(ctx, objectPrefix)
		if err != nil {
			continue
		}
		for _, obj := range objects {
			// Extract timestamp from filename at the end of the path
			filename := func(s string) string {
				for i := len(s) - 1; i >= 0; i-- {
					if s[i] == '/' {
						return s[i+1:]
					}
				}
				return s
			}(obj)

			if len(filename) >= 14 {
				ts := filename[:14]
				if flat {
					existing[table+"/"+ts] = struct{}{}
				} else {
					dateStr := ts[:8]
					existing[table+"/"+dateStr+"/"+ts] = struct{}{}
				}
			}
		}
	}

	return existing, nil
}

// Ensure the compile doesn't complain about unused imports.
var _ = atomic.LoadInt64
