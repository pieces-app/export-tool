package exporter

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// These are separate records, not generic material inventories. The endpoint
// paths and JSON bindings differ (notably camelCase on connector relations).
// Keep the allowlist tied to the server routes and common association schemas.
type associationFamily struct {
	name                  string
	leftType, rightType   string
	leftRoute, rightRoute string
	leftField, rightField string
}

var associationFamilies = []associationFamily{
	{"connector_to_annotation_associations", "CONNECTORS", "ANNOTATIONS", "connector", "annotation", "connector", "annotation"},
	{"connector_to_conversation_associations", "CONNECTORS", "CONVERSATIONS", "connector", "conversation", "connector", "conversation"},
	{"connector_to_conversation_message_associations", "CONNECTORS", "CONVERSATION_MESSAGES", "connector", "message", "connector", "conversationMessage"},
	{"connector_to_person_associations", "CONNECTORS", "PERSONS", "connector", "person", "connector", "person"},
	{"connector_to_website_associations", "CONNECTORS", "WEBSITES", "connector", "website", "connector", "website"},
	{"connector_to_workstream_event_associations", "CONNECTORS", "WORKSTREAM_EVENTS", "connector", "workstream_event", "connector", "workstreamEvent"},
	{"connector_to_workstream_summary_associations", "CONNECTORS", "WORKSTREAM_SUMMARIES", "connector", "workstream_summary", "connector", "workstreamSummary"},
	{"fingerprint_to_person_associations", "FINGERPRINTS", "PERSONS", "fingerprint", "person", "fingerprint", "person"},
	{"fingerprint_to_workstream_event_associations", "FINGERPRINTS", "WORKSTREAM_EVENTS", "fingerprint", "workstream_event", "fingerprint", "workstream_event"},
	{"pipeline_to_connector_associations", "PIPELINES", "CONNECTORS", "pipeline", "connector", "pipeline", "connector"},
	{"pipeline_to_range_associations", "PIPELINES", "RANGES", "pipeline", "range", "pipeline", "range"},
	{"pipeline_to_schedule_associations", "PIPELINES", "SCHEDULES", "pipeline", "schedule", "pipeline", "schedule"},
	{"pipeline_to_signal_associations", "PIPELINES", "SIGNALS", "pipeline", "signal", "pipeline", "signal"},
	{"pipeline_to_website_associations", "PIPELINES", "WEBSITES", "pipeline", "website", "pipeline", "website"},
	{"pipeline_to_workstream_pattern_engine_source_associations", "PIPELINES", "WORKSTREAM_PATTERN_ENGINE_SOURCES", "pipeline", "source", "pipeline", "source"},
	{"pipeline_to_workstream_summary_associations", "PIPELINES", "WORKSTREAM_SUMMARIES", "pipeline", "workstream_summary", "pipeline", "workstreamSummary"},
	{"signal_to_annotation_associations", "SIGNALS", "ANNOTATIONS", "signal", "annotation", "signal", "annotation"},
	{"signal_to_person_associations", "SIGNALS", "PERSONS", "signal", "person", "signal", "person"},
	{"signal_to_range_associations", "SIGNALS", "RANGES", "signal", "range", "signal", "range"},
	{"signal_to_website_associations", "SIGNALS", "WEBSITES", "signal", "website", "signal", "website"},
	{"signal_to_workstream_event_associations", "SIGNALS", "WORKSTREAM_EVENTS", "signal", "workstream_event", "signal", "workstream_event"},
	{"signal_to_workstream_summary_associations", "SIGNALS", "WORKSTREAM_SUMMARIES", "signal", "workstream_summary", "signal", "workstream_summary"},
	{"workstream_event_to_person_associations", "WORKSTREAM_EVENTS", "PERSONS", "workstream_event", "person", "workstream_event", "person"},
}

func associationFamilyByName(name string) (associationFamily, bool) {
	for _, family := range associationFamilies {
		if family.name == name {
			return family, true
		}
	}
	return associationFamily{}, false
}

type associationReference struct {
	id, left, right string
}

type associationBatchResult struct {
	records     []map[string]any
	unavailable []associationReference
}

func validAssociationIdentity(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 4096 && !strings.ContainsFunc(id, unicode.IsControl)
}

func validateAssociationRecord(family associationFamily, v map[string]any, ref associationReference) error {
	id := fieldString(v, "id")
	if !validAssociationIdentity(id) || ref.id != "" && ref.id != id || fieldString(v, family.leftField) != ref.left || fieldString(v, family.rightField) != ref.right {
		return errConfig("association response identity or endpoint bindings do not match the requested record")
	}
	for _, field := range []string{"created", "updated"} {
		if _, err := time.Parse(time.RFC3339Nano, timestamp(v, field)); err != nil {
			return errConfig("association response has invalid source timestamps")
		}
	}
	return nil
}

