// Package testkit holds test helpers shared by module tests: the
// cross-tenant assertion every endpoint test uses, a registry proving every
// store-scoped operation has one, a pgx query counter, and catalog checks
// that every tenant table is behind RLS. Production code must not import
// it.
package testkit

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// StoreScopedPrefix is the path prefix of operations that act inside one
// store; each needs a cross-tenant test.
const StoreScopedPrefix = "/v1/stores/{store_id}"

// CrossTenantCase is one attempt by store A's member to reach store B.
type CrossTenantCase struct {
	// OperationID names the operation in api/openapi.yaml.
	OperationID string
	// Request carries store A's credentials and targets store B's
	// resource (B's store ID in the path, or A's store with B's resource ID).
	Request *http.Request
	// Leaks are values belonging to B (IDs, names, emails) that must not
	// appear anywhere in the response.
	Leaks []string
	// Unchanged, if set, verifies B's data afterwards (required for writes).
	Unchanged func(t testing.TB)
}

// Registry records which operations have a cross-tenant test.
type Registry struct {
	mu      sync.Mutex
	covered map[string]bool
}

// AssertCrossTenantDenied sends c.Request to h and asserts the attempt is
// indistinguishable from asking for something that does not exist: 404
// problem+json, no trace of B's data, and B's data unchanged. It records
// c.OperationID in reg.
func AssertCrossTenantDenied(t testing.TB, reg *Registry, h http.Handler, c CrossTenantCase) {
	t.Helper()
	if c.OperationID == "" || c.Request == nil {
		t.Fatal("testkit: CrossTenantCase needs OperationID and Request")
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Unchanged == nil {
		t.Fatalf("testkit: %s %s is a write; set Unchanged to prove B's data survived", c.Request.Method, c.OperationID)
	}
	reg.record(c.OperationID)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, c.Request)
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("%s: cross-tenant status = %d, want 404; body: %s", c.OperationID, res.StatusCode, body)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("%s: cross-tenant content type = %q, want application/problem+json", c.OperationID, ct)
	}
	var headers strings.Builder
	for k, vs := range res.Header {
		headers.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	for _, leak := range c.Leaks {
		if leak == "" {
			continue
		}
		if strings.Contains(string(body), leak) || strings.Contains(headers.String(), leak) {
			t.Errorf("%s: response leaks store B's value %q", c.OperationID, leak)
		}
	}
	if c.Unchanged != nil {
		c.Unchanged(t)
	}
}

func (r *Registry) record(op string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.covered == nil {
		r.covered = map[string]bool{}
	}
	r.covered[op] = true
}

// Missing returns the operations in required that have no cross-tenant
// test yet, sorted.
func (r *Registry) Missing(required []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, op := range required {
		if !r.covered[op] {
			out = append(out, op)
		}
	}
	sort.Strings(out)
	return out
}

// StoreScopedOperations lists the operationIds under StoreScopedPrefix,
// optionally limited to the given tags (a module's tests pass its tag).
func StoreScopedOperations(spec *openapi3.T, tags ...string) []string {
	var ops []string
	for path, item := range spec.Paths.Map() {
		if !strings.HasPrefix(path, StoreScopedPrefix) {
			continue
		}
		for _, op := range item.Operations() {
			if len(tags) > 0 && !slices.ContainsFunc(op.Tags, func(t string) bool { return slices.Contains(tags, t) }) {
				continue
			}
			ops = append(ops, op.OperationID)
		}
	}
	sort.Strings(ops)
	return ops
}
