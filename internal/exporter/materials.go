package exporter

import "strings"

// Material describes read-only routes; no route is built from arbitrary user input.
type Material struct {
	Type, Collection, Singular, Batch, Field, Folder string
	SnapshotOnly                                     bool
}

var Materials = []Material{
	{"WORKSTREAM_SUMMARIES", "/workstream_summaries", "/workstream_summary/", "/workstream_summaries/batch", "workstreamSummaries", "summaries", false},
	{"WORKSTREAM_EVENTS", "/workstream_events", "/workstream_event/", "/workstream_events/batch/fetch", "workstreamEvents", "events", false},
	{"TAGS", "/tags", "/tag/", "/tags/batch/fetch", "tags", "tags", false},
	{"ANNOTATIONS", "/annotations", "/annotation/", "/annotations/batch/fetch", "annotations", "annotations", false},
	{"PERSONS", "/persons", "/person/", "/persons/batch/fetch", "persons", "persons", false},
	{"CONVERSATIONS", "/conversations", "/conversation/", "/conversations/batch/fetch", "conversations", "conversations", false},
	{"CONVERSATION_MESSAGES", "/messages", "/message/", "/messages/batch/fetch", "conversationMessages", "messages", false},
	{"ASSETS", "/assets", "/asset/", "/assets/batch/fetch", "assets", "assets", false},
	{"FORMATS", "/formats", "/format/", "/formats/batch/fetch", "formats", "formats", false},
	{"ANCHORS", "/anchors", "/anchor/", "/anchors/batch/fetch", "anchors", "anchors", false},
	{"ANCHOR_POINTS", "/anchor_points", "/anchor_point/", "/anchor_points/batch/fetch", "anchorPoints", "anchor_points", false},
	{"WEBSITES", "/websites", "/website/", "/websites/batch/fetch", "websites", "websites", false},
	{"RANGES", "/ranges", "/range/", "/ranges/batch/fetch", "ranges", "ranges", false},
	{"HINTS", "/hints", "/hint/", "/hints/batch/fetch", "hints", "hints", false},
	{"WORKSTREAM_PATTERN_ENGINE_SOURCES", "/workstream_pattern_engine/sources", "/workstream_pattern_engine/source/", "/workstream_pattern_engine/sources/batch/fetch", "identifiedWorkstreamPatternEngineSources", "sources", false},
	{"WORKSTREAM_PATTERN_ENGINE_SOURCE_WINDOWS", "/workstream_pattern_engine/source_windows", "/workstream_pattern_engine/source_window/", "/workstream_pattern_engine/source_windows/batch/fetch", "workstreamPatternEngineSourceWindows", "source_windows", false},
	{"WORKSTREAM_PATTERN_ENGINE_OBSERVERS", "/workstream_pattern_engine_observers", "/workstream_pattern_engine_observer/", "", "", "observers", true},
	{"CONNECTORS", "/connectors", "/connector/", "/connectors/batch/fetch", "connectors", "connectors", true},
	{"PIPELINES", "/pipelines", "/pipeline/", "/pipelines/batch/fetch", "pipelines", "pipelines", false},
	{"SCHEDULES", "/schedules", "/schedule/", "/schedules/batch/fetch", "schedules", "schedules", false},
	{"SIGNALS", "/signals", "/signal/", "/signals/batch/fetch", "signals", "signals", false},
	{"FINGERPRINTS", "/fingerprints", "/fingerprint/", "/fingerprints/batch/fetch", "fingerprints", "fingerprints", false},
	{"ACTIVITIES", "/activities", "/activity/", "/activities/batch/fetch", "activities", "activities", false},
	{"APPLICATIONS", "/applications", "/applications/", "", "", "applications", false},
	{"DISTRIBUTIONS", "/distributions", "/distribution/", "/distributions/batch/fetch", "distributions", "distributions", false},
	{"ENTITIES", "/entities", "/entity/", "/entities/batch/fetch", "entities", "entities", false},
	{"MODELS", "/models", "/model/", "/models/batch/fetch", "models", "models", false},
	{"RELATIONSHIPS", "/relationships", "/relationship/", "", "", "relationships", false},
	{"SENSITIVES", "/sensitives", "/sensitive/", "/sensitives/batch/fetch", "sensitives", "sensitives", false},
	{"SHARES", "/shares", "/share/", "/shares/batch/fetch", "shares", "shares", false},
	{"SUBSCRIPTIONS", "/subscriptions", "/subscription/", "/subscriptions/batch/fetch", "subscriptions", "subscriptions", false},
	{"USERS", "/users", "", "/users/batch/fetch", "users", "users", false},
	{"ALLOCATIONS", "/allocations", "/allocation/", "", "", "allocations", false},
	{"INTERNAL_SUMMARY_REPORTS", "", "", "", "", "internal_summary_reports", false},
}

func SelectMaterials(selection string) ([]Material, error) {
	if selection == "" || strings.EqualFold(selection, "all") {
		return append([]Material(nil), Materials...), nil
	}
	want := map[string]bool{}
	for _, s := range strings.Split(selection, ",") {
		want[strings.ToUpper(strings.TrimSpace(s))] = true
	}
	var result []Material
	for _, m := range Materials {
		if want[m.Type] {
			result = append(result, m)
			delete(want, m.Type)
		}
	}
	if len(want) > 0 {
		return nil, errConfig("unknown material type; use the materials command")
	}
	return result, nil
}

func materialByType(t string) (Material, bool) {
	if family, ok := associationFamilyByType(t); ok {
		return family.material(), true
	}
	for _, m := range Materials {
		if m.Type == t {
			return m, true
		}
	}
	return Material{}, false
}

var referenceTypes = map[string]string{
	"summaries": "WORKSTREAM_SUMMARIES", "parents": "WORKSTREAM_SUMMARIES", "children": "WORKSTREAM_SUMMARIES", "summary": "WORKSTREAM_SUMMARIES", "summaryRoot": "WORKSTREAM_SUMMARIES",
	"events": "WORKSTREAM_EVENTS", "workstream_events": "WORKSTREAM_EVENTS", "workstreamEvents": "WORKSTREAM_EVENTS",
	"tags": "TAGS", "annotations": "ANNOTATIONS", "persons": "PERSONS", "person": "PERSONS", "websites": "WEBSITES",
	"anchors": "ANCHORS", "anchor_points": "ANCHOR_POINTS", "anchorPoints": "ANCHOR_POINTS", "assets": "ASSETS", "asset": "ASSETS",
	"formats": "FORMATS", "original": "FORMATS", "preview": "FORMATS", "alternatives": "FORMATS",
	"conversations": "CONVERSATIONS", "conversation": "CONVERSATIONS", "messages": "CONVERSATION_MESSAGES", "messageRoot": "CONVERSATION_MESSAGES",
	"ranges": "RANGES", "hints": "HINTS", "sources": "WORKSTREAM_PATTERN_ENGINE_SOURCES", "source_windows": "WORKSTREAM_PATTERN_ENGINE_SOURCE_WINDOWS",
	"connectors": "CONNECTORS", "pipelines": "PIPELINES", "signals": "SIGNALS", "schedules": "SCHEDULES", "fingerprints": "FINGERPRINTS",
	"sensitives": "SENSITIVES", "models": "MODELS", "applications": "APPLICATIONS", "users": "USERS",
}
