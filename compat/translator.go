package main

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type coordinate struct {
	Longitude float64
	Latitude  float64
}

type routeRequest struct {
	Coordinates    []coordinate
	GeometryFormat string
	Instructions   bool
}

type OSRMResponse struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Routes    []OSRMRoute    `json:"routes"`
	Waypoints []OSRMWaypoint `json:"waypoints"`
}

type OSRMRoute struct {
	Distance   float64      `json:"distance"`
	Duration   float64      `json:"duration"`
	Weight     float64      `json:"weight"`
	WeightName string       `json:"weight_name"`
	Geometry   OSRMGeometry `json:"geometry"`
	Legs       []OSRMLeg    `json:"legs"`
}

type OSRMLeg struct {
	Distance float64    `json:"distance"`
	Duration float64    `json:"duration"`
	Weight   float64    `json:"weight"`
	Summary  string     `json:"summary"`
	Steps    []OSRMStep `json:"steps"`
}

type OSRMStep struct {
	Distance float64      `json:"distance"`
	Duration float64      `json:"duration"`
	Name     string       `json:"name"`
	Maneuver OSRMManeuver `json:"maneuver"`
}

type OSRMManeuver struct {
	Type     string    `json:"type"`
	Modifier string    `json:"modifier"`
	Location []float64 `json:"location"`
}

type OSRMWaypoint struct {
	Location []float64 `json:"location"`
	Name     string    `json:"name"`
	Distance float64   `json:"distance"`
}

type OSRMGeometry struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}

type ORSFeatureCollection struct {
	Type     string       `json:"type"`
	BBox     []float64    `json:"bbox"`
	Features []ORSFeature `json:"features"`
	Metadata ORSMetadata  `json:"metadata"`
}

type ORSFeature struct {
	Type       string        `json:"type"`
	BBox       []float64     `json:"bbox"`
	Properties ORSProperties `json:"properties"`
	Geometry   OSRMGeometry  `json:"geometry"`
}

type ORSProperties struct {
	Segments  []ORSSegment `json:"segments"`
	Summary   ORSSummary   `json:"summary"`
	WayPoints []int        `json:"way_points"`
}

type ORSSegment struct {
	Distance float64   `json:"distance"`
	Duration float64   `json:"duration"`
	Steps    []ORSStep `json:"steps"`
}

type ORSStep struct {
	Distance    float64 `json:"distance"`
	Duration    float64 `json:"duration"`
	Type        int     `json:"type"`
	Instruction string  `json:"instruction"`
	Name        string  `json:"name"`
	WayPoints   []int   `json:"way_points,omitempty"`
}

type ORSSummary struct {
	Distance float64 `json:"distance"`
	Duration float64 `json:"duration"`
}

type ORSMetadata struct {
	Attribution string    `json:"attribution"`
	Service     string    `json:"service"`
	Timestamp   int64     `json:"timestamp"`
	Query       ORSQuery  `json:"query"`
	Engine      ORSEngine `json:"engine"`
}

type ORSQuery struct {
	Coordinates [][]float64 `json:"coordinates"`
	Profile     string      `json:"profile"`
	ProfileName string      `json:"profileName"`
	Format      string      `json:"format"`
}

type ORSEngine struct {
	Version   string `json:"version"`
	BuildDate string `json:"build_date"`
	GraphDate string `json:"graph_date"`
	OSMDate   string `json:"osm_date"`
}

func parseRouteRequest(r *http.Request) (routeRequest, error) {
	query := r.URL.Query()
	for key := range query {
		switch key {
		case "start", "end", "geometry_format", "instructions":
		default:
			return routeRequest{}, fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	start, err := parseCoordinate(query.Get("start"))
	if err != nil {
		return routeRequest{}, fmt.Errorf("invalid start: %w", err)
	}
	end, err := parseCoordinate(query.Get("end"))
	if err != nil {
		return routeRequest{}, fmt.Errorf("invalid end: %w", err)
	}
	geometryFormat := query.Get("geometry_format")
	if geometryFormat == "" {
		geometryFormat = "geojson"
	}
	if geometryFormat != "geojson" {
		return routeRequest{}, fmt.Errorf("geometry_format %q is unsupported; use geojson", geometryFormat)
	}
	instructions := false
	if raw := query.Get("instructions"); raw != "" {
		instructions, err = strconv.ParseBool(raw)
		if err != nil {
			return routeRequest{}, fmt.Errorf("invalid instructions value %q", raw)
		}
	}
	return routeRequest{
		Coordinates:    []coordinate{start, end},
		GeometryFormat: geometryFormat,
		Instructions:   instructions,
	}, nil
}

func parseCoordinate(raw string) (coordinate, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return coordinate{}, fmt.Errorf("expected longitude,latitude")
	}
	longitude, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil || math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
		return coordinate{}, fmt.Errorf("invalid longitude")
	}
	latitude, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil || math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
		return coordinate{}, fmt.Errorf("invalid latitude")
	}
	return coordinate{Longitude: longitude, Latitude: latitude}, nil
}

