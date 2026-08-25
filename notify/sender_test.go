package notify

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type captured struct {
	method  string
	path    string
	headers http.Header
	body    string
}

// captureServer records the last request it received and replies with status.
func captureServer(t *testing.T, status int) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.method = r.Method
		got.path = r.URL.Path
		got.headers = r.Header.Clone()
		got.body = string(body)
		w.WriteHeader(status)
		io.WriteString(w, "response body")
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func testNotification() Notification {
	return Notification{
		Rule:     "ev_charge_window",
		Title:    "Battery nearly full",
		Message:  "Battery at 96% with 2.5h of sun left.",
		Device:   "SN123",
		At:       time.Date(2026, 8, 25, 14, 30, 0, 0, time.UTC),
		Priority: "high",
		Tags:     []string{"battery", "car"},
		Metrics:  map[string]float64{"battery_soc": 96},
		Fields:   map[string]string{"battery_soc": "96", "device": "SN123"},
	}
}

func TestSender_Ntfy(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	target := &Target{Name: "phone", Type: TargetNtfy, URL: srv.URL + "/growud-test", Click: "http://localhost:8080"}
	if err := NewSender().Send(context.Background(), target, testNotification()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	if got.path != "/growud-test" {
		t.Errorf("path = %s, want /growud-test", got.path)
	}
	if got.body != "Battery at 96% with 2.5h of sun left." {
		t.Errorf("body = %q", got.body)
	}
	if h := got.headers.Get("Title"); h != "Battery nearly full" {
		t.Errorf("Title header = %q", h)
	}
	if h := got.headers.Get("Priority"); h != "high" {
		t.Errorf("Priority header = %q, want high", h)
	}
	if h := got.headers.Get("Tags"); h != "battery,car" {
		t.Errorf("Tags header = %q, want battery,car", h)
	}
	if h := got.headers.Get("Click"); h != "http://localhost:8080" {
		t.Errorf("Click header = %q", h)
	}
}

func TestSender_NtfyTargetDefaults(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	// A notification with no priority or tags of its own falls back to the
	// target's defaults.
	n := testNotification()
	n.Priority = ""
	n.Tags = nil

	target := &Target{
		Name: "phone", Type: TargetNtfy, URL: srv.URL,
		Priority: "low", Tags: []string{"solar"},
	}
	if err := NewSender().Send(context.Background(), target, n); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if h := got.headers.Get("Priority"); h != "low" {
		t.Errorf("Priority header = %q, want the target default low", h)
	}
	if h := got.headers.Get("Tags"); h != "solar" {
		t.Errorf("Tags header = %q, want the target default solar", h)
	}
}

func TestSender_NtfyAuthFromEnv(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	t.Setenv("GROWUD_TEST_NTFY_TOKEN", "tk_secret")

	target := &Target{Name: "phone", Type: TargetNtfy, URL: srv.URL, AuthTokenEnv: "GROWUD_TEST_NTFY_TOKEN"}
	if err := NewSender().Send(context.Background(), target, testNotification()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if h := got.headers.Get("Authorization"); h != "Bearer tk_secret" {
		t.Errorf("Authorization header = %q", h)
	}
}

func TestSender_NtfyUnsetAuthEnvSendsNoHeader(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	target := &Target{Name: "phone", Type: TargetNtfy, URL: srv.URL, AuthTokenEnv: "GROWUD_TEST_ABSENT_TOKEN"}
	if err := NewSender().Send(context.Background(), target, testNotification()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if h := got.headers.Get("Authorization"); h != "" {
		t.Errorf("Authorization header = %q, want none", h)
	}
}

func TestSender_NtfyEncodesNonASCIITitle(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	n := testNotification()
	n.Title = "Batterie voll ☀"

	target := &Target{Name: "phone", Type: TargetNtfy, URL: srv.URL}
	if err := NewSender().Send(context.Background(), target, n); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	h := got.headers.Get("Title")
	if !strings.HasPrefix(h, "=?UTF-8?") {
		t.Errorf("Title header = %q, want an RFC 2047 encoded word", h)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(h)
	if err != nil {
		t.Fatalf("decoding title: %v", err)
	}
	if decoded != n.Title {
		t.Errorf("decoded title = %q, want %q", decoded, n.Title)
	}
}

// TestSender_StripsHeaderInjection makes sure a rendered value can never
// smuggle in a header of its own.
func TestSender_StripsHeaderInjection(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	n := testNotification()
	n.Title = "hi\r\nX-Injected: yes"

	target := &Target{Name: "phone", Type: TargetNtfy, URL: srv.URL}
	if err := NewSender().Send(context.Background(), target, n); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if h := got.headers.Get("X-Injected"); h != "" {
		t.Errorf("X-Injected header leaked through: %q", h)
	}
	if h := got.headers.Get("Title"); strings.ContainsAny(h, "\r\n") {
		t.Errorf("Title header still contains a line break: %q", h)
	}
}

func TestSender_WebhookDefaultJSON(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	target := &Target{Name: "hass", Type: TargetWebhook, URL: srv.URL + "/hook"}
	if err := NewSender().Send(context.Background(), target, testNotification()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if ct := got.headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var payload struct {
		Rule    string             `json:"rule"`
		Title   string             `json:"title"`
		Message string             `json:"message"`
		Device  string             `json:"device"`
		Metrics map[string]float64 `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, got.body)
	}
	if payload.Rule != "ev_charge_window" || payload.Device != "SN123" {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Metrics["battery_soc"] != 96 {
		t.Errorf("payload metrics = %+v", payload.Metrics)
	}
}

func TestSender_WebhookBodyTemplate(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)

	target := &Target{
		Name: "slack", Type: TargetWebhook, URL: srv.URL,
		Method:       "PUT",
		Headers:      map[string]string{"X-Custom": "growud"},
		BodyTemplate: `{"text": "{{title}}: {{message}} (soc {{battery_soc}})"}`,
	}
	if err := NewSender().Send(context.Background(), target, testNotification()); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if got.method != http.MethodPut {
		t.Errorf("method = %s, want PUT", got.method)
	}
	if got.headers.Get("X-Custom") != "growud" {
		t.Errorf("X-Custom = %q", got.headers.Get("X-Custom"))
	}
	want := `{"text": "Battery nearly full: Battery at 96% with 2.5h of sun left. (soc 96)"}`
	if got.body != want {
		t.Errorf("body = %q, want %q", got.body, want)
	}
}

func TestSender_ErrorStatus(t *testing.T) {
	srv, _ := captureServer(t, http.StatusForbidden)

	target := &Target{Name: "phone", Type: TargetNtfy, URL: srv.URL}
	err := NewSender().Send(context.Background(), target, testNotification())
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v, want it to mention the status code", err)
	}
}

func TestSender_UnknownType(t *testing.T) {
	target := &Target{Name: "x", Type: "pigeon", URL: "https://example.test"}
	if err := NewSender().Send(context.Background(), target, testNotification()); err == nil {
		t.Error("expected an error for an unknown target type")
	}
}
