// Package cdn purges the edge cache by tag (ADR-0019). Storefront pages
// carry Cache-Tag headers; when something a shopper sees changes, the
// worker purges the tags of the pages that show it.
package cdn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/roshbaikfb/iftahonline/internal/platform/retry"
)

// Purger removes cached responses by tag.
type Purger interface {
	PurgeTags(ctx context.Context, tags []string) error
}

// Nop purges nothing: for deployments without an edge cache (and
// development), where the origin's own TTL keeps pages fresh.
type Nop struct{}

// PurgeTags implements Purger.
func (Nop) PurgeTags(context.Context, []string) error { return nil }

// cloudflareBatch is how many tags one purge request carries, below the
// API's per-request limit on every plan.
const cloudflareBatch = 30

// Cloudflare purges through the Cloudflare API (purge by tag).
type Cloudflare struct {
	base  string
	zone  string
	token string
	http  *http.Client
	retry retry.Policy
}

// CloudflareOptions configures NewCloudflare.
type CloudflareOptions struct {
	ZoneID  string
	Token   string        // API token with the zone's Cache Purge permission
	BaseURL string        // default https://api.cloudflare.com/client/v4 (tests point it elsewhere)
	Timeout time.Duration // per request; default 10 s
}

// NewCloudflare returns a Cloudflare purger.
func NewCloudflare(o CloudflareOptions) (*Cloudflare, error) {
	if o.ZoneID == "" || o.Token == "" {
		return nil, errors.New("cdn: cloudflare needs a zone ID and a token")
	}
	if o.BaseURL == "" {
		o.BaseURL = "https://api.cloudflare.com/client/v4"
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	return &Cloudflare{base: o.BaseURL, zone: o.ZoneID, token: o.Token, http: &http.Client{Timeout: o.Timeout}, retry: retry.Default()}, nil
}

// PurgeTags purges tags in batches. Rate limiting and server errors are
// retried with backoff; a rejected request (bad token, bad tag) is not.
// Purging is idempotent, so a job may repeat it safely.
func (c *Cloudflare) PurgeTags(ctx context.Context, tags []string) error {
	for start := 0; start < len(tags); start += cloudflareBatch {
		batch := tags[start:min(start+cloudflareBatch, len(tags))]
		if err := retry.Do(ctx, c.retry, func(ctx context.Context) error { return c.purge(ctx, batch) }); err != nil {
			return err
		}
	}
	return nil
}

type purgeResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *Cloudflare) purge(ctx context.Context, tags []string) error {
	body, err := json.Marshal(map[string][]string{"tags": tags})
	if err != nil {
		return retry.Permanent(fmt.Errorf("cdn: encode: %w", err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/zones/"+c.zone+"/purge_cache", bytes.NewReader(body))
	if err != nil {
		return retry.Permanent(fmt.Errorf("cdn: request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cdn: purge: %w", err) // network: retry
	}
	defer func() { _ = resp.Body.Close() }()
	var out purgeResponse
	if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil && resp.StatusCode < 300 {
		return fmt.Errorf("cdn: purge: decode response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("cdn: purge: status %d", resp.StatusCode)
	case resp.StatusCode >= 300 || !out.Success:
		msg := ""
		if len(out.Errors) > 0 {
			msg = fmt.Sprintf(": %d %s", out.Errors[0].Code, out.Errors[0].Message)
		}
		return retry.Permanent(fmt.Errorf("cdn: purge rejected (status %d)%s", resp.StatusCode, msg))
	}
	return nil
}
