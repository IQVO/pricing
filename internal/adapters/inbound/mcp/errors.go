package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/domain/shared"
)

// maxIDLength bounds an identifier argument, like the REST adapter's path
// parameters and the A2A agent.
const maxIDLength = 128

// errorCatalogue names each typed error a read tool can meet with the same
// stable slug the REST adapter uses as the last segment of its RFC 9457
// "type" URI (internal/adapters/inbound/http/errors.go), so a client sees one
// error vocabulary over both surfaces. Only the errors a READ can raise are
// listed: a miss of any repository (ports.ErrNotFound) and the value
// validation of the domain (shared.ErrValidation).
var errorCatalogue = []struct {
	target error
	slug   string
}{
	{ports.ErrNotFound, "not-found"},
	{shared.ErrValidation, "validation-failed"},
}

// slugFor returns the REST slug of err; ok is false for an untyped error.
func slugFor(err error) (slug string, ok bool) {
	for _, entry := range errorCatalogue {
		if errors.Is(err, entry.target) {
			return entry.slug, true
		}
	}
	return "", false
}

// toolError builds a tool-level error "<slug>: <detail>". Returned from a
// handler it becomes an isError tool result, never a transport failure.
func toolError(slug, detail string) error {
	return fmt.Errorf("%s: %s", slug, detail)
}

// mapError turns a use-case/repository error into the tool error the model
// sees. Typed errors are prefixed with their REST slug and keep their
// message; anything else is logged and reported generically so
// infrastructure details (DSNs, SQL) never reach the model.
func mapError(ctx context.Context, err error) error {
	if slug, ok := slugFor(err); ok {
		return toolError(slug, err.Error())
	}
	slog.ErrorContext(ctx, "mcp tool failed with an unexpected error", "error", err)
	return toolError("internal-error", "an unexpected internal error occurred")
}

// nameable reports whether id can name a resource at all. The REST adapter
// answers 404 for an identifier longer than maxIDLength and for one carrying
// U+0000 (PostgreSQL text cannot hold it: the statement would abort with
// SQLSTATE 22021 and surface as a 500), so this adapter answers the same
// not-found before the identifier reaches a use case.
func nameable(id string) bool {
	return len(id) <= maxIDLength && !strings.ContainsRune(id, 0)
}

// checkID validates one identifier argument: blank is validation-failed, an
// identifier that can never name a resource is not-found (with notFound as its
// detail, which never echoes the identifier).
func checkID(field, id, notFound string) error {
	if strings.TrimSpace(id) == "" {
		return toolError("validation-failed", field+" is required")
	}
	if !nameable(id) {
		return toolError("not-found", notFound)
	}
	return nil
}
