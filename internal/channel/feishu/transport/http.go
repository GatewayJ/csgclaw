package transport

import (
	"net/http"
	"time"
)

const defaultOpenAPIRequestTimeout = 2 * time.Minute

// singleAttemptHTTPClient prevents the generated SDK from recognizing dial
// errors as its private retry signal. One generated OpenAPI call therefore
// maps to one network attempt; retry ownership stays with the in-memory
// dispatcher.
type singleAttemptHTTPClient struct {
	client *http.Client
}

func newSingleAttemptHTTPClient() *singleAttemptHTTPClient {
	return &singleAttemptHTTPClient{client: &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   defaultOpenAPIRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (c *singleAttemptHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.client == nil {
		return nil, ErrInvalidConfig
	}
	resp, err := c.client.Do(req)
	if err == nil {
		return resp, nil
	}
	return resp, &singleAttemptRequestError{cause: err}
}

// singleAttemptRequestError preserves errors.Is/As classification without
// exposing the concrete *url.Error that makes SDK v3.9.7 retry a dial once.
type singleAttemptRequestError struct {
	cause error
}

func (e *singleAttemptRequestError) Error() string {
	return "feishu HTTP request failed"
}

func (e *singleAttemptRequestError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

var _ httpDoer = (*singleAttemptHTTPClient)(nil)
