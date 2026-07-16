package digitalocean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAccountSendsAuthenticationAndParsesRateLimit(t *testing.T) {
	resetUnix := time.Now().Add(time.Hour).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/account" {
			t.Errorf("request = %s %s, want GET /v2/account", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("RateLimit-Limit", "5000")
		w.Header().Set("RateLimit-Remaining", "4998")
		w.Header().Set("RateLimit-Reset", fmt.Sprint(resetUnix))
		_, _ = w.Write([]byte(`{"account":{"droplet_limit":25,"floating_ip_limit":3,"email":"owner@example.com","uuid":"team-uuid","email_verified":true,"status":"active","status_message":"","team":{"name":"Example","uuid":"team-uuid"}}}`))
	}))
	defer server.Close()

	account, rate, err := NewWithBaseURL("  test-token  ", server.URL+"/v2").Account(context.Background())
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if account.UUID != "team-uuid" || account.Email != "owner@example.com" || account.DropletLimit != 25 {
		t.Fatalf("unexpected account: %+v", account)
	}
	if account.Team == nil || account.Team.UUID != "team-uuid" {
		t.Fatalf("unexpected team: %+v", account.Team)
	}
	if rate.Limit == nil || *rate.Limit != 5000 || rate.Remaining == nil || *rate.Remaining != 4998 {
		t.Fatalf("unexpected rate info: %+v", rate)
	}
	if rate.ResetAt == nil || rate.ResetAt.Unix() != resetUnix {
		t.Fatalf("unexpected reset time: %+v", rate.ResetAt)
	}
}

func TestListDropletsFollowsPaginationAndPreservesRawJSON(t *testing.T) {
	var requests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v2/droplets" {
			t.Errorf("path = %q, want /v2/droplets", r.URL.Path)
		}
		if got := r.URL.Query().Get("per_page"); got != "200" {
			t.Errorf("per_page = %q, want 200", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("RateLimit-Remaining", "99")
			_, _ = fmt.Fprintf(w, `{"droplets":[{"id":101,"name":"one","status":"active","custom_field":"kept"}],"links":{"pages":{"next":%q}}}`, server.URL+"/v2/droplets?per_page=200&page=2")
		case "2":
			w.Header().Set("RateLimit-Remaining", "98")
			_, _ = w.Write([]byte(`{"droplets":[{"id":202,"name":"two","status":"off"}],"links":{"pages":{}}}`))
		default:
			t.Errorf("unexpected page query: %q", r.URL.RawQuery)
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	droplets, rate, err := NewWithBaseURL("token", server.URL+"/v2").ListDroplets(context.Background())
	if err != nil {
		t.Fatalf("ListDroplets: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("request count = %d, want 2", requests.Load())
	}
	if len(droplets) != 2 || droplets[0].ID != 101 || droplets[1].ID != 202 {
		t.Fatalf("unexpected droplets: %+v", droplets)
	}
	if !strings.Contains(string(droplets[0].Raw), `"custom_field":"kept"`) {
		t.Fatalf("raw JSON was not preserved: %s", droplets[0].Raw)
	}
	if rate.Remaining == nil || *rate.Remaining != 98 {
		t.Fatalf("last-page rate info = %+v, want remaining 98", rate)
	}
}

func TestAPIErrorIncludesProviderDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "request-123")
		w.Header().Set("RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"id":"too_many_requests","message":"API rate limit exceeded"}`))
	}))
	defer server.Close()

	_, rate, err := NewWithBaseURL("token", server.URL+"/v2").Account(context.Background())
	if err == nil {
		t.Fatal("Account returned nil error for a 429 response")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.ID != "too_many_requests" || apiErr.Message != "API rate limit exceeded" || apiErr.RequestID != "request-123" {
		t.Fatalf("unexpected API error: %+v", apiErr)
	}
	if !IsStatus(err, http.StatusTooManyRequests) || IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("IsStatus did not classify error correctly: %v", err)
	}
	if rate.Remaining == nil || *rate.Remaining != 0 {
		t.Fatalf("rate info = %+v, want remaining 0", rate)
	}
}

func TestAPIErrorFallsBackToResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream unavailable\n"))
	}))
	defer server.Close()

	_, _, err := NewWithBaseURL("token", server.URL+"/v2").Account(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Message != "upstream unavailable" || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected API error: %+v", apiErr)
	}
}

