package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseIPv4(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"93.184.216.34\n", "93.184.216.34", false},
		{"  10.0.0.1  ", "10.0.0.1", false},
		{"2600:1700::1", "", true},
		{"::ffff:93.184.216.34", "93.184.216.34", false}, // 4-mapped unwraps
		{"<html>error</html>", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := parseIPv4(tt.in)
		if tt.wantErr != (err != nil) {
			t.Errorf("parseIPv4(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got.String() != tt.want {
			t.Errorf("parseIPv4(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestSplitURLs(t *testing.T) {
	got := splitURLs(" https://a.example ,https://b.example,, ")
	if len(got) != 2 || got[0] != "https://a.example" || got[1] != "https://b.example" {
		t.Errorf("splitURLs = %v", got)
	}
}

func TestLookupWANFallsBack(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "203.0.113.7")
	}))
	defer good.Close()

	c := &checker{
		wanURLs: []string{bad.URL, good.URL},
		client:  &http.Client{Timeout: 5 * time.Second},
		timeout: 5 * time.Second,
	}
	addr, err := c.lookupWAN(t.Context())
	if err != nil {
		t.Fatalf("lookupWAN: %v", err)
	}
	if addr.String() != "203.0.113.7" {
		t.Errorf("lookupWAN = %s, want 203.0.113.7", addr)
	}
}

func TestMetricsBeforeFirstVerdict(t *testing.T) {
	c := &checker{record: "home.stewart.net"}
	rec := httptest.NewRecorder()
	c.metrics(rec, nil)
	body := rec.Body.String()
	if strings.Contains(body, "homenet_ddns_record_matches_wan{") {
		t.Errorf("verdict metric emitted before any successful check:\n%s", body)
	}
	if !strings.Contains(body, "homenet_ddns_last_success_timestamp_seconds 0") {
		t.Errorf("expected zero last-success timestamp:\n%s", body)
	}
}
