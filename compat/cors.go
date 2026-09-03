package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultCORSAllowedHeaders = "Content-Type, X-Request-ID"
	allowedCORSMethods        = "GET, POST, OPTIONS"
)

type corsConfig struct {
	enabled        bool
	allowedOrigins map[string]struct{}
	allowedHeaders map[string]struct{}
	allowHeaders   string
}

func newCORSConfig(enabled bool, rawOrigins, rawHeaders string) (corsConfig, error) {
	origins, err := parseCORSOrigins(rawOrigins)
	if err != nil {
		return corsConfig{}, err
	}
	if enabled && len(origins) == 0 {
		return corsConfig{}, fmt.Errorf("CORS_ENABLED=true requires at least one allowed origin")
	}
	headers, allowHeaders, err := parseCORSHeaders(rawHeaders)
	if err != nil {
		return corsConfig{}, err
	}
	return corsConfig{enabled: enabled, allowedOrigins: origins, allowedHeaders: headers, allowHeaders: allowHeaders}, nil
}

func parseCORSOrigins(raw string) (map[string]struct{}, error) {
	origins := make(map[string]struct{})
	for _, value := range splitCORSValues(raw) {
		if value == "*" {
			return nil, fmt.Errorf("wildcard origin is not supported; list exact origins")
		}
		if value != "null" {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				return nil, fmt.Errorf("invalid origin %q", value)
			}
		}
		origins[value] = struct{}{}
	}
	return origins, nil
}

func parseCORSHeaders(raw string) (map[string]struct{}, string, error) {
	headers := make(map[string]struct{})
	values := splitCORSValues(raw)
	for _, value := range values {
		canonical := http.CanonicalHeaderKey(value)
		if canonical == "" || strings.ContainsAny(value, " \t\r\n") {
			return nil, "", fmt.Errorf("invalid header %q", value)
		}
		headers[canonical] = struct{}{}
	}
	return headers, strings.Join(values, ", "), nil
}

func splitCORSValues(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (config corsConfig) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !config.enabled {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Add("Vary", "Origin")
		if !config.originAllowed(origin) {
			if r.Method == http.MethodOptions {
				http.Error(w, "CORS origin is not allowed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		if r.Method == http.MethodOptions {
			if requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method"))); requestedMethod != "" && !corsMethodAllowed(requestedMethod) {
				http.Error(w, "CORS method is not allowed", http.StatusForbidden)
				return
			}
			if requestedHeaders := r.Header.Get("Access-Control-Request-Headers"); !config.headersAllowed(requestedHeaders) {
				http.Error(w, "CORS header is not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", allowedCORSMethods)
			w.Header().Set("Access-Control-Allow-Headers", config.allowHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (config corsConfig) originAllowed(origin string) bool {
	_, ok := config.allowedOrigins[origin]
	return ok
}

func (config corsConfig) headersAllowed(raw string) bool {
	for _, value := range splitCORSValues(raw) {
		if _, ok := config.allowedHeaders[http.CanonicalHeaderKey(value)]; !ok {
			return false
		}
	}
	return true
}

func corsMethodAllowed(method string) bool {
	return method == http.MethodGet || method == http.MethodPost
}
