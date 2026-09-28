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

// The portal supports a 30 MiB upload limit; local validation enforces 5 MiB.
const UploadEndpoint = "/internal_api/upload?max_size=30m"

type Submission struct {
	Description string
	SiteURL     string
	Version     string
	Channel     string
	UserID      string
	UserName    string
	Images      []Image
}

type Result struct {
	Success  bool   `json:"success"`
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
	urls := make([]string, 0, len(submission.Images))
	for index, image := range submission.Images {
		imageURL, err := c.upload(ctx, submission.SiteURL, index, image)
		if err != nil {
			return Result{}, err
		}
		urls = append(urls, imageURL)
	}
	body, err := json.Marshal(map[string]any{
		"problem_module": "feature", "problem_description": submission.Description,
		"screenshot_urls": urls, "user_id": submission.UserID, "user_name": submission.UserName,
		"csgclaw": map[string]string{"site_url": submission.SiteURL, "version": submission.Version, "channel": submission.Channel},
	})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+Endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	var result Result
	err = c.request(req, &result)
	return result, err
}

func (c *Client) upload(ctx context.Context, site string, index int, image Image) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fmt.Sprintf("feedback-%d.%s", index, image.Extension))
	if err != nil {
		return "", err
	}
	if _, err = part.Write(image.Data); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(site, "/")+UploadEndpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	// The portal forwards this credential when its storage proxy is enabled.
	req.AddCookie(&http.Cookie{Name: "user_token", Value: c.Token})
	var result struct {
		URL string `json:"url"`
	}
	if err = c.request(req, &result); err != nil {
		return "", err
	}
	parsed, err := url.Parse(result.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("image upload returned an invalid URL")
	}
	return result.URL, nil
}

func (c *Client) request(req *http.Request, result any) error {
	req.Header.Set("Accept", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &HTTPError{Status: response.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(result)
}
