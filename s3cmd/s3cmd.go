// Package s3cmd provides one-shot S3-compatible object operations
// (upload / download / delete) for the `launch-agent` CLI. It exists so
// the platform can drive backups, restores, and retention pruning on a
// server WITHOUT installing the `aws` CLI — the agent already links the
// AWS SDK for its storage backend, so we reuse it.
//
// These commands are intentionally config-file-free: every parameter
// arrives via flags/env on a single invocation. Credentials come from
// the environment (AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY) so they
// never appear in `ps` output — matching the convention the old
// `aws s3 …` scripts used.
package s3cmd

import (
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

// Auth + destination common to every operation. Credentials are kept
// separate from the (non-secret) target fields so callers can source
// them from the environment.
type Config struct {
	Bucket          string
	Region          string
	Endpoint        string // optional; set for non-AWS S3 (Spaces, B2, Wasabi, MinIO)
	ForcePathStyle  bool   // true for most non-AWS S3 providers
	AccessKeyID     string // from env AWS_ACCESS_KEY_ID
	SecretAccessKey string // from env AWS_SECRET_ACCESS_KEY
}

func (c Config) validate() error {
	switch {
	case c.Bucket == "":
		return errors.New("bucket is required")
	case c.AccessKeyID == "" || c.SecretAccessKey == "":
		return errors.New("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be set in the environment")
	}
	return nil
}

// newSession builds an AWS session from the config. Shared by all ops.
func (c Config) newSession() (*session.Session, error) {
	cfg := aws.NewConfig().
		WithRegion(c.Region).
		WithCredentials(credentials.NewStaticCredentials(c.AccessKeyID, c.SecretAccessKey, ""))
	if c.Endpoint != "" {
		cfg = cfg.WithEndpoint(c.Endpoint)
	}
	if c.ForcePathStyle {
		cfg = cfg.WithS3ForcePathStyle(true)
	}
	return session.NewSession(cfg)
}

// Upload streams a local file to s3://<bucket>/<key>. StorageClass is
// applied only when non-empty (some backends reject it). Returns the
// remote location on success.
func Upload(cfg Config, filePath, key, storageClass string) (string, error) {
	if err := cfg.validate(); err != nil {
		return "", err
	}
	if filePath == "" || key == "" {
		return "", errors.New("file and key are required")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", filePath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", filePath, err)
	}
	if fi.Size() == 0 {
		return "", fmt.Errorf("refusing to upload empty file %q", filePath)
	}

	sess, err := cfg.newSession()
	if err != nil {
		return "", fmt.Errorf("create aws session: %w", err)
	}

	input := &s3manager.UploadInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
		Body:   f,
	}
	if storageClass != "" {
		input.StorageClass = aws.String(storageClass)
	}

	result, err := s3manager.NewUploader(sess).Upload(input, func(u *s3manager.Uploader) {
		// Small-ish parts + single stream for friendliness with flaky/
		// non-AWS endpoints; bump part size only when the 10k-part
		// ceiling demands it.
		var partSize int64 = 64 * 1024 * 1024 // 64 MiB
		if maxParts := fi.Size() / partSize; maxParts > 10000 {
			partSize = int64(math.Ceil(float64(fi.Size()) / 10000))
		}
		u.Concurrency = 1
		u.LeavePartsOnError = false
		u.PartSize = partSize
	})
	if err != nil {
		return "", fmt.Errorf("upload to s3://%s/%s: %w", cfg.Bucket, key, err)
	}
	return result.Location, nil
}

// Download fetches s3://<bucket>/<key> to destPath.
func Download(cfg Config, key, destPath string) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if key == "" || destPath == "" {
		return errors.New("key and dest are required")
	}

	sess, err := cfg.newSession()
	if err != nil {
		return fmt.Errorf("create aws session: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %q: %w", destPath, err)
	}
	defer f.Close()

	n, err := s3manager.NewDownloader(sess).Download(f, &s3.GetObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("download s3://%s/%s: %w", cfg.Bucket, key, err)
	}
	if n == 0 {
		return fmt.Errorf("downloaded 0 bytes from s3://%s/%s", cfg.Bucket, key)
	}
	return nil
}

// Delete removes s3://<bucket>/<key>. S3 DeleteObject is idempotent, so
// deleting a key that doesn't exist succeeds — which is the behaviour
// the retention-prune loop wants (a missing object is a benign no-op).
func Delete(cfg Config, key string) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("key is required")
	}

	sess, err := cfg.newSession()
	if err != nil {
		return fmt.Errorf("create aws session: %w", err)
	}

	_, err = s3.New(sess).DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// NoSuchKey is not an error for our purposes.
		var aerr awserr.Error
		if errors.As(err, &aerr) && aerr.Code() == s3.ErrCodeNoSuchKey {
			return nil
		}
		return fmt.Errorf("delete s3://%s/%s: %w", cfg.Bucket, key, err)
	}
	return nil
}
