package server

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/gofiber/swagger"

	"go-suggest-neo/internal/config"
)

// openapiYAML is the hand-written API spec served to the Swagger UI. It is
// edited by hand (no codegen step) — keep it in sync when routes change.
//
//go:embed openapi.yaml
var openapiYAML []byte

// registerDocs wires the Swagger UI at /docs and the raw spec at
// /openapi.yaml. When SWAGGER_USER and SWAGGER_PASSWORD are both set, the
// docs page (and the spec) require HTTP Basic Auth.
func registerDocs(app *fiber.App, cfg *config.Config) {
	if !cfg.SwaggerEnabled {
		return
	}

	guard := func(c *fiber.Ctx) error { return c.Next() } // open by default
	if cfg.SwaggerUser != "" && cfg.SwaggerPassword != "" {
		guard = basicauth.New(basicauth.Config{
			Users: map[string]string{cfg.SwaggerUser: cfg.SwaggerPassword},
			Realm: "go-suggest-neo docs",
		})
	}

	app.Get("/openapi.yaml", guard, func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "application/yaml")
		return c.Send(openapiYAML)
	})
	app.Get("/docs/*", guard, swagger.New(swagger.Config{
		URL: "/openapi.yaml",
	}))
}
