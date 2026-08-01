// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-logr/logr/testr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// staleConnUpstream is a raw TCP HTTP/1.1 server that answers exactly one
// request per connection and then kills the connection as soon as the next
// request starts arriving on it — the behaviour of a backend (or its LB)
// dropping keep-alives during a 5xx/restart episode. A client that retries
// on a fresh connection always succeeds; a client that fails the request
// outright when the pooled connection turns out to be dead does not.
func staleConnUpstream(t *testing.T) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, req.Body)
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
				// Hold the connection open (so it sits in the proxy's idle
				// pool), then slam it shut the moment a second request
				// starts arriving.
				buf := make([]byte, 1)
				_, _ = br.Read(buf)
			}(conn)
		}
	}()

	u, err := url.Parse("http://" + ln.Addr().String())
	require.NoError(t, err)
	return u
}

// TestProxy_PutSurvivesStaleUpstreamConnection reproduces the burst-502
// symptom: after a healthy request parks a keep-alive connection in the
// proxy's pool, the backend drops it. The next PutObject drawn onto that
// dead connection must still succeed (the proxy has to recover — retry on
// a fresh connection or not reuse the corpse), not surface an instant
// 502 upstream-error with latency_ms ~0 as it does today.
func TestProxy_PutSurvivesStaleUpstreamConnection(t *testing.T) {
	upsURL := staleConnUpstream(t)
	// newTestServer only needs the upstream's URL string.
	fakeUps := &httptest.Server{URL: upsURL.String()}

	vc := &VirtualCredential{
		AccessKeyID:     "AKIASTALECONN0000000",
		SecretAccessKey: "staleconnsecretstaleconnsecretstalecon00",
		BackendName:     "primary",
		BucketScopes: []BucketScope{
			{BucketName: "stale-bucket", BackendName: "primary"},
		},
	}
	proxy := newTestServer(t, fakeUps, vc)
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)

	// Request 1: a healthy GET. Its upstream connection goes idle in the
	// proxy's transport pool afterwards.
	getReq, _ := http.NewRequest(http.MethodGet, proxy.URL+"/stale-bucket/warm.txt", nil)
	getReq.Host = proxyURL.Host
	signVirtual(t, getReq, vc.AccessKeyID, vc.SecretAccessKey, nil)
	resp, err := http.DefaultClient.Do(getReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Request 2: a PUT that the proxy will send down the now-doomed pooled
	// connection. The upstream kills it mid-request; the proxy must not
	// turn that into a client-visible 502.
	body := []byte("object payload after backend dropped keepalives")
	putReq, _ := http.NewRequest(http.MethodPut, proxy.URL+"/stale-bucket/after.txt", bytes.NewReader(body))
	putReq.Host = proxyURL.Host
	signVirtual(t, putReq, vc.AccessKeyID, vc.SecretAccessKey, body)
	resp2, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equalf(t, http.StatusOK, resp2.StatusCode,
		"PUT after upstream dropped its keep-alive must succeed, got %d: %s",
		resp2.StatusCode, readAll(resp2.Body))
}

// TestProxy_ClientCancelIsNotUpstreamError covers the lone-GetObject flavour
// of the 0-1ms 502s: when the *client* has already gone away (context
// cancelled — e.g. an SDK abandoning a read to retry), the proxy currently
// reports 502/upstream-error, polluting logs, metrics, and the audit trail
// with phantom backend failures. A client cancellation is not a backend
// error and must not be classified as 502 Bad Gateway.
func TestProxy_ClientCancelIsNotUpstreamError(t *testing.T) {
	ups := httptest.NewServer(newUpstream())
	defer ups.Close()

	vc := &VirtualCredential{
		AccessKeyID:     "AKIACANCELCANARY0000",
		SecretAccessKey: "cancelsecretcancelsecretcancelsecret0000",
		BackendName:     "primary",
		BucketScopes: []BucketScope{
			{BucketName: "cancel-bucket", BackendName: "primary"},
		},
	}
	src := &fakeSource{
		byAKID: map[string]*VirtualCredential{vc.AccessKeyID: vc},
		byAnon: map[string]*AnonymousBinding{},
	}
	srv := NewServer(Config{
		Source:        src,
		Backends:      NewBackendResolver(&stubBackendLookup{endpointURL: ups.URL}),
		Limiter:       NewLimiter(0, 0),
		IPLimiter:     NewIPLimiter(0),
		Metrics:       NewMetrics(prometheus.NewRegistry()),
		Log:           testr.New(t),
		BucketCreated: time.Now(),
		AdminCredsOverride: func(_ context.Context, _ BackendSpec) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "admin", SecretAccessKey: "secret"}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "http://proxy.test/cancel-bucket/obj.txt", nil)
	signVirtual(t, req, vc.AccessKeyID, vc.SecretAccessKey, nil)

	// The client disconnects before (or while) the proxy talks upstream.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	require.NotEqual(t, http.StatusBadGateway, rec.Code,
		"a client-cancelled request must not be recorded as a 502 upstream-error")
}
