package main

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestParseRouteRequest(t *testing.T) {
	request := httptest.NewRequest("GET", "/ors/v2/directions/driving-car?start=121.056,14.676&end=121.058,14.678&geometry_format=geojson&instructions=false", nil)
	parsed, err := parseRouteRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Coordinates) != 2 || parsed.Coordinates[0].Longitude != 121.056 || parsed.Coordinates[1].Latitude != 14.678 {
		t.Fatalf("unexpected coordinates: %#v", parsed.Coordinates)
	}
	if parsed.GeometryFormat != "geojson" || parsed.Instructions {
		t.Fatalf("unexpected options: %#v", parsed)
	}
}

func TestParseRouteRequestRejectsUnsupportedParameter(t *testing.T) {
	request := httptest.NewRequest("GET", "/ors/v2/directions/driving-car?start=121.056,14.676&end=121.058,14.678&preference=fastest", nil)
	if _, err := parseRouteRequest(request); err == nil {
		t.Fatal("expected unsupported parameter error")
	}
}

func TestBuildOSRMURL(t *testing.T) {
	base, err := url.Parse("http://osrm:5000")
	if err != nil {
		t.Fatal(err)
	}
	request := routeRequest{
		Coordinates:    []coordinate{{Longitude: 121.056, Latitude: 14.676}, {Longitude: 121.058, Latitude: 14.678}},
		GeometryFormat: "geojson",
	}
	result := buildOSRMURL(base, request)
	if result.Path != "/route/v1/driving/121.056,14.676;121.058,14.678" {
		t.Fatalf("unexpected path: %s", result.Path)
	}
	if result.Query().Get("overview") != "full" || result.Query().Get("geometries") != "geojson" || result.Query().Get("steps") != "false" {
		t.Fatalf("unexpected query: %s", result.RawQuery)
	}
}

func TestTranslateRoute(t *testing.T) {
	request := routeRequest{
		Coordinates: []coordinate{{Longitude: 121.056, Latitude: 14.676}, {Longitude: 121.058, Latitude: 14.678}},
	}
	route := OSRMRoute{
		Distance: 396.3,
		Duration: 59.1,
		Geometry: OSRMGeometry{
			Type:        "LineString",
			Coordinates: [][]float64{{121.055795, 14.676158}, {121.058004, 14.67805}},
		},
		Legs: []OSRMLeg{{Distance: 396.3, Duration: 59.1, Steps: []OSRMStep{}}},
	}
	translated, err := translateRoute(request, route)
	if err != nil {
		t.Fatal(err)
	}
	if translated.Type != "FeatureCollection" || len(translated.Features) != 1 {
		t.Fatalf("unexpected feature collection: %#v", translated)
	}
	if translated.Features[0].Properties.Summary.Distance != 396.3 {
		t.Fatalf("unexpected summary: %#v", translated.Features[0].Properties.Summary)
	}
	if len(translated.Features[0].Properties.Segments[0].Steps) != 0 {
		t.Fatal("expected empty steps")
	}
}
