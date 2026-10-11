// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Ops wraps the S3 bucket-level operations the control plane needs.
type Ops struct {
	S3 *s3.Client
}

func (o *Ops) HeadBucket(ctx context.Context, name string) error {
	_, err := o.S3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(name)})
	return Classify(err)
}

func (o *Ops) PutBucket(ctx context.Context, name string) error {
	_, err := o.S3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)})
	err = Classify(err)
	if errors.Is(err, ErrBucketExists) {
		return nil
	}
	return err
}

// EncryptionRule is the bucket-default SSE rule the operator asserts.
type EncryptionRule struct {
	SSEAlgorithm string
	KMSKeyID     string
}

// EnsureBucketEncryption makes the bucket's default encryption match rule.
// It reads first and only writes on a difference, so a reconcile against a
// correctly configured bucket costs one GET. A backend that does not
// implement bucket encryption surfaces its error unchanged: the caller
// holds the claim not-Ready rather than hand out a plaintext bucket.
func (o *Ops) EnsureBucketEncryption(ctx context.Context, name string, rule EncryptionRule) error {
	want := s3types.ServerSideEncryptionByDefault{SSEAlgorithm: s3types.ServerSideEncryption(rule.SSEAlgorithm)}
	if rule.KMSKeyID != "" {
		want.KMSMasterKeyID = aws.String(rule.KMSKeyID)
	}
	cur, err := o.S3.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(name)})
	if err == nil && cur.ServerSideEncryptionConfiguration != nil {
		for _, r := range cur.ServerSideEncryptionConfiguration.Rules {
			d := r.ApplyServerSideEncryptionByDefault
			if d != nil && d.SSEAlgorithm == want.SSEAlgorithm && aws.ToString(d.KMSMasterKeyID) == aws.ToString(want.KMSMasterKeyID) {
				return nil
			}
		}
	} else if err != nil && !isNoEncryptionConfig(err) {
		return fmt.Errorf("get bucket encryption: %w", Classify(err))
	}
	_, err = o.S3.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: aws.String(name),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{{ApplyServerSideEncryptionByDefault: &want}},
		},
	})
	if err != nil {
		return fmt.Errorf("put bucket encryption: %w", Classify(err))
	}
	return nil
}

// isNoEncryptionConfig reports the S3 "no default encryption configured"
// answer, which is the normal state of a freshly created bucket.
func isNoEncryptionConfig(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode() == "ServerSideEncryptionConfigurationNotFoundError"
	}
	return false
}

func (o *Ops) DeleteBucket(ctx context.Context, name string) error {
	_, err := o.S3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)})
	return Classify(err)
}

func (o *Ops) ListBuckets(ctx context.Context) ([]string, error) {
	out, err := o.S3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, Classify(err)
	}
	names := make([]string, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		if b.Name != nil {
			names = append(names, *b.Name)
		}
	}
	return names, nil
}

// EmptyBucket lists and deletes every object and in-flight multipart upload
// in the named bucket. Safe to call on an already-empty bucket.
func (o *Ops) EmptyBucket(ctx context.Context, name string) error {
	// Abort in-flight multipart uploads first.
	uploadsPaginator := s3.NewListMultipartUploadsPaginator(o.S3, &s3.ListMultipartUploadsInput{Bucket: aws.String(name)})
	for uploadsPaginator.HasMorePages() {
		page, err := uploadsPaginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list multipart uploads: %w", Classify(err))
		}
		for _, u := range page.Uploads {
			_, err := o.S3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(name),
				Key:      u.Key,
				UploadId: u.UploadId,
			})
			if err != nil {
				return fmt.Errorf("abort multipart: %w", Classify(err))
			}
		}
	}

	// BulkDelete in pages of 1000.
	objPaginator := s3.NewListObjectsV2Paginator(o.S3, &s3.ListObjectsV2Input{Bucket: aws.String(name)})
	for objPaginator.HasMorePages() {
		page, err := objPaginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list objects: %w", Classify(err))
		}
		if len(page.Contents) == 0 {
			continue
		}
		ids := make([]s3types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			ids = append(ids, s3types.ObjectIdentifier{Key: obj.Key})
		}
		_, err = o.S3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(name),
			Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("delete objects: %w", Classify(err))
		}
	}
	return nil
}
