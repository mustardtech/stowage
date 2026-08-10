// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/stretchr/testify/require"

	"github.com/stowage-dev/stowage/internal/sigv4verifier"
)

// multipartUpstream is a fake S3 backend that implements just enough of the
// multipart API surface for the presigned end-to-end tests. It records every
// request so tests can assert what actually reached the backend.
type multipartUpstream struct {
	mu    sync.Mutex
	parts map[string][]byte // "uploadId/partNumber" -> body
	seen  []*http.Request
}

func newMultipartUpstream() *multipartUpstream {
	return &multipartUpstream{parts: map[string][]byte{}}
}

func (u *multipartUpstream) record(r *http.Request) {
	cp := r.Clone(context.Background())
	u.mu.Lock()
	u.seen = append(u.seen, cp)
	u.mu.Unlock()
}

func (u *multipartUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.record(r)
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`,
			strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0], "big.bin")
	case r.Method == http.MethodPut && q.Has("partNumber") && q.Has("uploadId"):
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.parts[q.Get("uploadId")+"/"+q.Get("partNumber")] = b
		u.mu.Unlock()
		w.Header().Set("ETag", `"part-etag-`+q.Get("partNumber")+`"`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		w.Header().Set("Content-Type", "application/xml")
		// Location deliberately points at this (upstream) server — the proxy
		// must rewrite it before the client sees it.
		fmt.Fprintf(w, `<CompleteMultipartUploadResult><Location>http://%s%s</Location><Bucket>real-bucket</Bucket><Key>big.bin</Key><ETag>"final-etag-2"</ETag></CompleteMultipartUploadResult>`,
			r.Host, r.URL.Path)
	case r.Method == http.MethodGet && q.Has("uploadId"):
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<ListPartsResult><Part><PartNumber>1</PartNumber><ETag>"part-etag-1"</ETag></Part></ListPartsResult>`)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

// presignProxyURL presigns method+rawURL against the proxy with the virtual
// credential, mirroring what an SDK's presigner produces (X-Amz-Expires is
// part of the signed query).
func presignProxyURL(t *testing.T, method, rawURL, akid, sk string) string {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	require.NoError(t, err)
	req.Host = req.URL.Host
	q := req.URL.Query()
	q.Set("X-Amz-Expires", "300")
	req.URL.RawQuery = q.Encode()
	signer := v4.NewSigner()
	uri, _, err := signer.PresignHTTP(context.Background(),
		aws.Credentials{AccessKeyID: akid, SecretAccessKey: sk},
		req, sigv4verifier.UnsignedPayload, "s3", "us-east-1", time.Now().UTC(),
	)
	require.NoError(t, err)
	return uri
}

func (u *multipartUpstream) lastRequest() *http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.seen) == 0 {
		return nil
	}
	return u.seen[len(u.seen)-1]
}

// TestProxy_PresignedMultipart drives the full multipart lifecycle through
// the proxy using only presigned URLs, the way a browser or curl would after
// receiving SDK-presigned links.
func TestProxy_PresignedMultipart(t *testing.T) {
	ups := httptest.NewServer(newMultipartUpstream())
	defer ups.Close()
	mu := ups.Config.Handler.(*multipartUpstream)

	vc := newCred("AKIATESTCLAIM0000000",
		"secretsecretsecretsecretsecretsecretsecr",
		"my-app-uploads", "primary")
	proxy := newTestServer(t, ups, vc)
	defer proxy.Close()
	base := proxy.URL + "/my-app-uploads/big.bin"

	do := func(method, uri string, body io.Reader) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, uri, body)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	// CreateMultipartUpload: POST ?uploads
	resp := do(http.MethodPost, presignProxyURL(t, http.MethodPost, base+"?uploads", vc.AccessKeyID, vc.SecretAccessKey), nil)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	require.Contains(t, string(b), "<UploadId>upload-1</UploadId>")

	// UploadPart: PUT ?partNumber=1&uploadId=upload-1
	partBody := []byte("part one bytes")
	partURL := presignProxyURL(t, http.MethodPut, base+"?partNumber=1&uploadId=upload-1", vc.AccessKeyID, vc.SecretAccessKey)
	req, err := http.NewRequest(http.MethodPut, partURL, bytes.NewReader(partBody))
	require.NoError(t, err)
	req.ContentLength = int64(len(partBody))
	req.Header.Set("X-Amz-Checksum-Crc32", "AAAAAA==")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, `"part-etag-1"`, resp.Header.Get("ETag"))

	// The backend must have received the exact part bytes, the multipart
	// query params, the checksum header — and none of the presigned params.
	mu.mu.Lock()
	require.Equal(t, partBody, mu.parts["upload-1/1"])
	mu.mu.Unlock()
	last := mu.lastRequest()
	require.Equal(t, "1", last.URL.Query().Get("partNumber"))
	require.Equal(t, "upload-1", last.URL.Query().Get("uploadId"))
	require.Equal(t, "AAAAAA==", last.Header.Get("X-Amz-Checksum-Crc32"))
	require.Empty(t, last.URL.Query().Get("X-Amz-Signature"))
	require.NotEmpty(t, last.Header.Get("Authorization"), "outbound must be re-signed via header")

	// ListParts: GET ?uploadId
	resp = do(http.MethodGet, presignProxyURL(t, http.MethodGet, base+"?uploadId=upload-1", vc.AccessKeyID, vc.SecretAccessKey), nil)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(b), "<PartNumber>1</PartNumber>")

	// CompleteMultipartUpload: POST ?uploadId — Location must be rewritten
	// to the proxy, not leak the upstream endpoint.
	completeBody := strings.NewReader(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"part-etag-1"</ETag></Part></CompleteMultipartUpload>`)
	resp = do(http.MethodPost, presignProxyURL(t, http.MethodPost, base+"?uploadId=upload-1", vc.AccessKeyID, vc.SecretAccessKey), completeBody)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	upsURL, _ := url.Parse(ups.URL)
	require.NotContains(t, string(b), upsURL.Host, "upstream endpoint must not leak")
	proxyURL, _ := url.Parse(proxy.URL)
	require.Contains(t, string(b), "<Location>http://"+proxyURL.Host+"/my-app-uploads/big.bin</Location>")
	require.Contains(t, string(b), `<ETag>"final-etag-2"</ETag>`)

	// AbortMultipartUpload: DELETE ?uploadId
	resp = do(http.MethodDelete, presignProxyURL(t, http.MethodDelete, base+"?uploadId=upload-1", vc.AccessKeyID, vc.SecretAccessKey), nil)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// TestProxy_PresignedUploadPart_AwsChunked: a presigned part upload sent with
