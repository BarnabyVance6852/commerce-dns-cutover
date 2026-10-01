package infrai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/commerce-dns-cutover/cutover"
)

func TestDNSRequestBoundary(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/dns/domain/get":
			if r.Method != http.MethodGet || r.URL.Query().Get("domain") != "shop.example.com" {
				t.Fatalf("domain request = %s %s", r.Method, r.URL.String())
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]string{"zone_id": "zone-7"}})
		case "/v1/dns/record/list":
			if r.Method != http.MethodGet || r.URL.Query().Get("zone_id") != "zone-7" {
				t.Fatalf("list request = %s %s", r.Method, r.URL.String())
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"records": []any{}}})
		case "/v1/dns/record/upsert":
			if r.Method != http.MethodPut || r.Header.Get("Idempotency-Key") == "" {
				t.Fatalf("upsert request method/key = %s/%q", r.Method, r.Header.Get("Idempotency-Key"))
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["zone_id"] != "zone-7" || body["record_type"] != "CNAME" {
				t.Fatalf("upsert body = %#v", body)
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL
	client.http = server.Client()
	zoneID, err := client.ZoneID(context.Background(), "shop.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Records(context.Background(), zoneID); err != nil {
		t.Fatal(err)
	}
	if err := client.UpsertRecord(context.Background(), zoneID, cutover.Record{RecordType: "CNAME", Name: "checkout", Content: "green.example.net", TTL: 60}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestRetryAfter429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "RATE_LIMITED", "message": "retry later"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]string{"zone_id": "zone-8"}})
	}))
	defer server.Close()

	client := NewClient("test-key")
	client.baseURL = server.URL
	client.http = server.Client()
	client.sleep = func(context.Context, time.Duration) error { return nil }
	zoneID, err := client.ZoneID(context.Background(), "shop.example.com")
	if err != nil || zoneID != "zone-8" || attempts != 2 {
		t.Fatalf("ZoneID() = %q, %v after %d attempts", zoneID, err, attempts)
	}
}
