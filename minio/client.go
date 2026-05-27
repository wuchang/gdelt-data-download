// Package minio wraps minio-go for GDELT ZIP upload and inspection.
package minio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client wraps the minio-go client with convenience methods.
type Client struct {
	mc     *minio.Client
	bucket string
	logger *slog.Logger
}

// NewClient creates a new MinIO client.
func NewClient(ctx context.Context, endpoint, accessKey, secretKey, bucket string, secure bool, logger *slog.Logger) (*Client, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	client := &Client{mc: mc, bucket: bucket, logger: logger}

	// Ensure bucket exists
	if err := client.ensureBucket(ctx); err != nil {
		return nil, fmt.Errorf("ensure bucket %s: %w", bucket, err)
	}

	return client, nil
}

func (c *Client) ensureBucket(ctx context.Context) error {
	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return c.mc.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{})
	}
	return nil
}

// ObjectExists checks if an object exists in the bucket and returns its size.
// Returns exists=false, size=0 if the object is not found.
func (c *Client) ObjectExists(ctx context.Context, objectName string) (exists bool, size int64, err error) {
	info, err := c.mc.StatObject(ctx, c.bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		errResponse := minio.ToErrorResponse(err)
		if errResponse.Code == "NoSuchKey" || errResponse.Code == "NotFound" {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("stat object %s: %w", objectName, err)
	}
	return true, info.Size, nil
}

// Upload uploads data from a reader to the specified object.
func (c *Client) Upload(ctx context.Context, objectName string, reader io.Reader, size int64) error {
	_, err := c.mc.PutObject(ctx, c.bucket, objectName, reader, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("upload %s: %w", objectName, err)
	}
	return nil
}

// UploadBytes uploads a byte slice to the specified object.
func (c *Client) UploadBytes(ctx context.Context, objectName string, data []byte) error {
	return c.Upload(ctx, objectName, bytes.NewReader(data), int64(len(data)))
}

// ListPrefix lists objects under a given prefix.
func (c *Client) ListPrefix(ctx context.Context, prefix string) ([]minio.ObjectInfo, error) {
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	var objects []minio.ObjectInfo
	for obj := range c.mc.ListObjects(ctx, c.bucket, opts) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list objects %s: %w", prefix, obj.Err)
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

// ListPrefixNames returns object names (keys) under a prefix.
func (c *Client) ListPrefixNames(ctx context.Context, prefix string) ([]string, error) {
	objects, err := c.ListPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(objects))
	for i, obj := range objects {
		names[i] = obj.Key
	}
	return names, nil
}

// BuildObjectKey constructs a Hive-partitioned or flat object key.
// hivePath: "{prefix}/{table}/year=YYYY/month=MM/day=DD/{filename}"
// flatPath: "{prefix}/{table}/{filename}"
func BuildObjectKey(prefix, table, dateStr, filename string, flat bool) string {
	if flat {
		return fmt.Sprintf("%s/%s/%s", strings.TrimRight(prefix, "/"), table, filename)
	}
	year := dateStr[:4]
	month := dateStr[4:6]
	day := dateStr[6:8]
	return fmt.Sprintf("%s/%s/year=%s/month=%s/day=%s/%s",
		strings.TrimRight(prefix, "/"), table, year, month, day, filename)
}

// ParseObjectKeyDate attempts to extract YYYYMMDD from a Hive-partitioned key.
func ParseObjectKeyDate(key string) string {
	// Expected: .../year=YYYY/month=MM/day=DD/...
	parts := strings.Split(key, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "year=") && i+2 < len(parts) {
			year := strings.TrimPrefix(part, "year=")
			month := strings.TrimPrefix(parts[i+1], "month=")
			day := strings.TrimPrefix(parts[i+2], "day=")
			if len(year) == 4 && len(month) == 2 && len(day) == 2 {
				return year + month + day
			}
		}
	}
	return ""
}
