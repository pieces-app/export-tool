package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

const eventPersonFamily = "workstream_event_to_person_associations"
const associationPageSize = 50
const maxAssociationPagesPerPerson = 20000

var errAssociationPaginationSchema = errors.New("association endpoint did not expose pagination metadata")

type associationPage struct {
	records     []map[string]any
	total       int
	fingerprint [32]byte
}

// Both source routes share this collection schema. The exporter normally pages
// by person, which requires far fewer initial requests than paging by event.
func (c *Client) readEventPersonPage(ctx context.Context, kind, id string, limit, offset int) (associationPage, error) {
	page := associationPage{}
	if (kind != "person" && kind != "workstream_event") || !validAssociationIdentity(id) || limit < 1 || limit > 50 || offset < 0 || offset > maxAssociationPairs {
		return page, errConfig("association page requires a supported endpoint and bounded pagination")
	}
	path := fmt.Sprintf("/%s/%s/%s?limit=%d&offset=%d&transferables=false", eventPersonFamily, kind, url.PathEscape(id), limit, offset)
	var v map[string]any
	if err := c.JSON(ctx, "GET", path, nil, &v); err != nil {
		return page, err
	}
	if v["limit"] == nil || v["offset"] == nil || v["total"] == nil {
		return page, errAssociationPaginationSchema
	}
	integer := func(key string) (int, bool) {
		n, ok := v[key].(json.Number)
		if !ok {
			return 0, false
		}
		i, err := n.Int64()
		return int(i), err == nil && i >= 0 && i <= maxAssociationPairs
	}
	total, validTotal := integer("total")
	gotLimit, validLimit := integer("limit")
	gotOffset, validOffset := integer("offset")
	rows, validRows := v["iterable"].([]any)
	if !validTotal || !validLimit || !validOffset || gotLimit != limit || gotOffset != offset || !validRows || len(rows) > limit || !associationCollectionValid(v) {
		return page, errConfig("association response omitted or contradicted bounded pagination")
	}
	if len(rows) > 0 && (offset > total || len(rows) > total-offset) {
		return page, errConfig("association page rows exceed the reported total")
	}
	family, _ := associationFamilyByName(eventPersonFamily)
	seen := map[string]bool{}
	for _, raw := range rows {
		record, ok := raw.(map[string]any)
		if !ok {
			return associationPage{}, errConfig("association page contains an invalid record")
		}
		ref := associationReference{left: fieldString(record, "workstream_event"), right: fieldString(record, "person")}
		if !validAssociationIdentity(ref.left) || !validAssociationIdentity(ref.right) || kind == "person" && ref.right != id || kind == "workstream_event" && ref.left != id {
			return associationPage{}, errConfig("association page has an unexpected endpoint binding")
		}
		if err := validateAssociationRecord(family, record, ref); err != nil {
			return associationPage{}, err
		}
		associationID := fieldString(record, "id")
		if seen[associationID] {
			return associationPage{}, errConfig("association page has duplicate record identities")
		}
		seen[associationID] = true
		page.records = append(page.records, record)
	}
	active := references(v)
	if len(active) != len(seen) {
		return associationPage{}, errConfig("association page references contradict its records")
	}
	for _, id := range active {
		if !seen[id] {
			return associationPage{}, errConfig("association page references an unreturned record")
		}
	}
	page.total = total
	// Hash actual rows, including metadata and update times. Pagination totals
	// are checked separately. This detects some drift, not an atomic snapshot.
	encoded, err := json.Marshal(page.records)
	if err != nil {
		return associationPage{}, errConfig("association page cannot be fingerprinted")
	}
	page.fingerprint = sha256.Sum256(encoded)
	return page, nil
}

type AssociationPageCoverage struct {
	Persons              int `json:"persons"`
	Attempted            int `json:"attempted_persons"`
	Reconciled           int `json:"reconciled_persons"`
	Pages                int `json:"page_reads"`
	Rows                 int `json:"returned_rows"`
	Unavailable          int `json:"unavailable_or_unsupported_persons"`
	Unsupported          int `json:"unsupported_persons"`
	SchemaUnsupported    int `json:"unsupported_schema_persons"`
	Failed               int `json:"failed_persons"`
	Drift                int `json:"unstable_persons"`
	NotAttempted         int `json:"not_attempted_persons"`
	UnavailableEndpoints int `json:"unavailable_endpoints"`
}