// aws-chunked framing must be decoded before it reaches the backend — the
// stored bytes are the payload, not the framing.
func TestProxy_PresignedUploadPart_AwsChunked(t *testing.T) {
	ups := httptest.NewServer(newMultipartUpstream())
	defer ups.Close()
	mu := ups.Config.Handler.(*multipartUpstream)

	vc := newCred("AKIATESTCLAIM0000000",
		"secretsecretsecretsecretsecretsecretsecr",
		"my-app-uploads", "primary")
	proxy := newTestServer(t, ups, vc)
	defer proxy.Close()

	payload := []byte("chunked part payload")
	var framed bytes.Buffer
	fmt.Fprintf(&framed, "%x\r\n", len(payload))
	framed.Write(payload)
	framed.WriteString("\r\n0\r\n")
	framed.WriteString("x-amz-checksum-crc32:AAAAAA==\r\n\r\n")

	uri := presignProxyURL(t, http.MethodPut,
		proxy.URL+"/my-app-uploads/big.bin?partNumber=2&uploadId=upload-1",
		vc.AccessKeyID, vc.SecretAccessKey)
	req, err := http.NewRequest(http.MethodPut, uri, bytes.NewReader(framed.Bytes()))
	require.NoError(t, err)
	req.ContentLength = int64(framed.Len())
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("X-Amz-Decoded-Content-Length", fmt.Sprint(len(payload)))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	mu.mu.Lock()
	got := mu.parts["upload-1/2"]
	mu.mu.Unlock()
	require.Equal(t, payload, got, "backend must receive decoded payload, not aws-chunked framing")

	last := mu.lastRequest()
	require.NotContains(t, last.Header.Get("Content-Encoding"), "aws-chunked")
}
