// Package server — admin auth middleware for mutation/destructive endpoints.
//
// Design (fail-closed):
//
//	1. ADMIN_TOKEN unset     → admin routes return 503 Service Unavailable.
//	2. ADMIN_TOKEN set       → requests must present the same token via
//	                            X-API-Key header OR Authorization: Bearer <t>.
//	3. ADMIN_ALLOWED_IPS set → client IP must match one of the listed
//	                            IPs or CIDRs (defense-in-depth).
//
// Token comparison uses crypto/subtle to avoid timing side-channels.
// Denied attempts are logged with client IP + path for audit.
package server

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// AdminAuthConfig is the bundle the middleware factory needs.
type AdminAuthConfig struct {
	Token      string
	AllowedIPs []string // IPs or CIDRs; empty = no IP restriction
}

// requireAdmin returns a Fiber middleware that gates access to admin
// endpoints. Intended to be chained before the real handler:
//
//	app.Post("/corpus/apply", requireAdmin(cfg), corpusApply(...))
func requireAdmin(cfg AdminAuthConfig) fiber.Handler {
	allowed := parseAllowedIPs(cfg.AllowedIPs)
	tokenBytes := []byte(cfg.Token)
	tokenConfigured := len(tokenBytes) > 0

	return func(c *fiber.Ctx) error {
		// 1. Fail-closed: no token configured → admin endpoints unavailable.
		if !tokenConfigured {
			slog.Warn("admin endpoint called with ADMIN_TOKEN unset — rejecting",
				"path", c.Path(), "ip", c.IP(), "method", c.Method())
			return fiber.NewError(fiber.StatusServiceUnavailable,
				"admin endpoints disabled: set ADMIN_TOKEN to enable")
		}

		// 2. IP allowlist (defense-in-depth).
		if len(allowed) > 0 {
			clientIP := net.ParseIP(c.IP())
			if clientIP == nil || !ipMatchesAny(clientIP, allowed) {
				slog.Warn("admin denied: IP not in allowlist",
					"path", c.Path(), "ip", c.IP())
				return fiber.NewError(fiber.StatusForbidden, "client IP not allowed")
			}
		}

		// 3. Token check (constant time).
		got := extractToken(c)
		if subtle.ConstantTimeCompare([]byte(got), tokenBytes) != 1 {
			slog.Warn("admin denied: invalid or missing token",
				"path", c.Path(), "ip", c.IP())
			return fiber.NewError(fiber.StatusUnauthorized, "invalid admin token")
		}

		return c.Next()
	}
}

// extractToken pulls the candidate token from either X-API-Key or an
// Authorization: Bearer <token> header. Missing → "".
func extractToken(c *fiber.Ctx) string {
	if t := c.Get("X-API-Key"); t != "" {
		return t
	}
	auth := c.Get("Authorization")
	const bearer = "Bearer "
	if strings.HasPrefix(auth, bearer) {
		return strings.TrimPrefix(auth, bearer)
	}
	return ""
}

// parseAllowedIPs turns each spec ("1.2.3.4" or "10.0.0.0/8") into a matcher.
// Invalid entries are dropped with a warning at boot — the server still
// starts; it just won't honour the bad entry.
func parseAllowedIPs(specs []string) []ipMatcher {
	out := make([]ipMatcher, 0, len(specs))
	for _, s := range specs {
		m, err := parseIPMatcher(s)
		if err != nil {
			slog.Warn("invalid ADMIN_ALLOWED_IPS entry, skipping", "entry", s, "err", err)
			continue
		}
		out = append(out, m)
	}
	return out
}

// ipMatcher holds either a single IP or a CIDR. Cheaper than walking
// net.IP equality / net.IPNet.Contains inside a switch at match time.
type ipMatcher struct {
	single net.IP
	cidr   *net.IPNet
}

func parseIPMatcher(s string) (ipMatcher, error) {
	if strings.Contains(s, "/") {
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			return ipMatcher{}, err
		}
		return ipMatcher{cidr: cidr}, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return ipMatcher{}, &net.ParseError{Type: "IP", Text: s}
	}
	return ipMatcher{single: ip}, nil
}

func (m ipMatcher) match(ip net.IP) bool {
	if m.cidr != nil {
		return m.cidr.Contains(ip)
	}
	return m.single != nil && m.single.Equal(ip)
}

func ipMatchesAny(ip net.IP, matchers []ipMatcher) bool {
	for _, m := range matchers {
		if m.match(ip) {
			return true
		}
	}
	return false
}
