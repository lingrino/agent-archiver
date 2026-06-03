package tool

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const livenessUserAgent = "Mozilla/5.0 (compatible; agent-archiver/1.0)"

// livenessClient follows redirects (Go's default policy, up to 10 hops) so the
// probe reflects where the URL actually lands.
var livenessClient = &http.Client{Timeout: 25 * time.Second}

// Liveness is the result of a cheap reachability probe.
type Liveness struct {
	Alive      bool   // false only on strong "content is gone" signals
	StatusCode int    // final HTTP status (0 if the request never completed)
	FinalURL   string // URL after following redirects
	Redirected bool   // true if the request was redirected
	Reason     string // human-readable explanation when not alive
}

// CheckLiveness performs a preflight reachability probe before the expensive
// archive pipeline runs, so dead links can be logged and skipped cheaply.
//
// A URL is reported dead (Alive=false) only on strong signals that the content
// is gone: the host is unreachable (DNS / connection / TLS failure) or the
// final response status after following redirects is a client/server error
// (>= 400). Access-control and rate-limit statuses (401, 403, 429) are NOT
// treated as dead — the headless scrapers (Cloudflare/Firecrawl) often get past
// anti-bot blocks that a bare request cannot, so the pipeline is allowed to try
// and the incomplete-content check handles genuine paywalls. A redirect that
// lands on a healthy page is alive; FinalURL reports where it landed.
func CheckLiveness(ctx context.Context, rawURL string) Liveness {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Liveness{Reason: fmt.Sprintf("invalid URL: %v", err)}
	}
	req.Header.Set("User-Agent", livenessUserAgent)

	resp, err := livenessClient.Do(req)
	if err != nil {
		return Liveness{Reason: fmt.Sprintf("request failed: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}

	result := Liveness{
		StatusCode: resp.StatusCode,
		FinalURL:   finalURL,
		Redirected: finalURL != rawURL,
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden,
		resp.StatusCode == http.StatusTooManyRequests:
		// Blocked / rate-limited, not gone — let the pipeline attempt it.
		result.Alive = true
	case resp.StatusCode >= 400:
		result.Reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
	default:
		result.Alive = true
	}

	return result
}
