// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestPrepareOutboundHeaders_Checksums: flexible-checksum headers must reach
// the backend — SDKs send them on multipart by default, and stripping the
// per-part checksum makes CompleteMultipartUpload fail upstream. The SDK
// announcement header is forwarded only alongside an actual checksum value.
func TestPrepareOutboundHeaders_Checksums(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]string
		want    []string
		dropped []string
	}{
		{
			name: "checksum value and announcement pass together",
			in: map[string]string{
				"X-Amz-Checksum-Crc32":         "AAAAAA==",
				"X-Amz-Sdk-Checksum-Algorithm": "CRC32",
			},
			want: []string{"X-Amz-Checksum-Crc32", "X-Amz-Sdk-Checksum-Algorithm"},
		},
		{
			name: "announcement without value is dropped",
			in: map[string]string{
				"X-Amz-Sdk-Checksum-Algorithm": "CRC32",
			},
			dropped: []string{"X-Amz-Sdk-Checksum-Algorithm"},
		},
		{
			name: "multipart create declarations pass, but do not count as values",
			in: map[string]string{
				"X-Amz-Checksum-Algorithm":     "CRC32",
				"X-Amz-Checksum-Type":          "FULL_OBJECT",
				"X-Amz-Sdk-Checksum-Algorithm": "CRC32",
			},
			want:    []string{"X-Amz-Checksum-Algorithm", "X-Amz-Checksum-Type"},
			dropped: []string{"X-Amz-Sdk-Checksum-Algorithm"},
		},
		{
			name: "unknown x-amz header still dropped",
			in: map[string]string{
				"X-Amz-Evil": "1",
			},
			dropped: []string{"X-Amz-Evil"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := http.Header{}
			for k, v := range tc.in {
				in.Set(k, v)
			}
			out := PrepareOutboundHeaders(in)
			for _, k := range tc.want {
				if out.Get(k) == "" {
					t.Errorf("expected %q forwarded, got %v", k, out)
				}
			}
			for _, k := range tc.dropped {
				if out.Get(k) != "" {
					t.Errorf("expected %q dropped, got %v", k, out)
				}
			}
		})
	}
}

func TestHasAwsChunkedEncoding(t *testing.T) {
	h := http.Header{}
	if HasAwsChunkedEncoding(h) {
		t.Error("empty header must not report aws-chunked")
	}
	h.Set("Content-Encoding", "gzip, aws-chunked")
	if !HasAwsChunkedEncoding(h) {
		t.Error("aws-chunked token not detected")
	}
	h.Set("Content-Encoding", "gzip")
	if HasAwsChunkedEncoding(h) {
		t.Error("gzip alone must not report aws-chunked")
	}
}

// TestRewriteCompleteMultipartLocation: the upstream's <Location> (its own
// endpoint + real bucket) must be replaced with the proxy-facing URL; the
// rest of the document and Content-Length must stay consistent.
func TestRewriteCompleteMultipartLocation(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<CompleteMultipartUploadResult><Location>http://backend.internal:9000/real-bucket/a/b.txt</Location>` +
		`<Bucket>my-bucket</Bucket><Key>a/b.txt</Key><ETag>"abc-2"</ETag></CompleteMultipartUploadResult>`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Length": []string{strings.Repeat("9", 3)}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	r := httptest.NewRequest(http.MethodPost, "http://proxy.example/my-bucket/a/b.txt?uploadId=u1", nil)
	r.Host = "proxy.example"
	route := RouteInfo{Bucket: "my-bucket", Key: "a/b.txt", PathStyle: true}

	rewriteCompleteMultipartLocation(resp, r, route, "")
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "<Location>http://proxy.example/my-bucket/a/b.txt</Location>") {
		t.Errorf("Location not rewritten: %s", got)
	}
	if strings.Contains(string(got), "backend.internal") {
		t.Errorf("upstream endpoint leaked: %s", got)
	}
	if !strings.Contains(string(got), `<ETag>"abc-2"</ETag>`) {
		t.Errorf("rest of document mangled: %s", got)
	}
	if want := len(got); resp.ContentLength != int64(want) || resp.Header.Get("Content-Length") != strconv.Itoa(want) {
		t.Errorf("Content-Length mismatch: header=%s cl=%d want=%d",
			resp.Header.Get("Content-Length"), resp.ContentLength, want)
	}
}

