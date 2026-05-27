package downloader

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	maxRetries            = 3
	baseBackoff           = 1 * time.Second
	maxConsecutiveFailures = 10
)

// Result holds the outcome of a single file download.
type Result struct {
	URL      string
	FilePath string // empty if skipped or 404
	Skipped  bool   // true if file already existed
	NotFound bool   // true if 404 (no data for this time slice)
	Err      error
}

// DownloadFile downloads a single URL to the given path with retries.
// It writes to a .tmp file first, then atomically renames on success.
func DownloadFile(ctx context.Context, client *http.Client, url, filePath string, hostHeader string, logger *slog.Logger) error {
	// Ensure parent directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpPath := filePath + ".tmp"

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := baseBackoff * (1 << (attempt - 1))
			logger.Debug("retry", "url", url, "attempt", attempt+1, "backoff", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		lastErr = func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			if hostHeader != "" {
				req.Host = hostHeader
			}

			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("http get: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusNotFound {
				return errNotFound{}
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("unexpected status %d", resp.StatusCode)
			}

			f, err := os.Create(tmpPath)
			if err != nil {
				return fmt.Errorf("create temp file: %w", err)
			}
			defer f.Close()

			written, err := io.Copy(f, resp.Body)
			if err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("download body: %w", err)
			}

			if err := f.Sync(); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("sync temp file: %w", err)
			}
			if err := f.Close(); err != nil {
				os.Remove(tmpPath)
				return fmt.Errorf("close temp file: %w", err)
			}

			if written == 0 {
				os.Remove(tmpPath)
				return fmt.Errorf("downloaded file is empty")
			}

			if err := os.Rename(tmpPath, filePath); err != nil {
				return fmt.Errorf("rename temp to final: %w", err)
			}

			return nil
		}()
		if lastErr == nil {
			return nil
		}
		// Don't retry 404s
		if isNotFound(lastErr) {
			return lastErr
		}
	}
	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// DownloadToMemory downloads a URL into a byte slice.
// Returns the bytes, or nil with a nil error on 404.
func DownloadToMemory(ctx context.Context, client *http.Client, url string, hostHeader string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := baseBackoff * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		lastErr = func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			if hostHeader != "" {
				req.Host = hostHeader
			}

			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("http get: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusNotFound {
				return errNotFound{}
			}
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("unexpected status %d", resp.StatusCode)
			}

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("read body: %w", err)
			}
			if len(data) == 0 {
				return fmt.Errorf("downloaded content is empty")
			}

			return nil
		}()
		if lastErr == nil {
			return nil, nil
		}
		if isNotFound(lastErr) {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

// errNotFound is a sentinel for 404 responses.
type errNotFound struct{}

func (e errNotFound) Error() string {
	return "404 not found"
}

func isNotFound(err error) bool {
	_, ok := err.(errNotFound)
	return ok
}

// FileExists checks if a file exists and has a non-zero size.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() > 0
}
