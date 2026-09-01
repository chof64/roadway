package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestDirectionsHandlerRejectsUnsupportedInput(t *testing.T) {
	app := &application{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/ors/v2/directions/driving-car?start=121.056,14.676&end=121.058,14.678&preference=fastest", nil)
	app.directionsHandler(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
}