// The caller must discover a pair from actual graph evidence, not a Cartesian
// product or a guessed association UUID. HTTP 404 does not establish a negative
// relationship: older servers can also lack the route. Return it unchanged.
func (c *Client) readAssociationPair(ctx context.Context, name, left, right string) (map[string]any, error) {
	family, ok := associationFamilyByName(name)
	if !ok || !validAssociationIdentity(left) || !validAssociationIdentity(right) {
		return nil, errConfig("association lookup requires a supported family and valid endpoint identities")
	}
	path := "/" + family.name + "/" + family.leftRoute + "/" + url.PathEscape(left) + "/" + family.rightRoute + "/" + url.PathEscape(right)
	var v map[string]any
	if err := c.JSON(ctx, "GET", path, nil, &v); err != nil {
		return nil, err
	}
	if err := validateAssociationRecord(family, v, associationReference{left: left, right: right}); err != nil {
		return nil, err
	}
	// Keep every metadata field, including future fields. Sanitization belongs
	// to the ordinary canonical-record pipeline before storage or rendering.
	return v, nil
}

// Fetch only association IDs already obtained from a source read. Each expected
// endpoint pair remains bound to its ID so swapped or unrelated responses fail.
// The API returns notFound as a flattened association collection, not []string;
// source read failures and absent records are both represented there.
func (c *Client) readAssociationBatch(ctx context.Context, name string, refs []associationReference) (associationBatchResult, error) {
	result := associationBatchResult{}
	family, ok := associationFamilyByName(name)
	if !ok || len(refs) > 50 {
		return result, errConfig("association batch requires a supported family and at most 50 references")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	wanted := map[string]associationReference{}
	items := []map[string]any{}
	ordered := []associationReference{}
	for _, ref := range refs {
		if !validAssociationIdentity(ref.id) || !validAssociationIdentity(ref.left) || !validAssociationIdentity(ref.right) {
			return result, errConfig("association batch contains an invalid identity")
		}
		if prior, exists := wanted[ref.id]; exists {
			if prior != ref {
				return result, errConfig("association batch has conflicting endpoint bindings")
			}
			continue
		}
		wanted[ref.id] = ref
		items = append(items, map[string]any{"id": ref.id})
		ordered = append(ordered, ref)
	}
	if len(ordered) == 0 {
		return result, nil
	}
	var envelope map[string]any
	if err := c.JSON(ctx, "POST", "/"+family.name+"/batch/fetch?transferables=true", map[string]any{"associations": map[string]any{"iterable": items}}, &envelope); err != nil {
		return result, err
	}
	found := object(envelope, "associations")
	missing := object(envelope, "notFound")
	if !associationCollectionValid(found) || !associationCollectionValid(missing) {
		return result, errConfig("association batch has invalid collection envelopes")
	}
	rows, ok := found["iterable"].([]any)
	if !ok || len(rows) > len(ordered) {
		return result, errConfig("association batch has invalid or excessive returned records")
	}
	records := map[string]map[string]any{}
	for _, raw := range rows {
		v, ok := raw.(map[string]any)
		if !ok {
			return result, errConfig("association batch contains an invalid record")
		}
		id := fieldString(v, "id")
		ref, requested := wanted[id]
		if !requested || records[id] != nil {
			return result, errConfig("association batch contains an unexpected or duplicate record")
		}
		if err := validateAssociationRecord(family, v, ref); err != nil {
			return result, err
		}
		records[id] = v
	}
	// Detect contradictory indices, tombstones or unexplained references.
	foundIDs := references(found)
	if len(foundIDs) != len(records) {
		return result, errConfig("association batch record collection contradicts its active references")
	}
	for _, id := range foundIDs {
		if records[id] == nil {
			return result, errConfig("association batch references an unreturned record")
		}
	}
	missingIDs := map[string]bool{}
	for _, id := range references(missing) {
		if _, requested := wanted[id]; !requested || records[id] != nil {
			return result, errConfig("association batch has contradictory unavailable references")
		}
		missingIDs[id] = true
	}
	for _, ref := range ordered {
		if record := records[ref.id]; record != nil {
			result.records = append(result.records, record)
		} else if missingIDs[ref.id] {
			result.unavailable = append(result.unavailable, ref)
		} else {
			return associationBatchResult{}, errConfig("association batch did not account for every requested reference")
		}
	}
	return result, nil
}

func associationCollectionValid(v map[string]any) bool {
	state := projectionState(v)
	return state == "linked" || state == "empty"
}
