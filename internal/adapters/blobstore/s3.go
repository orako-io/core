// SPDX-License-Identifier: AGPL-3.0-or-later

// Package blobstore is the S3-compatible attachment byte store. It satisfies
// service.BlobStore over any S3 API — AWS S3, Supabase Storage, MinIO,
// Cloudflare R2 — via the AWS SDK v2 with a custom base endpoint and path-style
// addressing (Supabase's endpoint carries a /storage/v1/s3 path, which the
// minio-go client rejects; the AWS SDK accepts it). Object storage is the
// single new hard dependency for attachments; when the S3 env is absent the
// composition root wires blobstore.Noop instead and attachment
// features degrade off.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// compile-time port satisfaction.

// Config holds the S3-compatible connection parameters.
type Config struct {
	// Endpoint is the FULL base URL of the S3 API, scheme and any path included:
	//   AWS       https://s3.eu-west-3.amazonaws.com
	//   Supabase  https://<project_ref>.supabase.co/storage/v1/s3
	//   MinIO     http://localhost:9000
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// S3Store is the S3-compatible blob store.
type S3Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// New builds an S3Store. It does not preflight the bucket (some S3-compatible
// backends reject HeadBucket): credential/bucket errors surface on first use.
func New(_ context.Context, cfg Config) (*S3Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("blobstore: endpoint and bucket are required")
	}

	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		// Path-style ("<endpoint>/<bucket>/<key>") is required by Supabase and
		// MinIO and works everywhere; virtual-hosted style would break them.
		UsePathStyle: true,
	})

	return &S3Store{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

// Put stores size bytes read from r under key with the given content type.
func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	}); err != nil {
		return fmt.Errorf("blobstore: putting %q: %w", key, err)
	}

	return nil
}

// Get opens the object at key for reading.
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("blobstore: getting %q: %w", key, err)
	}

	return out.Body, nil
}

// SignedGetURL returns a presigned URL granting read access to key for ttl.
func (s *S3Store) SignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blobstore: presigning %q: %w", key, err)
	}

	return req.URL, nil
}

// Delete removes the object at key. A missing object is not an error on S3.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("blobstore: deleting %q: %w", key, err)
	}

	return nil
}

// Enabled reports true — a real S3 store is wired.
func (s *S3Store) Enabled() bool { return true }
