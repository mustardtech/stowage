// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/stretchr/testify/require"
)

const streamingUnsignedTrailer = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"

// buildUnsignedTrailerBody produces the aws-chunked wire body that
// aws-sdk-go-v2 (flexible checksums, e.g. aws CLI >= 2.23, Loki >= 3.6)
// sends for STREAMING-UNSIGNED-PAYLOAD-TRAILER: bare hex-size chunk
// framing (no chunk-signature extension), a zero-size terminator, then a
// trailing x-amz-checksum-crc32 line.
func buildUnsignedTrailerBody(chunks [][]byte, crc uint32) []byte {
	var out bytes.Buffer
	for _, data := range chunks {
		fmt.Fprintf(&out, "%x\r\n", len(data))
		out.Write(data)
		out.WriteString("\r\n")
	}
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc)
	out.WriteString("0\r\n")
	fmt.Fprintf(&out, "x-amz-checksum-crc32:%s\r\n", base64.StdEncoding.EncodeToString(sum[:]))
	out.WriteString("\r\n")
	return out.Bytes()
}

// TestProxy_ChunkedUploadUnsignedTrailer covers the aws-chunked variant
// modern SDK clients default to. The proxy must decode the framing and
// store only the payload; today it forwards the wire bytes verbatim, so
// the stored object starts with a hex chunk-size line and ends with the
// trailing checksum line, and is only detected as corrupt on readback.
func TestProxy_ChunkedUploadUnsignedTrailer(t *testing.T) {
	ups := httptest.NewServer(newUpstream())
	defer ups.Close()

	vc := &VirtualCredential{
		AccessKeyID:     "AKIATRAILERCANARY000",
		SecretAccessKey: "trailersecrettrailersecrettrailersecret00",
		BackendName:     "primary",
		BucketScopes: []BucketScope{
			{BucketName: "trailer-bucket", BackendName: "primary"},
		},
	}
	proxy := newTestServer(t, ups, vc)
	defer proxy.Close()

	proxyURL, _ := url.Parse(proxy.URL)

	decoded := []byte("unsigned trailer streaming upload payload contents")
	chunks := [][]byte{decoded[:17], decoded[17:38], decoded[38:]}
	body := buildUnsignedTrailerBody(chunks, crc32.ChecksumIEEE(decoded))

	putURL := fmt.Sprintf("%s/%s/loki-chunk.gz", proxy.URL, vc.BucketScopes[0].BucketName)
	req, _ := http.NewRequest(http.MethodPut, putURL, bytes.NewReader(body))
	req.Host = proxyURL.Host
	req.Header.Set("X-Amz-Content-Sha256", streamingUnsignedTrailer)
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.Itoa(len(decoded)))
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc32")
	req.Header.Set("X-Amz-Sdk-Checksum-Algorithm", "CRC32")
	req.ContentLength = int64(len(body))

	signer := v4.NewSigner()
	require.NoError(t, signer.SignHTTP(context.Background(),
		aws.Credentials{AccessKeyID: vc.AccessKeyID, SecretAccessKey: vc.SecretAccessKey},
		req, streamingUnsignedTrailer, "s3", "us-east-1", time.Now().UTC(),
	))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "body: %s", readAll(resp.Body))

	// Read back through the proxy: the object must be the decoded payload,
	// not the aws-chunked framing.
	getReq, _ := http.NewRequest(http.MethodGet, putURL, nil)
	getReq.Host = proxyURL.Host
	signVirtual(t, getReq, vc.AccessKeyID, vc.SecretAccessKey, nil)
	resp2, err := http.DefaultClient.Do(getReq)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	got, _ := io.ReadAll(resp2.Body)
	require.Equal(t, string(decoded), string(got),
		"stored object must contain the decoded payload, not aws-chunked wire framing")
}
