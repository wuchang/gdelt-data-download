package downloader

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wuchang/gdelt-data-download/minio"
)

// concurrencyCounter tracks consecutive failures.
var consecutiveFailures atomic.Int32

// BatchDownload downloads all tables across a date range.
func BatchDownload(ctx context.Context, opts *DownloadOptions) error {
	logger := opts.Logger

	// Resolve tables
	tables := ResolveTableList(opts.Table, opts.Translation)
	for _, t := range tables {
		if err := ValidateTable(t); err != nil {
			return err
		}
	}

	// Resolve date range
	dates, err := DateRange(opts.StartDate, opts.EndDate)
	if err != nil {
		return fmt.Errorf("date range: %w", err)
	}

	logger.Info("开始批量下载",
		"日期范围", fmt.Sprintf("%s ~ %s", dates[0], dates[len(dates)-1]),
		"表", tables,
		"输出", opts.Output,
		"并发数", opts.Concurrency,
	)

	// Prepare MinIO client if needed
	var mc *minio.Client
	if opts.Output == "minio" {
		mc, err = minio.NewClient(ctx, opts.MinioEndpoint, opts.MinioAccessKey,
			opts.MinioSecretKey, opts.MinioBucket, opts.MinioSecure, logger)
		if err != nil {
			return fmt.Errorf("minio client: %w", err)
		}
	}

	httpClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        opts.Concurrency * 2,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  false,
		},
	}

	// Worker pool
	taskCh := make(chan downloadTask, opts.Concurrency*2)
	var wg sync.WaitGroup
	results := make(chan downloadResult, opts.Concurrency*2)

	// Start workers
	for range opts.Concurrency {
		wg.Add(1)
		go worker(ctx, httpClient, mc, opts, taskCh, results, &wg)
	}

	// Producer: generate all tasks (15分钟时间片优先)
	go func() {
		defer close(taskCh)
		for _, date := range dates {
			select {
			case <-ctx.Done():
				return
			default:
			}

			timeSlices := TimeSlices(date)
			for _, ts := range timeSlices {
				for _, table := range tables {
					url := URLForTimestamp(opts.BaseURL, table, ts)
					task := downloadTask{
						url:       url,
						table:     table,
						date:      date,
						timestamp: ts,
					}
					select {
					case taskCh <- task:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	// Close results channel when all workers are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Process results
	var totalDownloaded, totalSkipped, totalNotFound, totalErrors int
	progressTicker := time.NewTicker(5 * time.Second)
	defer progressTicker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for r := range results {
			if r.err != nil {
				totalErrors++
				logger.Error("下载失败", "url", r.url, "error", r.err)
				consecutiveFailures.Add(1)
			} else if r.notFound {
				totalNotFound++
				consecutiveFailures.Store(0)
			} else if r.skipped {
				totalSkipped++
			} else {
				totalDownloaded++
				consecutiveFailures.Store(0)
			}

			// Check consecutive failures threshold
			if consecutiveFailures.Load() >= maxConsecutiveFailures {
				logger.Error("连续下载失败次数过多，停止", "count", maxConsecutiveFailures)
				// Don't kill the process, just log a warning
				consecutiveFailures.Store(0)
			}
		}
	}()

	// Progress reporting
	tickerDone := make(chan struct{})
	go func() {
		defer close(tickerDone)
		for {
			select {
			case <-progressTicker.C:
				logger.Info("下载进度",
					"已下载", totalDownloaded,
					"已跳过", totalSkipped,
					"无数据(404)", totalNotFound,
					"失败", totalErrors,
				)
			case <-done:
				return
			}
		}
	}()

	<-done
	<-tickerDone

	logger.Info("批量下载完成",
		"已下载", totalDownloaded,
		"已跳过(已存在)", totalSkipped,
		"无数据(404)", totalNotFound,
		"失败", totalErrors,
	)

	if totalDownloaded > 0 {
		if opts.Output == "local" {
			logger.Info("文件保存到本地目录", "路径", opts.DataDir)
		} else {
			logger.Info("文件保存到 MinIO", "存储桶", opts.MinioBucket, "前缀", opts.ZipPrefix)
		}
	}

	return nil
}

type downloadTask struct {
	url       string
	table     string
	date      string
	timestamp string
}

type downloadResult struct {
	url      string
	err      error
	skipped  bool
	notFound bool
}

func worker(ctx context.Context, httpClient *http.Client, mc *minio.Client, opts *DownloadOptions,
	tasks <-chan downloadTask, results chan<- downloadResult, wg *sync.WaitGroup) {
	defer wg.Done()

	for task := range tasks {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result := processTask(ctx, httpClient, mc, opts, task)
		results <- result
	}
}

func processTask(ctx context.Context, httpClient *http.Client, mc *minio.Client, opts *DownloadOptions, task downloadTask) downloadResult {
	filename := fmt.Sprintf("%s.%s", task.timestamp, Suffix(task.table))

	if opts.Output == "local" {
		return downloadToLocal(ctx, httpClient, opts, task, filename)
	}
	return downloadToMinio(ctx, httpClient, mc, opts, task, filename)
}

func downloadToLocal(ctx context.Context, httpClient *http.Client, opts *DownloadOptions, task downloadTask, filename string) downloadResult {
	filePath := BuildLocalPath(opts.DataDir, task.table, task.date, filename, opts.Flat)

	// Check if file already exists
	if FileExists(filePath) {
		opts.Logger.Debug("文件已存在，跳过", "文件", filePath)
		return downloadResult{url: task.url, skipped: true}
	}

	err := DownloadFile(ctx, httpClient, task.url, filePath, opts.HostHeader, opts.Logger)
	if err != nil {
		if isNotFound(err) {
			return downloadResult{url: task.url, notFound: true}
		}
		opts.Logger.Error("下载失败", "URL", task.url, "文件", filePath, "错误", err)
		return downloadResult{url: task.url, err: err}
	}

	opts.Logger.Info("下载成功", "文件", filePath)
	return downloadResult{url: task.url}
}

func downloadToMinio(ctx context.Context, httpClient *http.Client, mc *minio.Client, opts *DownloadOptions, task downloadTask, filename string) downloadResult {
	objectName := minio.BuildObjectKey(opts.ZipPrefix, task.table, task.date, filename, opts.Flat)

	// Check if already exists
	exists, remoteSize, err := mc.ObjectExists(ctx, objectName)
	if err != nil {
		opts.Logger.Warn("检查MinIO对象失败，将重新上传", "object", objectName, "error", err)
	} else if exists && remoteSize > 0 {
		opts.Logger.Debug("MinIO对象已存在，跳过", "object", objectName)
		return downloadResult{url: task.url, skipped: true}
	}

	// Download to memory
	data, err := DownloadToMemory(ctx, httpClient, task.url, opts.HostHeader)
	if err != nil {
		opts.Logger.Error("下载失败", "URL", task.url, "错误", err)
		return downloadResult{url: task.url, err: err}
	}
	if data == nil {
		return downloadResult{url: task.url, notFound: true}
	}

	// Upload to MinIO
	if err := mc.UploadBytes(ctx, objectName, data); err != nil {
		opts.Logger.Error("上传MinIO失败", "object", objectName, "错误", err)
		return downloadResult{url: task.url, err: fmt.Errorf("upload to minio: %w", err)}
	}

	opts.Logger.Info("下载成功", "object", fmt.Sprintf("minio://%s/%s", opts.MinioBucket, objectName))
	return downloadResult{url: task.url}
}

// DownloadTodayToLatest downloads from today to the most recent 15-minute slice.
func DownloadTodayToLatest(ctx context.Context, opts *DownloadOptions) error {
	opts.StartDate = time.Now().UTC().Format("20060102")
	opts.EndDate = ""
	return BatchDownload(ctx, opts)
}

// ScanLocalDir scans a local directory for existing Hive-partitioned files and
// returns a set of "table/date/timestamp" keys that have been downloaded.
func ScanLocalDir(dataDir string, tables []string, flat bool) (map[string]struct{}, error) {
	existing := make(map[string]struct{})

	for _, table := range tables {
		tableDir := filepath.Join(dataDir, table)
		if _, err := os.Stat(tableDir); os.IsNotExist(err) {
			continue
		}

		if flat {
			// Flat mode: just scan for .zip files
			entries, err := os.ReadDir(tableDir)
			if err != nil {
				return nil, fmt.Errorf("read dir %s: %w", tableDir, err)
			}
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".zip" {
					continue
				}
				// Extract timestamp from filename (first 14 chars before .)
				name := entry.Name()
				if len(name) >= 14 {
					ts := name[:14]
					existing[table+"/"+ts] = struct{}{}
				}
			}
		} else {
			// Hive mode: table/year=YYYY/month=MM/day=DD/*.zip
			err := filepath.WalkDir(tableDir, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil // skip inaccessible dirs
				}
				if d.IsDir() || filepath.Ext(d.Name()) != ".zip" {
					return nil
				}
				// Extract date from path
				name := d.Name()
				if len(name) >= 14 {
					ts := name[:14]
					dateStr := ts[:8]
					existing[table+"/"+dateStr+"/"+ts] = struct{}{}
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("walk dir %s: %w", tableDir, err)
			}
		}
	}

	return existing, nil
}

// MinioClientForConfig creates a MinIO client from DownloadOptions, or nil if output is local.
func MinioClientForConfig(ctx context.Context, opts *DownloadOptions, logger *slog.Logger) (*minio.Client, error) {
	if opts.Output != "minio" {
		return nil, nil
	}
	return minio.NewClient(ctx, opts.MinioEndpoint, opts.MinioAccessKey,
		opts.MinioSecretKey, opts.MinioBucket, opts.MinioSecure, logger)
}
