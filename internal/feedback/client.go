package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Endpoint = "/api/v1/csgbot/user-feedback"

type Submission struct {
	ID          string
	Description string
	SiteURL     string
	Version     string
	Channel     string
	Images      []Image
}

type Result struct {
	Success  bool   `json:"success"`
	State    string `json:"state"`
	IssueID  int    `json:"issue_id,omitempty"`
	IssueURL string `json:"issue_url,omitempty"`
	Message  string `json:"message,omitempty"`
}

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("feedback service returned HTTP %d", e.Status) }

func (c *Client) Submit(ctx context.Context, submission Submission) (Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{"submission_id": submission.ID, "problem_description": submission.Description,
		"site_url": submission.SiteURL, "version": submission.Version, "channel": submission.Channel}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return Result{}, err
		}
	}
	for index, image := range submission.Images {
		part, err := writer.CreateFormFile("images", fmt.Sprintf("feedback-%d.%s", index, image.Extension))
		if err != nil {
			return Result{}, err
		}
		if _, err = part.Write(image.Data); err != nil {
			return Result{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return Result{}, err
	}
	return c.request(ctx, http.MethodPost, Endpoint, &body, writer.FormDataContentType())
}

func (c *Client) Status(ctx context.Context, id string) (Result, error) {
	return c.request(ctx, http.MethodGet, Endpoint+"/"+url.PathEscape(id), nil, "")
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, &HTTPError{Status: response.StatusCode}
	}
	var result Result
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return Result{}, err
	}
	return result, nil
}
