package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBoundedResourceDownloaderEnforcesLimitDuringCopy(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/resource"
	downloader := &boundedResourceDownloader{fetch: func(context.Context, DownloadResourceRequest) (io.Reader, string, error) {
		return strings.NewReader("too-large"), "text/plain", nil
	}}
	_, err := downloader.DownloadResource(context.Background(), DownloadResourceRequest{
		MessageID: "message-1", FileKey: "file-1", Type: DownloadFile, DestinationPath: path, MaxBytes: 3,
	})
	if err == nil {
		t.Fatal("DownloadResource() accepted a response over the limit")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial resource remains: %v", statErr)
	}
}

func TestBoundedResourceDownloaderWritesPrivateFile(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/resource"
	downloader := &boundedResourceDownloader{fetch: func(context.Context, DownloadResourceRequest) (io.Reader, string, error) {
		return strings.NewReader("ok"), "text/plain", nil
	}}
	result, err := downloader.DownloadResource(context.Background(), DownloadResourceRequest{
		MessageID: "message-1", FileKey: "file-1", Type: DownloadFile, DestinationPath: path, MaxBytes: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.BytesWritten != 2 || result.ContentType != "text/plain" || info.Mode().Perm() != 0o600 {
		t.Fatalf("result = %+v, mode = %o", result, info.Mode().Perm())
	}
}

func TestBoundedResourceDownloaderValidatesBeforeFetch(t *testing.T) {
	t.Parallel()
	fetchCalls := 0
	downloader := &boundedResourceDownloader{fetch: func(context.Context, DownloadResourceRequest) (io.Reader, string, error) {
		fetchCalls++
		return strings.NewReader("ok"), "text/plain", nil
	}}
	_, err := downloader.DownloadResource(context.Background(), DownloadResourceRequest{
		MessageID: "message-1", FileKey: "file-1", Type: DownloadFile, MaxBytes: 3,
	})
	if err == nil || fetchCalls != 0 {
		t.Fatalf("DownloadResource() error=%v fetchCalls=%d", err, fetchCalls)
	}
}

func TestStreamingResourceDownloadStopsAtLimitWithoutPrebuffering(t *testing.T) {
	t.Parallel()
	body := &trackingReadCloser{reader: strings.NewReader(strings.Repeat("x", 1024))}
	var gotRequest *http.Request
	fetcher := &streamingResourceFetcher{
		baseURL: "https://open.feishu.test",
		tokens: tenantTokenSourceFunc(func(context.Context) (string, error) {
			return "tenant-token", nil
		}),
		client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			gotRequest = req
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          body,
				Header:        http.Header{"Content-Type": []string{"application/octet-stream"}},
				ContentLength: -1,
			}, nil
		}),
	}
	path := t.TempDir() + "/resource"
	downloader := &boundedResourceDownloader{fetch: fetcher.fetch}
	_, err := downloader.DownloadResource(context.Background(), DownloadResourceRequest{
		MessageID: "message-1", FileKey: "file-1", Type: DownloadFile, DestinationPath: path, MaxBytes: 3,
	})
	if err == nil {
		t.Fatal("DownloadResource() accepted a streaming response over the limit")
	}
	if body.bytesRead != 4 || !body.closed {
		t.Fatalf("stream read bytes=%d closed=%t, want max+1 bytes and closed", body.bytesRead, body.closed)
	}
	if gotRequest == nil || gotRequest.Header.Get("Authorization") != "Bearer tenant-token" || gotRequest.URL.Query().Get("type") != "file" || gotRequest.URL.Path != "/open-apis/im/v1/messages/message-1/resources/file-1" {
		t.Fatalf("streaming request = %#v", gotRequest)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial resource remains: %v", statErr)
	}
}

func TestStreamingResourceFetcherRejectsDeclaredOversizeAndHTTPFailure(t *testing.T) {
	t.Parallel()
	t.Run("declared size", func(t *testing.T) {
		body := &trackingReadCloser{reader: strings.NewReader("not read")}
		fetcher := testStreamingFetcher(body, http.StatusOK, 4)
		_, _, err := fetcher.fetch(context.Background(), DownloadResourceRequest{
			MessageID: "message-1", FileKey: "file-1", Type: DownloadImage, MaxBytes: 3,
		})
		if err == nil || body.bytesRead != 0 || !body.closed {
			t.Fatalf("fetch() error=%v bytes=%d closed=%t", err, body.bytesRead, body.closed)
		}
	})
	t.Run("http failure", func(t *testing.T) {
		body := &trackingReadCloser{reader: strings.NewReader("raw response body must not be read")}
		fetcher := testStreamingFetcher(body, http.StatusTooManyRequests, -1)
		_, _, err := fetcher.fetch(context.Background(), DownloadResourceRequest{
			MessageID: "message-1", FileKey: "file-1", Type: DownloadImage, MaxBytes: 3,
		})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusTooManyRequests || !IsRetryable(err) || body.bytesRead != 0 || !body.closed || strings.Contains(err.Error(), "raw response body") {
			t.Fatalf("fetch() API error=%#v error=%v bytes=%d closed=%t", apiErr, err, body.bytesRead, body.closed)
		}
	})
}

