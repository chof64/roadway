package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var supportedProfiles = []string{"driving-car", "cycling-electric"}

func profileFromPath(path string) string {
	for _, profile := range supportedProfiles {
		if strings.HasSuffix(path, "/"+profile) {
			return profile
		}
	}
	return "driving-car"
}

type coordinate struct {
	Longitude float64
	Latitude  float64
}

type routeRequest struct {
	Coordinates    []coordinate
	Profile        string
	GeometryFormat string
	Instructions   bool
}

type matrixRequest struct {
	Profile      string
	Locations    []coordinate
	Metrics      []string
	Sources      []int
	Destinations []int
}

type snapRequest struct {
	Locations []coordinate
	Radius    float64
}

type OSRMResponse struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Routes    []OSRMRoute    `json:"routes"`
	Waypoints []OSRMWaypoint `json:"waypoints"`
}

type OSRMTableResponse struct {
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	Durations [][]*float64 `json:"durations"`
	Distances [][]*float64 `json:"distances"`
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
	Routes   []ORSRoute   `json:"routes,omitempty"`
}

type ORSRoute struct {
	Distance float64    `json:"distance"`
	Duration float64    `json:"duration"`
	Geometry string     `json:"geometry"`
	Summary  ORSSummary `json:"summary"`
}

type ORSSnapResponse struct {
	Locations []ORSSnapLocation `json:"locations"`
}

type ORSSnapLocation struct {
	Location        any     `json:"location"`
	Name            string  `json:"name,omitempty"`
	SnappedDistance float64 `json:"snapped_distance,omitempty"`
}

type orsDirectionsPayload struct {
	Coordinates    [][]float64 `json:"coordinates"`
	GeometryFormat string      `json:"geometry_format"`
	Instructions   *bool       `json:"instructions"`
	Preference     string      `json:"preference"`
}

type orsMatrixPayload struct {
	Locations    [][]float64 `json:"locations"`
	Metrics      []string    `json:"metrics"`
	Sources      []int       `json:"sources"`
	Destinations []int       `json:"destinations"`
}

type orsSnapPayload struct {
	Locations [][]float64 `json:"locations"`
	Radius    float64     `json:"radius"`
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
	switch r.Method {
	case http.MethodGet:
		return parseRouteQuery(r.URL.Query())
	case http.MethodPost:
		return parseRouteJSON(r)
	default:
		return routeRequest{}, fmt.Errorf("method %s is unsupported", r.Method)
	}
}

func parseRouteQuery(query url.Values) (routeRequest, error) {
	for key := range query {
		switch key {
		case "start", "end", "geometry_format", "instructions", "preference":
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
	return routeRequestFromCoordinates(
		[]coordinate{start, end},
		query.Get("geometry_format"),
		query.Get("instructions"),
		query.Get("preference"),
	)
}

func parseRouteJSON(r *http.Request) (routeRequest, error) {
	var payload orsDirectionsPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		return routeRequest{}, fmt.Errorf("invalid directions body: %w", err)
	}
	coordinates, err := parseCoordinateList(payload.Coordinates, "coordinates")
	if err != nil {
		return routeRequest{}, err
	}
	instructions := ""
	if payload.Instructions != nil {
		instructions = strconv.FormatBool(*payload.Instructions)
	}
	return routeRequestFromCoordinates(coordinates, payload.GeometryFormat, instructions, payload.Preference)
}

func routeRequestFromCoordinates(coordinates []coordinate, geometryFormat, instructions, preference string) (routeRequest, error) {
	if len(coordinates) < 2 {
		return routeRequest{}, fmt.Errorf("at least 2 coordinates are required")
	}
	if geometryFormat == "" {
		geometryFormat = "geojson"
	}
	if geometryFormat != "geojson" {
		return routeRequest{}, fmt.Errorf("geometry_format %q is unsupported; use geojson", geometryFormat)
	}
	if preference != "" && preference != "fastest" {
		return routeRequest{}, fmt.Errorf("preference %q is unsupported; use fastest", preference)
	}
	parsedInstructions := false
	if instructions != "" {
		var err error
		parsedInstructions, err = strconv.ParseBool(instructions)
		if err != nil {
			return routeRequest{}, fmt.Errorf("invalid instructions value %q", instructions)
		}
	}
	return routeRequest{
		Coordinates:    coordinates,
		GeometryFormat: geometryFormat,
		Instructions:   parsedInstructions,
	}, nil
}

func parseMatrixRequest(r *http.Request, profile string) (matrixRequest, error) {
	if r.Method != http.MethodPost {
		return matrixRequest{}, fmt.Errorf("method %s is unsupported", r.Method)
	}
	var payload orsMatrixPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		return matrixRequest{}, fmt.Errorf("invalid matrix body: %w", err)
	}
	locations, err := parseCoordinateList(payload.Locations, "locations")
	if err != nil {
		return matrixRequest{}, err
	}
	if len(locations) < 2 {
		return matrixRequest{}, fmt.Errorf("at least 2 locations are required")
	}
	metrics := payload.Metrics
	if len(metrics) == 0 {
		metrics = []string{"distance", "duration"}
	}
	for _, metric := range metrics {
		if metric != "distance" && metric != "duration" {
			return matrixRequest{}, fmt.Errorf("metric %q is unsupported", metric)
		}
	}
	if err := validateIndices(payload.Sources, len(locations), "sources"); err != nil {
		return matrixRequest{}, err
	}
	if err := validateIndices(payload.Destinations, len(locations), "destinations"); err != nil {
		return matrixRequest{}, err
	}
	return matrixRequest{
		Profile:      profile,
		Locations:    locations,
		Metrics:      metrics,
		Sources:      payload.Sources,
		Destinations: payload.Destinations,
	}, nil
}

