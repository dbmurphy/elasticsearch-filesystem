package escore

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// httpClient is the low-level Elasticsearch transport: auth, TLS, timeouts, and
// a bounded-concurrency semaphore. It maps transport/status failures onto the
// ESFS error model.
type httpClient struct {
	base   string
	apiKey string
	hc     *http.Client
	sem    chan struct{}
}

func newHTTPClient(cfg Config) (*httpClient, error) {
	base := strings.TrimRight(cfg.Endpoint, "/")
	if base == "" {
		return nil, contract.Errf(contract.KindUsage, "no Elasticsearch endpoint configured")
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec // opt-in for test clusters
	if cfg.CACertFile != "" {
		pem, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, contract.Wrap(contract.KindUsage, err, "read CA cert %s", cfg.CACertFile)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, contract.Errf(contract.KindUsage, "no certificates parsed from %s", cfg.CACertFile)
		}
		tlsCfg.RootCAs = pool
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	conc := cfg.Concurrency
	if conc <= 0 {
		conc = 8
	}
	return &httpClient{
		base:   base,
		apiKey: cfg.ResolveAPIKey(),
		hc: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg, MaxIdleConnsPerHost: conc},
		},
		sem: make(chan struct{}, conc),
	}, nil
}

// response is a decoded ES response with status for callers to branch on.
type response struct {
	status int
	body   []byte
}

func (c *httpClient) do(ctx context.Context, method, path string, body any) (*response, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, contract.Wrap(contract.KindUpstream, ctx.Err(), "request canceled")
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, contract.Wrap(contract.KindInternal, err, "marshal request")
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, contract.Wrap(contract.KindInternal, err, "build request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "ApiKey "+c.apiKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, contract.Wrap(contract.KindUpstream, err, "elasticsearch request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, contract.Wrap(contract.KindUpstream, err, "read elasticsearch response")
	}
	return &response{status: resp.StatusCode, body: data}, nil
}

// statusError maps a non-2xx ES status to the ESFS error model.
func statusError(r *response, what string) error {
	switch r.status {
	case http.StatusNotFound:
		return contract.Errf(contract.KindNotFound, "%s: not found", what)
	case http.StatusUnauthorized, http.StatusForbidden:
		return contract.Errf(contract.KindPermission, "%s: not authorized (status %d)", what, r.status)
	case http.StatusConflict:
		return contract.Errf(contract.KindConflict, "%s: version conflict", what)
	case http.StatusRequestTimeout, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return contract.Errf(contract.KindUpstream, "%s: elasticsearch unavailable (status %d)", what, r.status)
	case http.StatusBadRequest:
		return contract.Errf(contract.KindUsage, "%s: %s", what, esErrorReason(r.body))
	default:
		return contract.Errf(contract.KindUpstream, "%s: unexpected status %d: %s", what, r.status, truncate(r.body, 200))
	}
}

func esErrorReason(body []byte) string {
	var e struct {
		Error struct {
			Reason string `json:"reason"`
			Type   string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error.Reason != "" {
		return fmt.Sprintf("%s (%s)", e.Error.Reason, e.Error.Type)
	}
	return truncate(body, 200)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
