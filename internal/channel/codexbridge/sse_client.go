package codexbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type BotEvent struct {
	MessageID string   `json:"message_id"`
	RoomID    string   `json:"room_id"`
	ChatType  string   `json:"chat_type"`
	Text      string   `json:"text"`
	Mentions  []string `json:"mentions,omitempty"`
}

type SendMessageRequest struct {
	RoomID    string `json:"room_id"`
	Text      string `json:"text"`
	MessageID string `json:"message_id,omitempty"`
}

type BotClient interface {
	StreamEvents(ctx context.Context, botID, lastEventID string) (<-chan BotEvent, <-chan error)
	SendMessage(ctx context.Context, botID string, req SendMessageRequest) error
}

type HTTPClient struct {
	BaseURL     string
	Token       string
	MentionOnly bool
	HTTPClient  *http.Client
}

func (c *HTTPClient) StreamEvents(ctx context.Context, botID, lastEventID string) (<-chan BotEvent, <-chan error) {
	events := make(chan BotEvent, 16)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/api/bots/"+strings.TrimSpace(botID)+"/events", nil)
		if err != nil {
			errs <- err
			return
		}
		if token := strings.TrimSpace(c.Token); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if last := strings.TrimSpace(lastEventID); last != "" {
			req.Header.Set("Last-Event-ID", last)
		}

		resp, err := c.httpClient().Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			errs <- fmt.Errorf("stream bot events: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			return
		}
		if err := decodeSSE(ctx, resp.Body, events, func(event BotEvent) bool {
			if c == nil || !c.MentionOnly {
				return true
			}
			if strings.TrimSpace(event.ChatType) != "group" {
				return true
			}
			return hasInboundBotAtMention(event.Text, botID)
		}); err != nil && ctx.Err() == nil {
			errs <- err
		}
	}()

	return events, errs
}

func (c *HTTPClient) SendMessage(ctx context.Context, botID string, req SendMessageRequest) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal send message request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/api/bots/"+strings.TrimSpace(botID)+"/messages/send", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(c.Token); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("send bot message: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *HTTPClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 0}
}

func decodeSSE(ctx context.Context, r io.Reader, events chan<- BotEvent, accept func(BotEvent) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventName string
	var dataLines []string
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			if err := emitSSEEvent(eventName, dataLines, events, accept); err != nil {
				return err
			}
			eventName = ""
			dataLines = dataLines[:0]
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimLeft(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return emitSSEEvent(eventName, dataLines, events, accept)
}

func emitSSEEvent(eventName string, dataLines []string, events chan<- BotEvent, accept func(BotEvent) bool) error {
	if eventName != "" && eventName != "message" {
		return nil
	}
	if len(dataLines) == 0 {
		return nil
	}
	var event BotEvent
	if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &event); err != nil {
		return fmt.Errorf("decode bot event: %w", err)
	}
	if accept != nil && !accept(event) {
		return nil
	}
	events <- event
	return nil
}

func hasInboundBotAtMention(content, botID string) bool {
	content = strings.TrimSpace(content)
	botID = strings.TrimSpace(botID)
	if content == "" || botID == "" {
		return false
	}

	const prefix = `<at user_id="`
	searchFrom := 0
	for {
		start := strings.Index(content[searchFrom:], prefix)
		if start < 0 {
			return false
		}
		start += searchFrom + len(prefix)
		end := strings.IndexByte(content[start:], '"')
		if end < 0 {
			return false
		}
		if strings.TrimSpace(content[start:start+end]) == botID {
			return true
		}
		searchFrom = start + end + 1
	}
}

const defaultReconnectDelay = 500 * time.Millisecond