func parseSnapRequest(r *http.Request) (snapRequest, error) {
	if r.Method != http.MethodPost {
		return snapRequest{}, fmt.Errorf("method %s is unsupported", r.Method)
	}
	var payload orsSnapPayload
	if err := decodeJSONBody(r, &payload); err != nil {
		return snapRequest{}, fmt.Errorf("invalid snap body: %w", err)
	}
	locations, err := parseCoordinateList(payload.Locations, "locations")
	if err != nil {
		return snapRequest{}, err
	}
	if len(locations) == 0 {
		return snapRequest{}, fmt.Errorf("at least 1 location is required")
	}
	radius := payload.Radius
	if radius == 0 {
		radius = 350
	}
	if radius < 0 || math.IsInf(radius, 0) || math.IsNaN(radius) {
		return snapRequest{}, fmt.Errorf("radius must be a non-negative number")
	}
	return snapRequest{Locations: locations, Radius: radius}, nil
}

func decodeJSONBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxUpstreamResponseBytes))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func parseCoordinateList(raw [][]float64, label string) ([]coordinate, error) {
	coordinates := make([]coordinate, len(raw))
	for index, point := range raw {
		if len(point) != 2 {
			return nil, fmt.Errorf("%s[%d] must be [longitude, latitude]", label, index)
		}
		parsed, err := parseCoordinate(
			strconv.FormatFloat(point[0], 'f', -1, 64) + "," + strconv.FormatFloat(point[1], 'f', -1, 64),
		)
		if err != nil {
			return nil, fmt.Errorf("invalid %s[%d]: %w", label, index, err)
		}
		coordinates[index] = parsed
	}
	return coordinates, nil
}

func validateIndices(indices []int, count int, label string) error {
	for _, index := range indices {
		if index < 0 || index >= count {
			return fmt.Errorf("%s index %d is out of range", label, index)
		}
	}
	return nil
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

func buildTableURL(base *url.URL, request matrixRequest) *url.URL {
	result := *base
	coordinates := make([]string, len(request.Locations))
	for index, point := range request.Locations {
		coordinates[index] = strconv.FormatFloat(point.Longitude, 'f', -1, 64) + "," + strconv.FormatFloat(point.Latitude, 'f', -1, 64)
	}
	query := url.Values{"annotations": []string{strings.Join(request.Metrics, ",")}}
	if len(request.Sources) > 0 {
		query.Set("sources", joinIndices(request.Sources))
	}
	if len(request.Destinations) > 0 {
		query.Set("destinations", joinIndices(request.Destinations))
	}
	result.Path = strings.TrimRight(base.Path, "/") + "/table/v1/driving/" + strings.Join(coordinates, ";")
	result.RawPath = ""
	result.RawQuery = query.Encode()
	return &result
}

func buildNearestURL(base *url.URL, point coordinate) *url.URL {
	result := *base
	coordinates := strconv.FormatFloat(point.Longitude, 'f', -1, 64) + "," + strconv.FormatFloat(point.Latitude, 'f', -1, 64)
	result.Path = strings.TrimRight(base.Path, "/") + "/nearest/v1/driving/" + coordinates
	result.RawPath = ""
	result.RawQuery = url.Values{"number": []string{"1"}}.Encode()
	return &result
}

func joinIndices(indices []int) string {
	values := make([]string, len(indices))
	for index, value := range indices {
		values[index] = strconv.Itoa(value)
	}
	return strings.Join(values, ";")
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
	encodedGeometry := encodePolyline(route.Geometry.Coordinates)
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
				Profile:     request.Profile,
				ProfileName: request.Profile,
				Format:      "json",
			},
			Engine: ORSEngine{Version: "roadway-ors-compat"},
		},
		Routes: []ORSRoute{{
			Distance: route.Distance,
			Duration: route.Duration,
			Geometry: encodedGeometry,
			Summary:  ORSSummary{Distance: route.Distance, Duration: route.Duration},
		}},
	}, nil
}

func translateMatrix(response OSRMTableResponse, request matrixRequest) map[string]any {
	result := make(map[string]any, 2)
	for _, metric := range request.Metrics {
		switch metric {
		case "distance":
			result["distances"] = response.Distances
		case "duration":
			result["durations"] = response.Durations
		}
	}
	return result
}

func translateSnap(response OSRMResponse) (ORSSnapResponse, error) {
	locations := make([]ORSSnapLocation, len(response.Waypoints))
	for index, waypoint := range response.Waypoints {
		locations[index] = ORSSnapLocation{
			Name:            waypoint.Name,
			SnappedDistance: waypoint.Distance,
		}
		if len(waypoint.Location) == 2 {
			locations[index].Location = waypoint.Location
		}
	}
	return ORSSnapResponse{Locations: locations}, nil
}

func encodePolyline(coordinates [][]float64) string {
	var builder strings.Builder
	lastLatitude, lastLongitude := 0, 0
	for _, point := range coordinates {
		latitude := int(math.Round(point[1] * 100000))
		longitude := int(math.Round(point[0] * 100000))
		encodePolylineValue(&builder, latitude-lastLatitude)
		encodePolylineValue(&builder, longitude-lastLongitude)
		lastLatitude, lastLongitude = latitude, longitude
	}
	return builder.String()
}

func encodePolylineValue(builder *strings.Builder, value int) {
	value <<= 1
	if value < 0 {
		value = ^value
	}
	for value >= 0x20 {
		builder.WriteByte(byte((0x20 | (value & 0x1f)) + 63))
		value >>= 5
	}
	builder.WriteByte(byte(value + 63))
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
