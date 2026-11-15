// Package platform calls the ad platform API (WireMock in the demo, docs/01 section 6.4).
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Conversion is the request body of POST /v1/conversions.
type Conversion struct {
	OrderID    string    `json:"order_id"`
	ClickID    string    `json:"click_id"`
	CampaignID int64     `json:"campaign_id"`
	Amount     int64     `json:"amount"`
	OrderTime  time.Time `json:"order_time"`
}

// Outcome tells the worker what to do next (docs/02 section 11).
type Outcome int

const (
	Accepted  Outcome = iota + 1 // 2xx: save the conversion, Ack
	Rejected                     // 4xx except 429: permanent, FAILED, Ack
	Retryable                    // 429, 5xx, timeout, network error: retry later
)

type Response struct {
	Outcome      Outcome
	ConversionID string // set when Accepted
	Reason       string // why it was Rejected or is Retryable
}

const (
	requestTimeout = 5 * time.Second // WRK-6
	maxBodyBytes   = 64 << 10        // a misbehaving server cannot fill our memory
	maxReasonBytes = 100             // the reason is stored in import_rows.error VARCHAR(255)
)

type Client struct {
	url  string
	http *http.Client
}

// New returns a client with the WRK-6 timeout. One Client is shared by all
// handler goroutines; http.Client is safe for concurrent use.
func New(baseURL string) *Client {
	return &Client{
		url:  strings.TrimSuffix(baseURL, "/") + "/v1/conversions",
		http: &http.Client{Timeout: requestTimeout},
	}
}

// Send posts one conversion with Idempotency-Key = order_id and classifies the
// answer. Transport problems are Retryable, never an error: the caller only has
// to switch on Outcome.
func (c *Client) Send(ctx context.Context, conv Conversion) Response {
	body, _ := json.Marshal(conv) // plain strings, numbers and a time cannot fail to encode
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Response{Outcome: Retryable, Reason: "build request: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", conv.OrderID)

	resp, err := c.http.Do(req)
	if err != nil {
		return Response{Outcome: Retryable, Reason: "request: " + err.Error()}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Response{Outcome: Retryable, Reason: "read body: " + err.Error()}
	}
	return classify(resp.StatusCode, data)
}

// classify maps a status code and body to an Outcome (WRK-7 to WRK-9).
func classify(status int, body []byte) Response {
	switch {
	case status >= 200 && status < 300:
		var ok struct {
			ConversionID string `json:"conversion_id"`
		}
		if err := json.Unmarshal(body, &ok); err != nil || ok.ConversionID == "" {
			return Response{Outcome: Retryable, Reason: "2xx without conversion_id"}
		}
		return Response{Outcome: Accepted, ConversionID: ok.ConversionID}
	case status == http.StatusTooManyRequests || status >= 500:
		return Response{Outcome: Retryable, Reason: fmt.Sprintf("http %d", status)}
	case status >= 400:
		return Response{Outcome: Rejected, Reason: errorCode(status, body)}
	default:
		return Response{Outcome: Retryable, Reason: fmt.Sprintf("unexpected http %d", status)}
	}
}

// errorCode reads {"error": "..."} from a 4xx body (WRK-8). Without one it
// falls back to http_<status>, so the row still says why it failed.
func errorCode(status int, body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil || e.Error == "" {
		return fmt.Sprintf("http_%d", status)
	}
	if len(e.Error) > maxReasonBytes {
		return strings.ToValidUTF8(e.Error[:maxReasonBytes], "")
	}
	return e.Error
}
