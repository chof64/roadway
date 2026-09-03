package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSAllowsConfiguredWebOrigin(t *testing.T) {
	config, err := newCORSConfig(true, "https://app.example,capacitor://localhost", defaultCORSAllowedHeaders)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := config.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodOptions, "/ors/v2/directions/driving-car", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type, x-request-id")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || called {
		t.Fatalf("unexpected preflight response: status=%d called=%v", recorder.Code, called)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("unexpected allowed origin: %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}
	if recorder.Header().Get("Access-Control-Allow-Methods") != allowedCORSMethods {
		t.Fatalf("unexpected allowed methods: %q", recorder.Header().Get("Access-Control-Allow-Methods"))
	}
	if recorder.Header().Get("Access-Control-Allow-Headers") != defaultCORSAllowedHeaders {
		t.Fatalf("unexpected allowed headers: %q", recorder.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestCORSRejectsUnconfiguredPreflightOrigin(t *testing.T) {
	config, err := newCORSConfig(true, "https://app.example", defaultCORSAllowedHeaders)
	if err != nil {
		t.Fatal(err)
	}
	handler := config.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("preflight reached application handler")
	}))

	request := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	request.Header.Set("Origin", "https://unknown.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS header for rejected origin: %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSLeavesNativeRequestsWithoutOriginUnchanged(t *testing.T) {
	config, err := newCORSConfig(true, "https://app.example", defaultCORSAllowedHeaders)
	if err != nil {
		t.Fatal(err)
	}
	handler := config.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	request := httptest.NewRequest(http.MethodPost, "/ors/v2/directions/driving-car", strings.NewReader(`{"coordinates":[]}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS header for native request: %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSRejectsWildcardOriginConfiguration(t *testing.T) {
	if _, err := newCORSConfig(true, "*", defaultCORSAllowedHeaders); err == nil {
		t.Fatal("expected wildcard origin configuration to be rejected")
	}
}

func TestCORSCanBeDisabled(t *testing.T) {
	config, err := newCORSConfig(false, "https://app.example", defaultCORSAllowedHeaders)
	if err != nil {
		t.Fatal(err)
	}
	handler := config.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	request := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS header while disabled: %q", recorder.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSEnablingRequiresAnOrigin(t *testing.T) {
	if _, err := newCORSConfig(true, "", defaultCORSAllowedHeaders); err == nil {
		t.Fatal("expected enabled CORS without origins to be rejected")
	}
}
