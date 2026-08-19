package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

const maxResourceAPIErrorBytes = 8 << 10

type resourceFetchFunc func(context.Context, DownloadResourceRequest) (io.Reader, string, error)

// boundedResourceDownloader copies a streaming response through a hard byte
// limit into a private file. Production deliberately bypasses the generated
// MessageResource.Get method because SDK v3.9.7 buffers the complete response
// in ApiResp.RawBody before exposing a reader.
type boundedResourceDownloader struct {
	fetch resourceFetchFunc
}

func newBoundedResourceDownloader(tokens tenantTokenSource, client httpDoer) resourceDownloader {
	fetcher := &streamingResourceFetcher{
		baseURL: lark.FeishuBaseUrl,
		client:  client,
		tokens:  tokens,
	}
	return &boundedResourceDownloader{fetch: fetcher.fetch}
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type streamingResourceFetcher struct {
	baseURL string
	client  httpDoer
	tokens  tenantTokenSource
}

func (f *streamingResourceFetcher) fetch(ctx context.Context, req DownloadResourceRequest) (io.Reader, string, error) {
	if ctx == nil {
		return nil, "", errNilContext
	}
	if f == nil || f.client == nil || f.tokens == nil {
		return nil, "", ErrInvalidConfig
	}
	messageID := strings.TrimSpace(req.MessageID)
	fileKey := strings.TrimSpace(req.FileKey)
	if messageID == "" || fileKey == "" {
		return nil, "", fmt.Errorf("feishu resource message id and file key are required")
	}
	if req.MaxBytes <= 0 {
		return nil, "", fmt.Errorf("feishu resource download limit must be positive")
	}
	resourceType := string(req.Type)
	if req.Type != DownloadImage && req.Type != DownloadFile {
		return nil, "", fmt.Errorf("unsupported feishu resource download type %q", req.Type)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(f.baseURL), "/") +
		"/open-apis/im/v1/messages/" + url.PathEscape(messageID) +
		"/resources/" + url.PathEscape(fileKey)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("build Feishu resource URL: %w", err)
	}
	query := parsed.Query()
	query.Set("type", resourceType)
	parsed.RawQuery = query.Encode()
	for attempt := 0; attempt < 2; attempt++ {
		token, tokenErr := f.tokens.Token(ctx)
		if tokenErr != nil {
			return nil, "", tokenErr
		}
		httpReq, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if requestErr != nil {
			return nil, "", fmt.Errorf("build Feishu resource request: %w", requestErr)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("Accept", "application/octet-stream")
		httpReq.Header.Set("User-Agent", larkTransportSource)
		resp, requestErr := f.client.Do(httpReq)
		if requestErr != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			return nil, "", requestAPIError("download message resource", requestErr)
		}
		if resp == nil {
			return nil, "", missingAPIResponse("download message resource")
		}
		if resp.Body == nil {
			return nil, "", missingAPIResult("download message resource", "response body", nil)
		}
		code := 0
		message := http.StatusText(resp.StatusCode)
		if resp.StatusCode == http.StatusBadRequest {
			code, message = boundedResourceAPIError(resp.Body, message)
		}
		if resp.StatusCode == http.StatusUnauthorized || code == tenantAccessTokenInvalidCode {
			if invalidatable, ok := f.tokens.(invalidatableTenantTokenSource); ok {
				invalidatable.Invalidate(token)
				if attempt == 0 {
					_ = resp.Body.Close()
					continue
				}
			}
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, "", &APIError{
				Operation: "download message resource", Code: code, HTTPStatus: resp.StatusCode, Message: message,
			}
		}
		if resp.ContentLength > req.MaxBytes {
			_ = resp.Body.Close()
			return nil, "", fmt.Errorf("Feishu resource exceeds the %d byte download limit", req.MaxBytes)
		}
		return resp.Body, strings.TrimSpace(resp.Header.Get("Content-Type")), nil
	}
	return nil, "", missingAPIResponse("download message resource")
}

func boundedResourceAPIError(body io.Reader, fallback string) (int, string) {
	if body == nil {
		return 0, fallback
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxResourceAPIErrorBytes+1))
	if err != nil || len(raw) > maxResourceAPIErrorBytes {
		return 0, fallback
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, fallback
	}
	message := sanitizeAPIMessage(result.Msg)
	if message == "" {
		message = fallback
	}
	return result.Code, message
}

func (d *boundedResourceDownloader) DownloadResource(ctx context.Context, req DownloadResourceRequest) (DownloadResourceResult, error) {
	if ctx == nil {
		return DownloadResourceResult{}, errNilContext
	}
	if d == nil || d.fetch == nil {
		return DownloadResourceResult{}, fmt.Errorf("feishu bounded resource downloader is unavailable")
	}
	req.MessageID = strings.TrimSpace(req.MessageID)
	req.FileKey = strings.TrimSpace(req.FileKey)
	req.DestinationPath = strings.TrimSpace(req.DestinationPath)
	if req.MessageID == "" || req.FileKey == "" {
		return DownloadResourceResult{}, fmt.Errorf("feishu resource message id and file key are required")
	}
	if req.DestinationPath == "" {
		return DownloadResourceResult{}, fmt.Errorf("feishu resource destination path is required")
	}
	if req.MaxBytes <= 0 {
		return DownloadResourceResult{}, fmt.Errorf("feishu resource download limit must be positive")
	}
	if req.Type != DownloadImage && req.Type != DownloadFile {
		return DownloadResourceResult{}, fmt.Errorf("unsupported feishu resource download type %q", req.Type)
	}
	reader, contentType, err := d.fetch(ctx, req)
	if err != nil {
		return DownloadResourceResult{}, err
	}
	if reader == nil {
		return DownloadResourceResult{}, fmt.Errorf("feishu resource response is empty")
	}
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}
	file, err := os.OpenFile(req.DestinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return DownloadResourceResult{}, err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(req.DestinationPath)
		}
	}()

	written, err := io.Copy(file, io.LimitReader(reader, req.MaxBytes+1))
	if err != nil {
		return DownloadResourceResult{}, err
	}
	if written > req.MaxBytes {
		return DownloadResourceResult{}, fmt.Errorf("Feishu resource exceeds the %d byte download limit", req.MaxBytes)
	}
	if err := file.Sync(); err != nil {
		return DownloadResourceResult{}, err
	}
	if err := file.Close(); err != nil {
		return DownloadResourceResult{}, err
	}
	keep = true
	return DownloadResourceResult{ContentType: strings.TrimSpace(contentType), BytesWritten: written}, nil
}

var _ resourceDownloader = (*boundedResourceDownloader)(nil)