// TestRewriteCompleteMultipartLocation_NoLocation: documents without a
// <Location> element (e.g. an error body) pass through byte-identical.
func TestRewriteCompleteMultipartLocation_NoLocation(t *testing.T) {
	body := `<Error><Code>InternalError</Code></Error>`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	r := httptest.NewRequest(http.MethodPost, "http://proxy.example/b/k?uploadId=u1", nil)
	route := RouteInfo{Bucket: "b", Key: "k", PathStyle: true}
	rewriteCompleteMultipartLocation(resp, r, route, "")
	got, _ := io.ReadAll(resp.Body)
	if string(got) != body {
		t.Errorf("body changed: %s", got)
	}
}

// TestStripPresignedQuery asserts that the SigV4 presigned-URL query params
// are removed while unrelated query params survive. If these params leaked
// through to the outbound URL, the backend would treat the request as
// query-string signed against the client's AKID — which it doesn't know —
// and reject with 403.
func TestStripPresignedQuery(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		// keep is the set of param keys we expect to remain after strip.
		// ordering is not asserted because url.Values.Encode sorts by key.
		keep []string
	}{
		{
			name: "empty",
			raw:  "",
			keep: nil,
		},
		{
			name: "all presigned params",
			raw:  "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKID/20260424/us-east-1/s3/aws4_request&X-Amz-Date=20260424T000000Z&X-Amz-Expires=60&X-Amz-SignedHeaders=host&X-Amz-Signature=deadbeef&X-Amz-Security-Token=TOKEN",
			keep: nil,
		},
		{
			name: "presigned mixed with application params",
			raw:  "x-id=GetObject&X-Amz-Signature=deadbeef&X-Amz-Credential=AKID&versionId=v1&response-content-type=text%2Fplain",
			keep: []string{"x-id", "versionId", "response-content-type"},
		},
		{
			name: "no presigned params — pass through verbatim",
			raw:  "x-id=PutObject&partNumber=1&uploadId=abc",
			keep: []string{"x-id", "partNumber", "uploadId"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripPresignedQuery(tc.raw)
			parsed, err := url.ParseQuery(got)
			if err != nil {
				t.Fatalf("result %q not parseable: %v", got, err)
			}
			for _, p := range presignedQueryParams {
				if parsed.Has(p) {
					t.Errorf("presigned param %q still present: %q", p, got)
				}
			}
			for _, k := range tc.keep {
				if !parsed.Has(k) {
					t.Errorf("expected %q preserved, got %q", k, got)
				}
			}
			if len(tc.keep) == 0 && tc.raw == "" && got != "" {
				t.Errorf("empty input must stay empty, got %q", got)
			}
		})
	}
}

// TestStripPresignedQuery_MalformedIsPassthrough: if the input can't be
// parsed as a query string, we return it verbatim rather than lose data.
// The backend will reject it either way, but we must not mangle it here.
func TestStripPresignedQuery_MalformedIsPassthrough(t *testing.T) {
	raw := "broken=%ZZ"
	got := StripPresignedQuery(raw)
	if !strings.Contains(got, "broken=%ZZ") {
		t.Errorf("malformed input should pass through, got %q", got)
	}
}

// TestBuildOutboundRawPath verifies the AWS canonical-URI encoding of the
// outbound path. Only unreserved characters (alnum + `-._~`) survive as-is;
// everything else — including `+`, `=`, space, and multi-byte UTF-8 — must
// be pct-encoded so the wire form and the SigV4 canonical URI agree.
func TestBuildOutboundRawPath(t *testing.T) {
	tests := []struct {
		name   string
		bucket string
		key    string
		want   string
	}{
		{"no key", "mybucket", "", "/mybucket"},
		{"plain key", "mybucket", "hello.txt", "/mybucket/hello.txt"},
		{"plus and equals", "mybucket", "a+b=c.txt", "/mybucket/a%2Bb%3Dc.txt"},
		{"space", "mybucket", "hello world.txt", "/mybucket/hello%20world.txt"},
		{"utf-8", "mybucket", "café.txt", "/mybucket/caf%C3%A9.txt"},
		{"slash inside key is preserved", "mybucket", "prefix/sub.txt", "/mybucket/prefix/sub.txt"},
		{"slash plus reserved char", "mybucket", "a/b+c=d.txt", "/mybucket/a/b%2Bc%3Dd.txt"},
		{"unreserved punct", "mybucket", "a-b.c_d~e.txt", "/mybucket/a-b.c_d~e.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildOutboundRawPath(tc.bucket, tc.key)
			if got != tc.want {
				t.Errorf("BuildOutboundRawPath(%q, %q) = %q, want %q", tc.bucket, tc.key, got, tc.want)
			}
		})
	}
}

func TestAWSPathEscape_FastPath(t *testing.T) {
	in := "abcXYZ123-._~"
	if got := awsPathEscape(in); got != in {
		t.Errorf("fast path expected %q, got %q", in, got)
	}
}
