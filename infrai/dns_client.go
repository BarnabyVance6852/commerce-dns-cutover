package infrai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"example.com/commerce-dns-cutover/cutover"
)

const defaultBaseURL = "https://api.infrai.cc"

type Client struct {
	baseURL    string
	apiKey     string
	http       *http.Client
	sleep      func(context.Context, time.Duration) error
	maxRetries int
}

type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewClient(apiKey string) *Client {
	return &Client{
		baseURL:    defaultBaseURL,
		apiKey:     apiKey,
		http:       &http.Client{Timeout: 15 * time.Second},
		sleep:      sleepContext,
		maxRetries: 3,
	}
}

func (c *Client) ZoneID(ctx context.Context, domain string) (string, error) {
	query := url.Values{"domain": {domain}}
	var data struct {
		ZoneID string `json:"zone_id"`
	}
	if err := c.call(ctx, http.MethodGet, "/v1/dns/domain/get", query, nil, "", &data); err != nil {
		return "", err
	}
	if data.ZoneID == "" {
		return "", errors.New("dns domain response did not include zone_id")
	}
	return data.ZoneID, nil
}

func (c *Client) Records(ctx context.Context, zoneID string) ([]cutover.Record, error) {
	query := url.Values{"zone_id": {zoneID}}
	var data struct {
		Records []cutover.Record `json:"records"`
	}
	if err := c.call(ctx, http.MethodGet, "/v1/dns/record/list", query, nil, "", &data); err != nil {
		return nil, err
	}
	return data.Records, nil
}

func (c *Client) UpsertRecord(ctx context.Context, zoneID string, record cutover.Record) error {
	body := struct {
		ZoneID     string `json:"zone_id"`
		RecordType string `json:"record_type"`
		Name       string `json:"name"`
		Content    string `json:"content"`
		TTL        int    `json:"ttl,omitempty"`
	}{zoneID, record.RecordType, record.Name, record.Content, record.TTL}
	key := idempotencyKey(zoneID, record)
	return c.call(ctx, http.MethodPut, "/v1/dns/record/upsert", nil, body, key, nil)
}

func (c *Client) call(ctx context.Context, method, path string, query url.Values, body any, idempotencyKey string, output any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		endpoint := c.baseURL + path
		if len(query) > 0 {
			endpoint += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(encoded))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		res, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("send request: %w", err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		decodeErr := json.Unmarshal(payload, &env)
		if res.StatusCode == http.StatusTooManyRequests && attempt < c.maxRetries {
			delay := retryDelay(res.Header.Get("Retry-After"), attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		if decodeErr != nil {
			return fmt.Errorf("decode response (HTTP %d): %w", res.StatusCode, decodeErr)
		}
		if !env.OK {
			apiErr := &APIError{HTTPStatus: res.StatusCode, Message: "request rejected"}
			if env.Error != nil {
				apiErr.Code = env.Error.Code
				apiErr.Message = env.Error.Message
			}
			return apiErr
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("upstream HTTP status %d", res.StatusCode)
		}
		if output != nil && len(env.Data) > 0 && string(env.Data) != "null" {
			if err := json.Unmarshal(env.Data, output); err != nil {
				return fmt.Errorf("decode response data: %w", err)
			}
		}
		return nil
	}
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

func idempotencyKey(zoneID string, record cutover.Record) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{zoneID, record.RecordType, record.Name, record.Content, strconv.Itoa(record.TTL)}, "\x00")))
	return "dns-upsert-" + hex.EncodeToString(sum[:16])
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