func buildOSRMURL(base *url.URL, request routeRequest) *url.URL {
	result := *base
	coordinates := make([]string, len(request.Coordinates))
	for index, point := range request.Coordinates {
		coordinates[index] = strconv.FormatFloat(point.Longitude, 'f', -1, 64) + "," + strconv.FormatFloat(point.Latitude, 'f', -1, 64)
	}
	result.Path = strings.TrimRight(base.Path, "/") + "/route/v1/driving/" + strings.Join(coordinates, ";")
	result.RawPath = ""
	result.RawQuery = url.Values{
		"overview":   []string{"full"},
		"geometries": []string{"geojson"},
		"steps":      []string{strconv.FormatBool(request.Instructions)},
	}.Encode()
	return &result
}

func translateRoute(request routeRequest, route OSRMRoute) (ORSFeatureCollection, error) {
	if route.Geometry.Type != "LineString" || len(route.Geometry.Coordinates) < 2 {
		return ORSFeatureCollection{}, fmt.Errorf("OSRM route has no usable geometry")
	}
	bbox := geometryBBox(route.Geometry.Coordinates)
	segments := make([]ORSSegment, len(route.Legs))
	for index, leg := range route.Legs {
		segments[index] = ORSSegment{
			Distance: leg.Distance,
			Duration: leg.Duration,
			Steps:    translateSteps(leg.Steps),
		}
	}
	if len(segments) == 0 {
		segments = []ORSSegment{{
			Distance: route.Distance,
			Duration: route.Duration,
			Steps:    []ORSStep{},
		}}
	}
	coordinates := make([][]float64, len(request.Coordinates))
	for index, point := range request.Coordinates {
		coordinates[index] = []float64{point.Longitude, point.Latitude}
	}
	return ORSFeatureCollection{
		Type: "FeatureCollection",
		BBox: bbox,
		Features: []ORSFeature{{
			Type:     "Feature",
			BBox:     bbox,
			Geometry: route.Geometry,
			Properties: ORSProperties{
				Segments:  segments,
				Summary:   ORSSummary{Distance: route.Distance, Duration: route.Duration},
				WayPoints: []int{0, len(route.Geometry.Coordinates) - 1},
			},
		}},
		Metadata: ORSMetadata{
			Attribution: "OpenStreetMap contributors",
			Service:     "routing",
			Timestamp:   timeNowMillis(),
			Query: ORSQuery{
				Coordinates: coordinates,
				Profile:     "driving-car",
				ProfileName: "driving-car",
				Format:      "json",
			},
			Engine: ORSEngine{Version: "roadway-ors-compat"},
		},
	}, nil
}

func translateSteps(steps []OSRMStep) []ORSStep {
	translated := make([]ORSStep, 0, len(steps))
	for _, step := range steps {
		translated = append(translated, ORSStep{
			Distance:    step.Distance,
			Duration:    step.Duration,
			Type:        orsStepType(step.Maneuver.Type),
			Instruction: orsInstruction(step.Maneuver.Type, step.Maneuver.Modifier),
			Name:        step.Name,
		})
	}
	return translated
}

func orsStepType(maneuverType string) int {
	switch maneuverType {
	case "depart":
		return 11
	case "arrive":
		return 10
	case "turn", "new name", "continue", "merge", "on ramp", "off ramp", "fork", "roundabout", "exit roundabout":
		return 0
	default:
		return 0
	}
}

func orsInstruction(maneuverType, modifier string) string {
	switch maneuverType {
	case "depart":
		return "Depart"
	case "arrive":
		return "Arrive"
	case "turn":
		if modifier != "" {
			return "Turn " + strings.ReplaceAll(modifier, "-", " ")
		}
		return "Turn"
	default:
		return strings.Title(strings.ReplaceAll(maneuverType, "-", " "))
	}
}

func geometryBBox(coordinates [][]float64) []float64 {
	minLongitude, minLatitude := coordinates[0][0], coordinates[0][1]
	maxLongitude, maxLatitude := minLongitude, minLatitude
	for _, point := range coordinates[1:] {
		if point[0] < minLongitude {
			minLongitude = point[0]
		}
		if point[1] < minLatitude {
			minLatitude = point[1]
		}
		if point[0] > maxLongitude {
			maxLongitude = point[0]
		}
		if point[1] > maxLatitude {
			maxLatitude = point[1]
		}
	}
	return []float64{minLongitude, minLatitude, maxLongitude, maxLatitude}
}

func timeNowMillis() int64 {
	return time.Now().UnixMilli()
}
