// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// trustedProxyRealIP trusts forwarding headers only when the immediate peer is
// in one of the configured CIDRs. Without an allowlist, client-controlled
// headers are ignored and RemoteAddr remains authoritative.
func trustedProxyRealIP(rawCIDRs string, log *slog.Logger) func(http.Handler) http.Handler {
	trusted := parseTrustedProxyCIDRs(rawCIDRs, log)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := remoteIP(r.RemoteAddr)
			if peer != nil && ipInNetworks(peer, trusted) {
				if client := forwardedClientIP(r, trusted); client != nil {
					r.RemoteAddr = net.JoinHostPort(client.String(), "0")
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func parseTrustedProxyCIDRs(raw string, log *slog.Logger) []*net.IPNet {
	var networks []*net.IPNet

	for value := range strings.SplitSeq(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		_, network, err := net.ParseCIDR(value)
		if err != nil {
			log.Warn("ignoring invalid trusted proxy CIDR", "cidr", value)
			continue
		}

		networks = append(networks, network)
	}

	return networks
}

func forwardedClientIP(r *http.Request, trusted []*net.IPNet) net.IP {
	values := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(values) == 1 && strings.TrimSpace(values[0]) == "" {
		values = []string{r.Header.Get("X-Real-IP")}
	}

	for _, value := range slices.Backward(values) {
		ip := net.ParseIP(strings.TrimSpace(value))
		if ip != nil && !ipInNetworks(ip, trusted) {
			return ip
		}
	}

	return nil
}

func remoteIP(address string) net.IP {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}

	return net.ParseIP(host)
}

func ipInNetworks(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// ipRateLimiter is a per-client-IP token-bucket rate limiter with idle eviction.
// It fronts the auth-sensitive routes — local login/reset/invite and the OAuth
// register/token endpoints — to blunt credential brute-force, password-reset
// email bombing, and dynamic-client-registration spam (M6/M7). The client IP is
// r.RemoteAddr after chi's RealIP middleware (the real client behind the trusted
// Caddy proxy); note X-Forwarded-For is spoofable, so this is a speed bump, not a
// hard identity boundary.
type ipRateLimiter struct {
	mu       sync.Mutex `exhaustruct:"optional"`
	visitors map[string]*rateVisitor
	limit    rate.Limit
	burst    int
}

type rateVisitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time `exhaustruct:"optional"`
}

// newIPRateLimiter builds a limiter allowing perSecond sustained requests per IP
// with the given burst, and starts a background eviction loop for idle IPs that
// stops when ctx is cancelled (server shutdown) rather than leaking (D4).
func newIPRateLimiter(ctx context.Context, perSecond float64, burst int) *ipRateLimiter {
	rl := &ipRateLimiter{
		visitors: make(map[string]*rateVisitor),
		limit:    rate.Limit(perSecond),
		burst:    burst,
	}

	go rl.evictLoop(ctx)

	return rl
}

func (rl *ipRateLimiter) get(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[ip]
	if !ok {
		v = &rateVisitor{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.visitors[ip] = v
	}

	v.lastSeen = time.Now()

	return v.limiter
}

// evictLoop drops IPs idle for over 10 minutes so the map can't grow unbounded.
// It runs until ctx is cancelled.
func (rl *ipRateLimiter) evictLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > 10*time.Minute {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// middleware returns 429 when the client IP exceeds its bucket.
func (rl *ipRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		if !rl.get(ip).Allow() {
			http.Error(w, "too many requests", http.StatusTooManyRequests)

			return
		}

		next.ServeHTTP(w, r)
	})
}
