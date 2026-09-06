// Package musicdiscovery serves public music metadata. Library and request
// state deliberately remain in the existing request service.
package musicdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

const userAgent = "Cantinarr/1.0 (+https://github.com/windoze95/cantinarr/issues)"

var errUnavailable = errors.New("music provider is unavailable; please retry")

// Each provider has its own serial start gate, including retries. Waiting
// callers honor their deadline and cannot accumulate reservations in the future.
type provider struct {
	base     string
	client   *http.Client
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func newProvider(base string) *provider {
	return &provider{base: base, interval: time.Second, client: &http.Client{
		Transport: httpx.External(), Timeout: 12 * time.Second,
		// Metadata endpoints have fixed origins. Only artwork needs redirects.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *provider) wait(ctx context.Context) error {
	for {
		p.mu.Lock()
		delay := time.Until(p.next)
		if delay <= 0 {
			p.next = time.Now().Add(p.interval)
			p.mu.Unlock()
			return ctx.Err()
		}
		p.mu.Unlock()
		if err := pause(ctx, delay); err != nil {
			return err
		}
	}
}

func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *provider) get(ctx context.Context, path string, dst any) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := p.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
		if err != nil {
			return errUnavailable
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/json")
		resp, err := p.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			// Respect a bounded Retry-After. A long hold returns a retryable
			// error instead of tying up the caller indefinitely.
			delay := time.Duration(attempt+1) * time.Second
			if seconds, e := time.ParseDuration(resp.Header.Get("Retry-After") + "s"); e == nil && seconds > delay {
				delay = seconds
			}
			if at, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil && time.Until(at) > delay {
				delay = time.Until(at)
			}
			p.mu.Lock()
			if until := time.Now().Add(delay); until.After(p.next) {
				p.next = until
			}
			p.mu.Unlock()
			if delay > 5*time.Second {
				return errUnavailable
			}
			continue
		}
		if resp.StatusCode != http.StatusOK || readErr != nil || len(data) > 16<<20 {
			return errUnavailable
		}
		if err := json.Unmarshal(data, dst); err != nil {
			return fmt.Errorf("invalid music provider response")
		}
		return nil
	}
	return errUnavailable
}

type cacheEntry struct {
	body    []byte
	expires time.Time
}
type pending struct {
	done chan struct{}
	body []byte
	err  error
}

// A bounded metadata/artwork cache also coalesces identical concurrent reads.
// A canceled browser does not cancel a shared fetch another user is awaiting.
type memo struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	pending map[string]*pending
	bytes   int
}

func (m *memo) get(ctx context.Context, key string, ttl time.Duration, load func(context.Context) ([]byte, error)) ([]byte, error) {
	m.mu.Lock()
	if e, ok := m.entries[key]; ok && time.Now().Before(e.expires) {
		m.mu.Unlock()
		return e.body, nil
	}
	if m.pending == nil {
		m.pending = make(map[string]*pending)
	}
	p, exists := m.pending[key]
	if !exists {
		if len(m.pending) >= 64 {
			m.mu.Unlock()
			return nil, errUnavailable
		}
		p = &pending{done: make(chan struct{})}
		m.pending[key] = p
		go func() {
			work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
			defer cancel()
			body, err := load(work)
			m.mu.Lock()
			defer m.mu.Unlock()
			if err == nil {
				if m.entries == nil {
					m.entries = make(map[string]cacheEntry)
				}
				// Expired entries leave first; capacity eviction never affects
				// a response already handed to a reader.
				for k, e := range m.entries {
					if time.Now().After(e.expires) || k == key {
						m.bytes -= len(e.body)
						delete(m.entries, k)
					}
				}
				for k, e := range m.entries {
					if len(m.entries) < 512 && m.bytes+len(body) <= 32<<20 {
						break
					}
					m.bytes -= len(e.body)
					delete(m.entries, k)
				}
				m.entries[key] = cacheEntry{body: body, expires: time.Now().Add(ttl)}
				m.bytes += len(body)
			}
			p.body, p.err = body, err
			delete(m.pending, key)
			close(p.done)
		}()
	}
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return p.body, p.err
	}
}
