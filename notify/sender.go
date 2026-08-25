package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"
)

// Notification is a fully rendered message, ready to deliver.
type Notification struct {
	Rule     string
	Title    string
	Message  string
	Device   string
	At       time.Time
	Priority string
	Tags     []string
	Metrics  map[string]float64

	// Fields is the placeholder context a webhook body_template renders against.
	Fields map[string]string
}

// Sender delivers notifications over HTTP.
type Sender struct {
	client *http.Client
}

// NewSender returns a Sender with a sensible request timeout.
func NewSender() *Sender {
	return &Sender{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send delivers one notification to one target.
func (s *Sender) Send(ctx context.Context, t *Target, n Notification) error {
	switch t.Type {
	case TargetNtfy:
		return s.sendNtfy(ctx, t, n)
	case TargetWebhook:
		return s.sendWebhook(ctx, t, n)
	default:
		return fmt.Errorf("unknown target type %q", t.Type)
	}
}

// sendNtfy publishes using ntfy's plain-text convention: the message is the
// request body, everything else rides along in headers.
func (s *Sender) sendNtfy(ctx context.Context, t *Target, n Notification) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, strings.NewReader(n.Message))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	if n.Title != "" {
		req.Header.Set("Title", encodeHeader(n.Title))
	}
	if priority := firstNonEmpty(n.Priority, t.Priority); priority != "" {
		req.Header.Set("Priority", strings.ToLower(priority))
	}
	tags := n.Tags
	if len(tags) == 0 {
		tags = t.Tags
	}
	if len(tags) > 0 {
		req.Header.Set("Tags", encodeHeader(strings.Join(tags, ",")))
	}
	if t.Click != "" {
		req.Header.Set("Click", sanitizeHeader(t.Click))
	}

	applyAuth(req, t)
	applyExtraHeaders(req, t)

	return s.do(req, t)
}

// sendWebhook posts the notification to an arbitrary endpoint, using
// body_template when one is configured and a JSON payload otherwise.
func (s *Sender) sendWebhook(ctx context.Context, t *Target, n Notification) error {
	method := http.MethodPost
	if t.Method != "" {
		method = strings.ToUpper(t.Method)
	}

	var body []byte
	contentType := "application/json"

	if t.BodyTemplate != "" {
		fields := make(map[string]string, len(n.Fields)+1)
		for k, v := range n.Fields {
			fields[k] = v
		}
		fields["message"] = n.Message
		fields["title"] = n.Title
		body = []byte(Render(t.BodyTemplate, fields))
		contentType = "text/plain; charset=utf-8"
	} else {
		payload := map[string]any{
			"rule":    n.Rule,
			"title":   n.Title,
			"message": n.Message,
			"device":  n.Device,
			"time":    n.At.Format(time.RFC3339),
			"metrics": n.Metrics,
		}
		if n.Priority != "" {
			payload["priority"] = n.Priority
		}
		if len(n.Tags) > 0 {
			payload["tags"] = n.Tags
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encoding payload: %w", err)
		}
		body = encoded
	}

	req, err := http.NewRequestWithContext(ctx, method, t.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	applyAuth(req, t)
	applyExtraHeaders(req, t)

	return s.do(req, t)
}

func (s *Sender) do(req *http.Request, t *Target) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending to %s: %w", t.Name, err)
	}
	defer resp.Body.Close()

	// Read a bounded slice of the response so a chatty endpoint can't blow up
	// the log, but errors still carry enough context to debug.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("target %s returned HTTP %d: %s", t.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// applyExtraHeaders sets the target's configured headers last, so a user can
// deliberately override anything set for them — including the Authorization
// header, for an endpoint that wants a different auth scheme.
func applyExtraHeaders(req *http.Request, t *Target) {
	for k, v := range t.Headers {
		req.Header.Set(sanitizeHeader(k), sanitizeHeader(v))
	}
}

// applyAuth reads the bearer token from the environment. The token is never
// stored in the config file and is never logged.
func applyAuth(req *http.Request, t *Target) {
	if t.AuthTokenEnv == "" {
		return
	}
	if token := os.Getenv(t.AuthTokenEnv); token != "" {
		req.Header.Set("Authorization", "Bearer "+sanitizeHeader(token))
	}
}

// sanitizeHeader strips CR and LF so a rendered value can never inject a
// header of its own.
func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
}

// encodeHeader sanitizes a value and, when it contains non-ASCII characters,
// wraps it in an RFC 2047 encoded word — which ntfy decodes on the way in.
func encodeHeader(v string) string {
	v = sanitizeHeader(v)
	if isASCII(v) {
		return v
	}
	return mime.BEncoding.Encode("UTF-8", v)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7e || s[i] < 0x20 {
			return false
		}
	}
	return true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
