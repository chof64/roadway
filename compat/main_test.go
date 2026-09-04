package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDirectionsHandlerTranslatesUpstreamRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/v1/driving/121.056,14.676;121.058,14.678" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("overview") != "full" || r.URL.Query().Get("geometries") != "geojson" || r.URL.Query().Get("steps") != "false" {
			t.Fatalf("unexpected upstream query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OSRMResponse{
			Code: "Ok",
			Routes: []OSRMRoute{{
				Distance: 396.3,
				Duration: 59.1,
				Geometry: OSRMGeometry{
					Type:        "LineString",
					Coordinates: [][]float64{{121.055795, 14.676158}, {121.058004, 14.67805}},
				},
				Legs: []OSRMLeg{{Distance: 396.3, Duration: 59.1, Steps: []OSRMStep{}}},
			}},
		})
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{upstream: upstreamURL, client: upstream.Client()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ors/v2/directions/driving-car?start=121.056,14.676&end=121.058,14.678&geometry_format=geojson&instructions=false", nil)
	app.directionsHandler(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Type") != "application/geo+json; charset=UTF-8" {
		t.Fatalf("unexpected content type: %s", recorder.Header().Get("Content-Type"))
	}
	var response ORSFeatureCollection
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "FeatureCollection" || response.Features[0].Properties.Summary.Distance != 396.3 {
		t.Fatalf("unexpected translated response: %#v", response)
	}
}

func TestORSHealthEndpointReportsOSRMReadiness(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/v1/driving/121.056,14.676;121.058,14.678" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{upstream: upstreamURL, client: upstream.Client()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ors/v2/health", nil)
	newHandler(app).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != "ready" {
		t.Fatalf("unexpected health response: %#v", response)
	}
}

func TestRequestLoggerLogsRequestDetails(t *testing.T) {
	var logs bytes.Buffer
	accessLogger := newAsyncAccessLogger(log.New(&logs, "", 0))
	t.Cleanup(accessLogger.shutdown)

	handler := requestLoggerWithAccessLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}), accessLogger)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ors/v2/health?probe=1", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	request.Header.Set("User-Agent", "health-check")
	request.Header.Set("X-Request-ID", "request-123")
	handler.ServeHTTP(recorder, request)
	accessLogger.shutdown()

	if recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
	for _, expected := range []string{
		"request method=GET",
		`path="/ors/v2/health"`,
		`query="probe=1"`,
		"status=201",
		"bytes=2",
		`remote="192.0.2.1:1234"`,
		`user_agent="health-check"`,
		`request_id="request-123"`,
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Fatalf("log does not contain %q: %s", expected, logs.String())
		}
	}
}

func TestDirectionsHandlerRejectsUnsupportedInput(t *testing.T) {
	app := &application{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ors/v2/directions/driving-car?start=121.056,14.676&end=121.058,14.678&preference=shortest", nil)
	app.directionsHandler(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
}

func TestDirectionsHandlerTranslatesPostAndMapsCyclingProfileToDrivingOSRM(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/route/v1/driving/121.056,14.676;121.058,14.678" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("steps") != "false" {
			t.Fatalf("unexpected upstream query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(OSRMResponse{
			Code: "Ok",
			Routes: []OSRMRoute{{
				Distance: 400,
				Duration: 60,
				Geometry: OSRMGeometry{
					Type:        "LineString",
					Coordinates: [][]float64{{121.056, 14.676}, {121.058, 14.678}},
				},
			}},
		})
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{upstream: upstreamURL, client: upstream.Client()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/ors/v2/directions/cycling-electric",
		bytes.NewBufferString(`{"coordinates":[[121.056,14.676],[121.058,14.678]],"instructions":false}`),
	)
	request.Header.Set("Content-Type", "application/json")
	app.directionsHandler(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", recorder.Code, recorder.Body.String())
	}
	var response ORSFeatureCollection
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Metadata.Query.Profile != "cycling-electric" {
		t.Fatalf("unexpected profile metadata: %#v", response.Metadata.Query)
	}
	if len(response.Routes) != 1 || response.Routes[0].Geometry == "" {
		t.Fatalf("expected legacy route compatibility shape: %#v", response.Routes)
	}
}

func TestMatrixHandlerTranslatesORSRequestToOSRMTable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/table/v1/driving/121.056,14.676;121.058,14.678" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("annotations") != "distance,duration" || r.URL.Query().Get("sources") != "0" || r.URL.Query().Get("destinations") != "1" {
			t.Fatalf("unexpected upstream query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(OSRMTableResponse{
			Code:      "Ok",
			Durations: [][]*float64{{nil, float64Ptr(60)}, {float64Ptr(60), nil}},
			Distances: [][]*float64{{nil, float64Ptr(400)}, {float64Ptr(400), nil}},
		})
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{upstream: upstreamURL, client: upstream.Client()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/ors/v2/matrix/cycling-electric",
		bytes.NewBufferString(`{"locations":[[121.056,14.676],[121.058,14.678]],"metrics":["distance","duration"],"sources":[0],"destinations":[1]}`),
	)
	app.matrixHandler(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["distances"] == nil || response["durations"] == nil {
		t.Fatalf("missing matrix metrics: %#v", response)
	}
}

func TestSnapHandlerTranslatesORSRequestToOSRMNearest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nearest/v1/driving/121.056,14.676" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(OSRMResponse{
			Code: "Ok",
			Waypoints: []OSRMWaypoint{{
				Location: []float64{121.0561, 14.6761},
				Name:     "Example Road",
				Distance: 12,
			}},
		})
	}))
	defer upstream.Close()

	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	app := &application{upstream: upstreamURL, client: upstream.Client()}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/ors/v2/snap/driving-car",
		bytes.NewBufferString(`{"locations":[[121.056,14.676]],"radius":100}`),
	)
	app.snapHandler(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body: %s", recorder.Code, recorder.Body.String())
	}
	var response ORSSnapResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Locations) != 1 || response.Locations[0].Name != "Example Road" {
		t.Fatalf("unexpected snap response: %#v", response)
	}
}

func float64Ptr(value float64) *float64 {
	return &value
}
