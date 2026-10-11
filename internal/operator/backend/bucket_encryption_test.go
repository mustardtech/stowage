// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package backend

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeS3 answers GetBucketEncryption from a canned body (or the S3 "not
// configured" error) and records whether a PutBucketEncryption arrived.
type fakeS3 struct {
	mu      sync.Mutex
	getBody string // "" => ServerSideEncryptionConfigurationNotFoundError
	puts    []string
}

func (f *fakeS3) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("encryption") || strings.Contains(r.URL.RawQuery, "encryption") {
		switch r.Method {
		case http.MethodGet:
			f.mu.Lock()
			body := f.getBody
			f.mu.Unlock()
			if body == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code><Message>none</Message></Error>`)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, body)
			return
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.puts = append(f.puts, string(b))
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func newOpsAgainst(t *testing.T, h http.HandlerFunc) *Ops {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(Config{Endpoint: srv.URL, Region: "us-east-1", AccessKeyID: "k", SecretAccessKey: "s", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return &Ops{S3: c}
}

const kmsBody = `<?xml version="1.0" encoding="UTF-8"?><ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>aws:kms</SSEAlgorithm><KMSMasterKeyID>tenant-s3</KMSMasterKeyID></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`

func TestEnsureBucketEncryption_PutsWhenUnset(t *testing.T) {
	f := &fakeS3{}
	ops := newOpsAgainst(t, f.handler)
	if err := ops.EnsureBucketEncryption(context.Background(), "b", EncryptionRule{SSEAlgorithm: "aws:kms", KMSKeyID: "tenant-s3"}); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 1 {
		t.Fatalf("want 1 PutBucketEncryption, got %d", len(f.puts))
	}
	if !strings.Contains(f.puts[0], "aws:kms") || !strings.Contains(f.puts[0], "tenant-s3") {
		t.Fatalf("put body missing rule: %s", f.puts[0])
	}
}

func TestEnsureBucketEncryption_NoopWhenMatching(t *testing.T) {
	f := &fakeS3{getBody: kmsBody}
	ops := newOpsAgainst(t, f.handler)
	if err := ops.EnsureBucketEncryption(context.Background(), "b", EncryptionRule{SSEAlgorithm: "aws:kms", KMSKeyID: "tenant-s3"}); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 0 {
		t.Fatalf("matching rule must not be re-put, got %d puts", len(f.puts))
	}
}

func TestEnsureBucketEncryption_PutsWhenKeyDiffers(t *testing.T) {
	f := &fakeS3{getBody: kmsBody}
	ops := newOpsAgainst(t, f.handler)
	if err := ops.EnsureBucketEncryption(context.Background(), "b", EncryptionRule{SSEAlgorithm: "aws:kms", KMSKeyID: "other-s3"}); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 1 || !strings.Contains(f.puts[0], "other-s3") {
		t.Fatalf("differing key must be re-put, got %v", f.puts)
	}
}

func TestEnsureBucketEncryption_BackendRefusalSurfaces(t *testing.T) {
	ops := newOpsAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "encryption") {
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `<Error><Code>NotImplemented</Code><Message>no</Message></Error>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := ops.EnsureBucketEncryption(context.Background(), "b", EncryptionRule{SSEAlgorithm: "AES256"}); err == nil {
		t.Fatal("a backend that cannot set default encryption must return an error, not a plaintext bucket")
	}
}
