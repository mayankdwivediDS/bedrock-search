package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// appWithAuth wires a single /admin-only route protected by requireAdmin for
// the tests. Keeps each test focused on the auth semantics without dragging
// in the full server construction.
func appWithAuth(cfg AdminAuthConfig) *fiber.App {
	app := fiber.New()
	mw := requireAdmin(cfg)
	app.Post("/x", mw, func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func do(t *testing.T, app *fiber.App, headers map[string]string) (status int, body string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/x", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAdmin_FailClosedWhenTokenUnset(t *testing.T) {
	app := appWithAuth(AdminAuthConfig{Token: ""})
	status, body := do(t, app, nil)
	if status != fiber.StatusServiceUnavailable {
		t.Errorf("no token: status=%d body=%s, want 503", status, body)
	}
	if !strings.Contains(body, "admin endpoints disabled") {
		t.Errorf("expected clear error message, got %q", body)
	}
}

func TestAdmin_RejectsMissingToken(t *testing.T) {
	app := appWithAuth(AdminAuthConfig{Token: "s3cret"})
	status, _ := do(t, app, nil)
	if status != fiber.StatusUnauthorized {
		t.Errorf("missing header: status=%d, want 401", status)
	}
}

func TestAdmin_RejectsWrongToken(t *testing.T) {
	app := appWithAuth(AdminAuthConfig{Token: "s3cret"})
	status, _ := do(t, app, map[string]string{"X-API-Key": "nope"})
	if status != fiber.StatusUnauthorized {
		t.Errorf("wrong token: status=%d, want 401", status)
	}
}

func TestAdmin_AcceptsXAPIKeyHeader(t *testing.T) {
	app := appWithAuth(AdminAuthConfig{Token: "s3cret"})
	status, body := do(t, app, map[string]string{"X-API-Key": "s3cret"})
	if status != fiber.StatusOK || body != "ok" {
		t.Errorf("X-API-Key valid: status=%d body=%s", status, body)
	}
}

func TestAdmin_AcceptsBearerToken(t *testing.T) {
	app := appWithAuth(AdminAuthConfig{Token: "s3cret"})
	status, body := do(t, app, map[string]string{"Authorization": "Bearer s3cret"})
	if status != fiber.StatusOK || body != "ok" {
		t.Errorf("Bearer valid: status=%d body=%s", status, body)
	}
}

func TestAdmin_BearerWithWrongPrefixDoesNotLeakAccess(t *testing.T) {
	app := appWithAuth(AdminAuthConfig{Token: "s3cret"})
	// "Basic" isn't our scheme; the token isn't extracted, so auth fails.
	status, _ := do(t, app, map[string]string{"Authorization": "Basic s3cret"})
	if status != fiber.StatusUnauthorized {
		t.Errorf("non-Bearer auth header accepted: status=%d", status)
	}
}

func TestAdmin_IPAllowlist_Permissive(t *testing.T) {
	// Empty allowlist = no restriction; just token check.
	app := appWithAuth(AdminAuthConfig{Token: "s3cret", AllowedIPs: nil})
	status, _ := do(t, app, map[string]string{"X-API-Key": "s3cret"})
	if status != fiber.StatusOK {
		t.Errorf("empty allowlist should permit any IP: status=%d", status)
	}
}

func TestAdmin_IPAllowlist_RejectsNonMatchingIP(t *testing.T) {
	// fiber's Test runs with client IP 0.0.0.0 by default; a 10.x
	// allowlist must reject it.
	app := appWithAuth(AdminAuthConfig{
		Token:      "s3cret",
		AllowedIPs: []string{"10.0.0.0/8"},
	})
	status, body := do(t, app, map[string]string{"X-API-Key": "s3cret"})
	if status != fiber.StatusForbidden {
		t.Errorf("non-matching IP should be forbidden: status=%d body=%s", status, body)
	}
}

func TestAdmin_ParseIPMatcher_InvalidEntriesDropped(t *testing.T) {
	matchers := parseAllowedIPs([]string{"10.0.0.0/8", "not-an-ip", "192.168.1.1"})
	if len(matchers) != 2 {
		t.Errorf("expected invalid entry dropped, got %d matchers", len(matchers))
	}
}
