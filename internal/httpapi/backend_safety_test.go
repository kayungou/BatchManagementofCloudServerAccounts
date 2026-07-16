package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ikun/cloud-account-manager/internal/model"
	"github.com/ikun/cloud-account-manager/internal/security"
)

func TestEnqueueDropletDeleteRejectsEmptySelectionBeforeStoreAccess(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/droplets/delete", strings.NewReader(`{"account_id":"00000000-0000-0000-0000-000000000000","droplet_ids":[],"confirm_names":{}}`))
	request = request.WithContext(context.WithValue(request.Context(), principalKey, principal{
		Session: model.Session{RecentAuthAt: time.Now()},
	}))
	recorder := httptest.NewRecorder()

	server.enqueueDropletDelete(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "invalid_selection" {
		t.Fatalf("error code = %q", response.Error.Code)
	}
}

func TestUsageSummaryAllScopeRequiresAdmin(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage-summary?scope=all", nil)
	request = request.WithContext(context.WithValue(request.Context(), principalKey, principal{
		User: model.User{Role: "user"},
	}))
	recorder := httptest.NewRecorder()

	server.usageSummary(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestUsageSummaryRejectsUnknownScopeBeforeStoreAccess(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage-summary?scope=team", nil)
	request = request.WithContext(context.WithValue(request.Context(), principalKey, principal{
		User: model.User{Role: "admin"},
	}))
	recorder := httptest.NewRecorder()

	server.usageSummary(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestAdminMutationsRequireRecentAuthentication(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		handler http.HandlerFunc
	}{
		{name: "create user", method: http.MethodPost, path: "/api/v1/admin/users", handler: (&Server{}).adminCreateUser},
		{name: "update user", method: http.MethodPatch, path: "/api/v1/admin/users/00000000-0000-0000-0000-000000000000", handler: (&Server{}).adminUpdateUser},
		{name: "update settings", method: http.MethodPut, path: "/api/v1/admin/settings", handler: (&Server{}).adminUpdateSettings},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request = request.WithContext(context.WithValue(request.Context(), principalKey, principal{
				User:    model.User{Role: "admin"},
				Session: model.Session{RecentAuthAt: time.Now().Add(-10 * time.Minute)},
			}))
			recorder := httptest.NewRecorder()

			test.handler(recorder, request)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
			}
		})
	}
}

func TestListJobsRejectsUnknownStateBeforeStoreAccess(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?state=cancelled", nil)
	request = request.WithContext(context.WithValue(request.Context(), principalKey, principal{User: model.User{Role: "user"}}))
	recorder := httptest.NewRecorder()

	server.listJobs(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestEnqueueDropletActionRejectsUnsupportedActionBeforeStoreAccess(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/droplets/actions", strings.NewReader(`{"account_id":"00000000-0000-0000-0000-000000000000","droplet_ids":[1],"action":"destroy","parameters":{}}`))
	request = request.WithContext(context.WithValue(request.Context(), principalKey, principal{User: model.User{Role: "user"}}))
	recorder := httptest.NewRecorder()

	server.enqueueDropletAction(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestValidJobState(t *testing.T) {
	for _, state := range []string{"queued", "running", "succeeded", "failed", "partial"} {
		if !validJobState(state) {
			t.Errorf("validJobState rejected %q", state)
		}
	}
	for _, state := range []string{"", "cancelled", "RUNNING"} {
		if validJobState(state) {
			t.Errorf("validJobState accepted %q", state)
		}
	}
}

func TestDigitalOceanClientReturnsDecryptError(t *testing.T) {
	manager, err := security.New(bytes.Repeat([]byte{0x27}, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{security: manager}

	client, err := server.digitalOceanClient(model.CloudAccount{
		TokenCiphertext: []byte("corrupted"),
		TokenNonce:      []byte("short"),
	})
	if err == nil {
		t.Fatal("digitalOceanClient accepted corrupted credentials")
	}
	if client != nil {
		t.Fatal("digitalOceanClient returned a client after decryption failed")
	}
}

func TestSessionTTLFromSetting(t *testing.T) {
	fallback := 72 * time.Hour
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "configured", value: `{"hours":12}`, want: 12 * time.Hour},
		{name: "maximum", value: `{"hours":2160}`, want: 2160 * time.Hour},
		{name: "zero", value: `{"hours":0}`, want: fallback},
		{name: "too large", value: `{"hours":2161}`, want: fallback},
		{name: "invalid JSON", value: `{`, want: fallback},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionTTLFromSetting([]byte(test.value), fallback); got != test.want {
				t.Fatalf("session TTL = %v, want %v", got, test.want)
			}
		})
	}
}

func TestValidateResendVerification(t *testing.T) {
	hash, err := security.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	pending := model.User{PasswordHash: hash, Status: "pending"}
	if err := validateResendVerification(pending, "correct-password"); err != nil {
		t.Fatalf("pending user was rejected: %v", err)
	}
	if err := validateResendVerification(pending, "wrong-password"); !errors.Is(err, errResendInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	active := pending
	active.Status = "active"
	if err := validateResendVerification(active, "correct-password"); !errors.Is(err, errVerificationNotPending) {
		t.Fatalf("active user error = %v", err)
	}
	verifiedAt := time.Now()
	pending.EmailVerifiedAt = &verifiedAt
	if err := validateResendVerification(pending, "correct-password"); !errors.Is(err, errVerificationNotPending) {
		t.Fatalf("verified user error = %v", err)
	}
}

func TestOptionalNullableIntDistinguishesOmittedNullAndNumber(t *testing.T) {
	decode := func(t *testing.T, document string) optionalNullableInt {
		t.Helper()
		var input struct {
			Quota optionalNullableInt `json:"quota"`
		}
		if err := json.Unmarshal([]byte(document), &input); err != nil {
			t.Fatalf("decode %s: %v", document, err)
		}
		return input.Quota
	}

	omitted := decode(t, `{}`)
	if omitted.Present || quotaUpdate(&omitted) != nil {
		t.Fatalf("omitted quota was treated as an update: %+v", omitted)
	}

	nullValue := decode(t, `{"quota":null}`)
	nullUpdate := quotaUpdate(&nullValue)
	if !nullValue.Present || nullUpdate == nil || *nullUpdate != nil {
		t.Fatalf("null quota did not produce an explicit unlimited update: %+v", nullValue)
	}

	number := decode(t, `{"quota":17}`)
	numberUpdate := quotaUpdate(&number)
	if !number.Present || numberUpdate == nil || *numberUpdate == nil || **numberUpdate != 17 {
		t.Fatalf("numeric quota was not preserved: %+v", number)
	}
}

func TestOptionalNullableIntRejectsNonInteger(t *testing.T) {
	for _, document := range []string{`{"quota":"17"}`, `{"quota":1.5}`, `{"quota":true}`} {
		var input struct {
			Quota optionalNullableInt `json:"quota"`
		}
		if err := json.Unmarshal([]byte(document), &input); err == nil {
			t.Fatalf("accepted non-integer quota %s", document)
		}
	}
}

func TestSMTPSecretUpdateSupportsPreserveClearAndReplace(t *testing.T) {
	manager, err := security.New(bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{security: manager}

	ciphertext, nonce, update, err := server.smtpSecretUpdate("", false)
	if err != nil || update || ciphertext != nil || nonce != nil {
		t.Fatalf("preserve result = (%x, %x, %v, %v)", ciphertext, nonce, update, err)
	}

	ciphertext, nonce, update, err = server.smtpSecretUpdate("", true)
	if err != nil || !update || ciphertext != nil || nonce != nil {
		t.Fatalf("clear result = (%x, %x, %v, %v)", ciphertext, nonce, update, err)
	}

	ciphertext, nonce, update, err = server.smtpSecretUpdate("smtp-secret", false)
	if err != nil || !update || len(ciphertext) == 0 || len(nonce) == 0 {
		t.Fatalf("replace result = (%x, %x, %v, %v)", ciphertext, nonce, update, err)
	}
	plaintext, err := manager.Decrypt(ciphertext, nonce, "smtp_password")
	if err != nil || string(plaintext) != "smtp-secret" {
		t.Fatalf("decrypt replacement = %q, %v", plaintext, err)
	}
}
