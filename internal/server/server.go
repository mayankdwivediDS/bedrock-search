package server

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"go-suggest-neo/internal/lifecycle"
)

// New builds the Fiber app around an Instance. Call app.Listen to serve.
//
// The whole surface is six routes:
//
//	GET  /health    public
//	GET  /suggest   public
//	POST /upload    write  (admin if ADMIN_TOKEN set)
//	POST /reload    write  (admin if ADMIN_TOKEN set)
//	GET  /backup    write  (admin if ADMIN_TOKEN set)
//	POST /restore   write  (admin if ADMIN_TOKEN set)
func New(inst *Instance) *fiber.App {
	cfg := inst.cfg
	app := fiber.New(fiber.Config{
		// Allow large CSV / backup uploads.
		BodyLimit: cfg.MaxUploadMB * 1024 * 1024,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if ferr, ok := err.(*fiber.Error); ok {
				code = ferr.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
		DisableStartupMessage: false,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} ${status} ${latency} ${method} ${path}\n",
	}))

	// Per-route metrics (counts + latency). Registered early so it wraps
	// every handler below.
	metrics := NewMetrics()
	app.Use(metricsMiddleware(metrics))
	app.Use(cors.New(cors.Config{
		AllowOrigins: strings.Join(cfg.CORSOrigins, ","),
		AllowMethods: "GET,POST,OPTIONS",
		AllowHeaders: "Origin, Content-Type, Accept, X-API-Key, Authorization",
	}))

	// Admin gate: required only when ADMIN_TOKEN is set. Unset = open (handy
	// for a local single-user setup; a warning is logged at boot).
	var admin fiber.Handler
	if cfg.AdminToken != "" {
		admin = requireAdmin(AdminAuthConfig{
			Token:      cfg.AdminToken,
			AllowedIPs: cfg.AdminAllowedIPs,
		})
	} else {
		slog.Warn("ADMIN_TOKEN not set — /upload, /reload, /backup, /restore are open to anyone who can reach this port")
		admin = func(c *fiber.Ctx) error { return c.Next() }
	}

	// Public reads.
	app.Get("/", statusHandler(inst))
	app.Get("/health", statusHandler(inst))
	app.Get("/suggest", suggestHandler(inst))
	app.Get("/metrics", metricsPromHandler(metrics, inst))  // Prometheus format
	app.Get("/metrics/json", metricsHandler(metrics, inst)) // JSON format

	// Writes / data management.
	app.Post("/upload", admin, uploadHandler(inst))
	app.Post("/reload", admin, reloadHandler(inst))
	app.Get("/backup", admin, backupHandler(inst))
	app.Post("/restore", admin, restoreHandler(inst))
	app.Get("/blacklist", admin, listBlacklistHandler(inst))
	app.Post("/blacklist", admin, blacklistHandler(inst))

	// Interactive API docs (Swagger UI) at /docs, spec at /openapi.yaml.
	registerDocs(app, cfg)

	return app
}

// statusHandler — GET / and GET /health. Liveness plus corpus size + the
// live version.
func statusHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		body := fiber.Map{
			"status":       "ok",
			"engine":       "go-suggest-neo",
			"corpus_words": corpusWords(inst),
		}
		if v, err := lifecycle.ReadCurrent(inst.Paths()); err == nil {
			body["version"] = v
		}
		return c.JSON(body)
	}
}

// suggestHandler — GET /suggest?query=...&limit=...&fuzzy=...
// The engine query logic is unchanged from the original service.
func suggestHandler(inst *Instance) fiber.Handler {
	return func(c *fiber.Ctx) error {
		cfg := inst.cfg
		e := inst.Engine()
		if e == nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "corpus reloading, try again shortly")
		}

		query := c.Query("query")
		if query == "" {
			return fiber.NewError(fiber.StatusBadRequest, "query parameter is required")
		}
		limit := parseIntParam(c.Query("limit"), cfg.DefaultLimit, 1, cfg.MaxLimit)
		fuzzy := c.QueryBool("fuzzy", false)

		ctx, cancel := context.WithTimeout(c.UserContext(),
			time.Duration(cfg.MaxQueryLatencyMs)*time.Millisecond)
		defer cancel()

		results, err := e.Suggest(ctx, query, limit, fuzzy)
		if err != nil {
			if isClientError(err) {
				return fiber.NewError(fiber.StatusBadRequest, err.Error())
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return fiber.NewError(fiber.StatusGatewayTimeout, "query deadline exceeded")
			}
			return err
		}
		return c.JSON(fiber.Map{
			"query":       query,
			"suggestions": results,
			"count":       len(results),
		})
	}
}

// isClientError identifies engine errors that map to HTTP 400.
func isClientError(err error) bool {
	return strings.Contains(err.Error(), "query must be at least")
}

func parseIntParam(s string, def, min, max int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}
