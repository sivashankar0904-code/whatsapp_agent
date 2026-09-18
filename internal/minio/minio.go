// Package minio publishes the WhatsApp pairing QR to S3-compatible object
// storage, so it can be opened from a browser when the agent runs in a
// container with no display.
package minio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	miniogo "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// Object is the key the pairing QR is published under. The container has no
// display and no useful filesystem, so the PNG goes to object storage where
// it can be opened from a browser, and is deleted as soon as pairing
// succeeds — a live pairing code is a credential.
const Object = "qr.png"

// Config holds the discrete connection parameters, read from WA_S3_* env
// vars. Endpoint empty means object storage is not configured.
type Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// LoadConfig reads Config from the environment.
func LoadConfig() Config {
	bucket := os.Getenv("WA_S3_BUCKET")
	if bucket == "" {
		bucket = "whatsapp-agent"
	}
	return Config{
		Endpoint:  os.Getenv("WA_S3_ENDPOINT"),
		Bucket:    bucket,
		AccessKey: os.Getenv("WA_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("WA_S3_SECRET_KEY"),
		UseSSL:    strings.EqualFold(os.Getenv("WA_S3_USE_SSL"), "true"),
	}
}

// Store publishes the pairing QR to S3-compatible object storage.
type Store struct {
	client *miniogo.Client
	Bucket string
	logger waLog.Logger
}

// New returns nil (not an error) when object storage is not configured
// (cfg.Endpoint == ""), so pairing still works with terminal output alone.
func New(ctx context.Context, cfg Config, logger waLog.Logger) (*Store, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}

	client, err := miniogo.New(cfg.Endpoint, &miniogo.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket %q: %w", cfg.Bucket, err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, miniogo.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket %q: %w", cfg.Bucket, err)
		}
		logger.Infof("created bucket %s", cfg.Bucket)
	}
	return &Store{client: client, Bucket: cfg.Bucket, logger: logger}, nil
}

// Put uploads the QR PNG, replacing any previous one.
func (s *Store) Put(ctx context.Context, png []byte) error {
	_, err := s.client.PutObject(ctx, s.Bucket, Object,
		bytes.NewReader(png), int64(len(png)),
		miniogo.PutObjectOptions{ContentType: "image/png"})
	return err
}

// Remove deletes the published QR. Called once pairing succeeds or the code
// expires, so a scannable credential is not left in the bucket.
func (s *Store) Remove(ctx context.Context) {
	if err := s.client.RemoveObject(ctx, s.Bucket, Object,
		miniogo.RemoveObjectOptions{}); err != nil {
		s.logger.Warnf("remove %s from bucket: %v", Object, err)
	}
}