func TestCachedTenantTokenSourceRefreshesBeforeExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	calls := 0
	source := &cachedTenantTokenSource{
		now: func() time.Time { return now },
		fetch: func(context.Context) (string, time.Duration, error) {
			calls++
			return "token-" + string(rune('0'+calls)), 10 * time.Minute, nil
		},
	}
	first, err := source.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := source.Token(context.Background())
	if err != nil || second != first || calls != 1 {
		t.Fatalf("cached Token() = %q, %v, calls=%d", second, err, calls)
	}
	now = now.Add(9*time.Minute + time.Second)
	third, err := source.Token(context.Background())
	if err != nil || third == first || calls != 2 {
		t.Fatalf("refreshed Token() = %q, %v, calls=%d", third, err, calls)
	}
}

func TestStreamingResourceFetcherRefreshesRejectedTokenOnce(t *testing.T) {
	t.Parallel()
	firstBody := &trackingReadCloser{reader: strings.NewReader("unauthorized response must not be read")}
	fetchCalls := 0
	tokens := &cachedTenantTokenSource{
		now: time.Now,
		fetch: func(context.Context) (string, time.Duration, error) {
			fetchCalls++
			return "token-" + string(rune('0'+fetchCalls)), time.Hour, nil
		},
	}
	requestCalls := 0
	fetcher := &streamingResourceFetcher{
		baseURL: "https://open.feishu.test",
		tokens:  tokens,
		client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			requestCalls++
			if requestCalls == 1 {
				if got := req.Header.Get("Authorization"); got != "Bearer token-1" {
					t.Fatalf("first Authorization = %q", got)
				}
				return &http.Response{StatusCode: http.StatusUnauthorized, Body: firstBody, Header: make(http.Header)}, nil
			}
			if got := req.Header.Get("Authorization"); got != "Bearer token-2" {
				t.Fatalf("second Authorization = %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), ContentLength: 2,
			}, nil
		}),
	}
	reader, _, err := fetcher.fetch(context.Background(), DownloadResourceRequest{
		MessageID: "message-1", FileKey: "file-1", Type: DownloadFile, MaxBytes: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.(io.Closer).Close()
	content, err := io.ReadAll(reader)
	if err != nil || string(content) != "ok" {
		t.Fatalf("resource = %q, %v", content, err)
	}
	if requestCalls != 2 || fetchCalls != 2 || !firstBody.closed || firstBody.bytesRead != 0 {
		t.Fatalf("requests=%d token fetches=%d first body closed=%t bytes=%d", requestCalls, fetchCalls, firstBody.closed, firstBody.bytesRead)
	}
}

func TestStreamingResourceFetcherRefreshesBoundedBusinessTokenError(t *testing.T) {
	t.Parallel()
	firstBody := &trackingReadCloser{reader: strings.NewReader(`{"code":99991663,"msg":"tenant token expired"}`)}
	tokens := &recordingTenantTokenSource{token: "stale-token"}
	requestCalls := 0
	fetcher := &streamingResourceFetcher{
		baseURL: "https://open.feishu.test",
		tokens:  tokens,
		client: httpDoerFunc(func(*http.Request) (*http.Response, error) {
			requestCalls++
			if requestCalls == 1 {
				return &http.Response{StatusCode: http.StatusBadRequest, Body: firstBody, Header: make(http.Header)}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), ContentLength: 2,
			}, nil
		}),
	}
	reader, _, err := fetcher.fetch(context.Background(), DownloadResourceRequest{
		MessageID: "message-1", FileKey: "file-1", Type: DownloadFile, MaxBytes: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.(io.Closer).Close()
	if requestCalls != 2 || tokens.invalidated != "stale-token" || !firstBody.closed || firstBody.bytesRead > maxResourceAPIErrorBytes+1 {
		t.Fatalf("requests=%d invalidated=%q first body closed=%t bytes=%d", requestCalls, tokens.invalidated, firstBody.closed, firstBody.bytesRead)
	}
}

func TestCachedTenantTokenSourceConcurrentFetchesOnce(t *testing.T) {
	t.Parallel()
	fetchCalls := 0
	source := &cachedTenantTokenSource{
		now: time.Now,
		fetch: func(context.Context) (string, time.Duration, error) {
			fetchCalls++
			return "shared-token", time.Hour, nil
		},
	}
	const goroutines = 16
	var wait sync.WaitGroup
	wait.Add(goroutines)
	results := make(chan string, goroutines)
	for range goroutines {
		go func() {
			defer wait.Done()
			token, err := source.Token(context.Background())
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- token
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result != "shared-token" {
			t.Fatalf("Token() = %q", result)
		}
	}
	if fetchCalls != 1 {
		t.Fatalf("token fetches = %d, want one", fetchCalls)
	}
}

type tenantTokenSourceFunc func(context.Context) (string, error)

func (f tenantTokenSourceFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackingReadCloser struct {
	reader    io.Reader
	bytesRead int
	closed    bool
}

func (r *trackingReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.bytesRead += n
	return n, err
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

func testStreamingFetcher(body io.ReadCloser, status int, contentLength int64) *streamingResourceFetcher {
	return &streamingResourceFetcher{
		baseURL: "https://open.feishu.test",
		tokens:  tenantTokenSourceFunc(func(context.Context) (string, error) { return "tenant-token", nil }),
		client: httpDoerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body, Header: make(http.Header), ContentLength: contentLength}, nil
		}),
	}
}
