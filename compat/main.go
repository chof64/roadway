package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

const maxUpstreamResponseBytes = 16 << 20

type application struct {
	upstream *url.URL
	client   *http.Client
}

func main() {
	upstream, err := parseUpstreamURL(getenv("OSRM_UPSTREAM_URL", "http://osrm:80"))
	if err != nil {
		log.Fatalf("invalid OSRM_UPSTREAM_URL: %v", err)
	}

	app := &application{
		upstream: upstream,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", app.readyHandler)
	for _, profile := range supportedProfiles {
		mux.HandleFunc("/ors/v2/directions/"+profile, app.directionsHandler)
		mux.HandleFunc("/ors/v2/matrix/"+profile, app.matrixHandler)
		mux.HandleFunc("/ors/v2/snap/"+profile, app.snapHandler)
	}

	server := &http.Server{
		Addr:              getenv("LISTEN_ADDR", ":80"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("listening on %s, OSRM upstream %s", server.Addr, upstream.String())
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseUpstreamURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("host is required")
	}
	return parsed, nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"}, "application/json")
}

func (app *application) readyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	probe := routeRequest{
		Coordinates:    []coordinate{{Longitude: 121.056, Latitude: 14.676}, {Longitude: 121.058, Latitude: 14.678}},
		GeometryFormat: "geojson",
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, buildOSRMURL(app.upstream, probe).String(), nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "OSRM upstream is unavailable")
		return
	}
	response, err := app.client.Do(request)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "OSRM upstream is unavailable")
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "OSRM upstream is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"}, "application/json")
}

func (app *application) directionsHandler(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	parsed, err := parseRouteRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}
	parsed.Profile = profileFromPath(r.URL.Path)

	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, buildOSRMURL(app.upstream, parsed).String(), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "failed to build OSRM request")
		return
	}
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		upstreamRequest.Header.Set("X-Request-ID", requestID)
	}
	response, err := app.client.Do(upstreamRequest)
	if err != nil {
		log.Printf("route upstream_error=%q duration_ms=%d", err, time.Since(started).Milliseconds())
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "OSRM upstream request failed")
		return
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamResponseBytes+1))
	if err != nil || len(body) > maxUpstreamResponseBytes {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "invalid OSRM upstream response")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, response.StatusCode, "OSRM upstream returned an error")
		return
	}

	var upstream OSRMResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "invalid OSRM upstream JSON")
		return
	}
	if upstream.Code != "Ok" {
		status := http.StatusBadGateway
		if upstream.Code == "InvalidQuery" || upstream.Code == "NoRoute" {
			status = http.StatusBadRequest
		}
		message := upstream.Message
		if message == "" {
			message = "OSRM route request failed"
		}
		writeError(w, status, status, message)
		return
	}
	if len(upstream.Routes) == 0 {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "OSRM returned no routes")
		return
	}

	translated, err := translateRoute(parsed, upstream.Routes[0])
	if err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, err.Error())
		return
	}
	log.Printf("route status=200 duration_ms=%d", time.Since(started).Milliseconds())
	writeJSON(w, http.StatusOK, translated, "application/geo+json; charset=UTF-8")
}

func (app *application) matrixHandler(w http.ResponseWriter, r *http.Request) {
	profile := profileFromPath(r.URL.Path)
	parsed, err := parseMatrixRequest(r, profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}

	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, buildTableURL(app.upstream, parsed).String(), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "failed to build OSRM table request")
		return
	}
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		upstreamRequest.Header.Set("X-Request-ID", requestID)
	}
	status, body, err := app.fetchUpstream(upstreamRequest)
	if err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "OSRM upstream request failed")
		return
	}
	if status < 200 || status >= 300 {
		writeError(w, http.StatusBadGateway, status, "OSRM upstream returned an error")
		return
	}

	var upstream OSRMTableResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, "invalid OSRM upstream JSON")
		return
	}
	if upstream.Code != "Ok" {
		message := upstream.Message
		if message == "" {
			message = "OSRM table request failed"
		}
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, message)
		return
	}
	writeJSON(w, http.StatusOK, translateMatrix(upstream, parsed), "application/json")
}

func (app *application) snapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	parsed, err := parseSnapRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}

	waypoints := make([]OSRMWaypoint, len(parsed.Locations))
	for index, location := range parsed.Locations {
		upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodGet, buildNearestURL(app.upstream, location).String(), nil)
		if err != nil {
			writeError(w, http.StatusBadGateway, http.StatusBadGateway, "failed to build OSRM nearest request")
			return
		}
		if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
			upstreamRequest.Header.Set("X-Request-ID", requestID)
		}
		status, body, err := app.fetchUpstream(upstreamRequest)
		if err != nil {
			writeError(w, http.StatusBadGateway, http.StatusBadGateway, "OSRM upstream request failed")
			return
		}
		if status < 200 || status >= 300 {
			writeError(w, http.StatusBadGateway, status, "OSRM upstream returned an error")
			return
		}

		var upstream OSRMResponse
		if err := json.Unmarshal(body, &upstream); err != nil {
			writeError(w, http.StatusBadGateway, http.StatusBadGateway, "invalid OSRM upstream JSON")
			return
		}
		if upstream.Code != "Ok" || len(upstream.Waypoints) == 0 {
			message := upstream.Message
			if message == "" {
				message = "OSRM nearest request failed"
			}
			writeError(w, http.StatusBadGateway, http.StatusBadGateway, message)
			return
		}
		waypoint := upstream.Waypoints[0]
		if waypoint.Distance > parsed.Radius {
			waypoint.Location = nil
		}
		waypoints[index] = waypoint
	}

	translated, err := translateSnap(OSRMResponse{Waypoints: waypoints})
	if err != nil {
		writeError(w, http.StatusBadGateway, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, translated, "application/json")
}

func (app *application) fetchUpstream(request *http.Request) (int, []byte, error) {
	response, err := app.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxUpstreamResponseBytes+1))
	if err != nil || len(body) > maxUpstreamResponseBytes {
		return 0, nil, fmt.Errorf("invalid upstream response")
	}
	return response.StatusCode, body, nil
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}, "application/json")
}

func writeJSON(w http.ResponseWriter, status int, value any, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
