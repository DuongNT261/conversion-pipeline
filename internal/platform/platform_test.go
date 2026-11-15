package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   Response
	}{
		{"200 with id", 200, `{"conversion_id":"cv_A1"}`, Response{Outcome: Accepted, ConversionID: "cv_A1"}},
		{"201 with id", 201, `{"conversion_id":"cv_A1"}`, Response{Outcome: Accepted, ConversionID: "cv_A1"}},
		{"200 without id", 200, `{}`, Response{Outcome: Retryable, Reason: "2xx without conversion_id"}},
		{"200 not json", 200, `ok`, Response{Outcome: Retryable, Reason: "2xx without conversion_id"}},
		{"400 click expired", 400, `{"error":"click_expired"}`, Response{Outcome: Rejected, Reason: "click_expired"}},
		{"400 missing key", 400, `{"error":"missing_idempotency_key"}`, Response{Outcome: Rejected, Reason: "missing_idempotency_key"}},
		{"400 without body", 400, ``, Response{Outcome: Rejected, Reason: "http_400"}},
		{"404 unknown route", 404, `No response could be served`, Response{Outcome: Rejected, Reason: "http_404"}},
		{"400 long reason is cut", 400, `{"error":"` + strings.Repeat("x", 300) + `"}`, Response{Outcome: Rejected, Reason: strings.Repeat("x", 100)}},
		{"429", 429, ``, Response{Outcome: Retryable, Reason: "http 429"}},
		{"500", 500, `{"error":"internal"}`, Response{Outcome: Retryable, Reason: "http 500"}},
		{"503", 503, ``, Response{Outcome: Retryable, Reason: "http 503"}},
		{"302", 302, ``, Response{Outcome: Retryable, Reason: "unexpected http 302"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.status, []byte(tt.body))
			if got != tt.want {
				t.Fatalf("want %+v, got %+v", tt.want, got)
			}
		})
	}
}

func TestSendRequest(t *testing.T) {
	var gotKey, gotType string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/conversions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		gotKey = r.Header.Get("Idempotency-Key")
		gotType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"conversion_id":"cv_A1"}`))
	}))
	defer srv.Close()

	conv := Conversion{
		OrderID: "A1", ClickID: "clk_1", CampaignID: 17, Amount: 350000,
		OrderTime: time.Date(2026, 11, 13, 12, 20, 0, 0, time.UTC),
	}
	got := New(srv.URL+"/").Send(context.Background(), conv)

	if got != (Response{Outcome: Accepted, ConversionID: "cv_A1"}) {
		t.Fatalf("want accepted, got %+v", got)
	}
	if gotKey != "A1" || gotType != "application/json" {
		t.Fatalf("headers: Idempotency-Key=%q Content-Type=%q", gotKey, gotType)
	}
	if gotBody["order_time"] != "2026-11-13T12:20:00Z" || gotBody["campaign_id"] != float64(17) {
		t.Fatalf("body: %v", gotBody)
	}
}

func TestSendNetworkErrorIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nothing listens any more

	got := New(srv.URL).Send(context.Background(), Conversion{OrderID: "A1"})
	if got.Outcome != Retryable || !strings.HasPrefix(got.Reason, "request: ") {
		t.Fatalf("want retryable request error, got %+v", got)
	}
}