func TestCreateDropletsSingleAndBatchPayloads(t *testing.T) {
	tests := []struct {
		name        string
		input       CreateDropletRequest
		response    string
		wantName    string
		wantNames   []string
		wantCount   int
		wantActions int
	}{
		{
			name: "single",
			input: CreateDropletRequest{
				Names: []string{"web-01"}, Region: "sgp1", Size: "s-1vcpu-1gb", Image: "ubuntu-24-04-x64",
				SSHKeys: []any{123, "fingerprint"}, Backups: true, IPv6: true, Monitoring: true,
				Tags: []string{"managed"}, UserData: "#cloud-config\n", VPCUUID: "vpc-uuid",
				BackupPolicy:     map[string]any{"plan": "weekly"},
				PublicNetworking: pointerBool(true), WithDropletAgent: pointerBool(false),
			},
			response:    `{"droplet":{"id":10,"name":"web-01"},"links":{"actions":[{"id":99,"status":"in-progress","type":"create"}]}}`,
			wantName:    "web-01",
			wantCount:   1,
			wantActions: 1,
		},
		{
			name:      "batch",
			input:     CreateDropletRequest{Names: []string{"worker-01", "worker-02"}, Region: "nyc3", Size: "s-2vcpu-2gb", Image: 12345},
			response:  `{"droplets":[{"id":20,"name":"worker-01"},{"id":21,"name":"worker-02"}],"links":{"actions":[]}}`,
			wantNames: []string{"worker-01", "worker-02"},
			wantCount: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v2/droplets" {
					t.Errorf("request = %s %s, want POST /v2/droplets", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if test.wantName != "" {
					if payload["name"] != test.wantName {
						t.Errorf("name = %#v, want %q", payload["name"], test.wantName)
					}
					if _, exists := payload["names"]; exists {
						t.Errorf("single request unexpectedly contains names: %#v", payload["names"])
					}
					if payload["public_networking"] != true || payload["with_droplet_agent"] != false {
						t.Errorf("optional booleans missing or incorrect: %#v", payload)
					}
					if policy, ok := payload["backup_policy"].(map[string]any); !ok || policy["plan"] != "weekly" {
						t.Errorf("backup_policy = %#v", payload["backup_policy"])
					}
				} else {
					gotNames, _ := payload["names"].([]any)
					if fmt.Sprint(gotNames) != fmt.Sprint(test.wantNames) {
						t.Errorf("names = %#v, want %#v", gotNames, test.wantNames)
					}
					if _, exists := payload["name"]; exists {
						t.Errorf("batch request unexpectedly contains name: %#v", payload["name"])
					}
					if _, exists := payload["public_networking"]; exists {
						t.Error("nil optional boolean should be omitted")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()

			response, _, err := NewWithBaseURL("token", server.URL+"/v2").CreateDroplets(context.Background(), test.input)
			if err != nil {
				t.Fatalf("CreateDroplets: %v", err)
			}
			if len(response.Droplets) != test.wantCount || len(response.Actions) != test.wantActions {
				t.Fatalf("response = %+v, want %d droplets and %d actions", response, test.wantCount, test.wantActions)
			}
		})
	}
}

func TestCreateDropletsValidatesBatchSizeBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	client := NewWithBaseURL("token", server.URL+"/v2")

	for _, names := range [][]string{nil, make([]string, 11)} {
		if _, _, err := client.CreateDroplets(context.Background(), CreateDropletRequest{Names: names}); err == nil {
			t.Fatalf("CreateDroplets accepted %d names", len(names))
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid requests reached the server %d times", requests.Load())
	}
}

func TestBuildRootPasswordCloudInitQuotesPassword(t *testing.T) {
	password := `#starts-like-a-comment!@%+=_-`
	cloudInit := BuildRootPasswordCloudInit(password)

	if !strings.HasPrefix(cloudInit, "#cloud-config\n") {
		t.Fatalf("missing cloud-config header: %q", cloudInit)
	}
	if !strings.Contains(cloudInit, `password: "#starts-like-a-comment!@%+=_-"`) {
		t.Fatalf("password is not represented as a quoted YAML scalar:\n%s", cloudInit)
	}
	for _, directive := range []string{
		"disable_root: false",
		"ssh_pwauth: true",
		"expire: false",
		"PermitRootLogin yes",
		"PasswordAuthentication yes",
		"systemctl restart sshd || systemctl restart ssh || true",
	} {
		if !strings.Contains(cloudInit, directive) {
			t.Errorf("cloud-init is missing %q", directive)
		}
	}
}

func TestRawMethodsEncodeQueryAndPreserveResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/images" || r.URL.Query().Get("type") != "distribution" || r.URL.Query().Get("per_page") != "200" {
			t.Errorf("unexpected request URL: %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"images":[{"id":1}]}`))
	}))
	defer server.Close()

	query := url.Values{"type": {"distribution"}, "per_page": {"200"}}
	raw, _, err := NewWithBaseURL("token", server.URL+"/v2").GetRaw(context.Background(), "/images", query)
	if err != nil {
		t.Fatalf("GetRaw: %v", err)
	}
	if string(raw) != `{"images":[{"id":1}]}` {
		t.Fatalf("raw response = %s", raw)
	}
}

func pointerBool(value bool) *bool {
	return &value
}
