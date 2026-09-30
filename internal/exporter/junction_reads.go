package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
)

const maxJunctionBulkIDs = 50 // Server cap is 200; keep adaptive batches smaller.
const maxJunctionBulkRows = 5000

func junctionSide(name, side string) (associationFamily, string, error) {
	f, ok := junctionFamilyByName(name)
	if !ok {
		return f, "", errConfig("unsupported junction family")
	}
	if side == f.leftRoute {
		return f, f.leftField, nil
	}
	if side == f.rightRoute {
		return f, f.rightField, nil
	}
	return f, "", errConfig("unsupported junction side")
}

// Unlike the older event-person listing, current generic pages do not carry
// total/offset/limit. Obtain counts from the separate endpoint. Counts are not
// snapshot tokens; callers must reconcile drift across the complete traversal.
func (c *Client) readJunctionCount(ctx context.Context, name, side, id string) (int, error) {
	if _, _, err := junctionSide(name, side); err != nil {
		return 0, err
	}
	if !validAssociationIdentity(id) {
		return 0, errConfig("junction count requires a valid source identity")
	}
	var v map[string]any
	path := "/" + name + "/" + side + "/" + url.PathEscape(id) + "/count"
	if err := c.JSON(ctx, "GET", path, nil, &v); err != nil {
		return 0, err
	}
	n, ok := v["count"].(json.Number)
	count, err := n.Int64()
	if !ok || err != nil || count < 0 || count > maxAssociationPairs || fieldString(v, "id") != id {
		return 0, errConfig("junction count has an invalid value or owner")
	}
	return int(count), nil
}

func (c *Client) readJunctionPage(ctx context.Context, name, side, id string, limit, offset int) ([]map[string]any, error) {
	f, field, err := junctionSide(name, side)
	if err != nil {
		return nil, err
	}
	if !validAssociationIdentity(id) || limit < 1 || limit > 50 || offset < 0 || offset > maxAssociationPairs {
		return nil, errConfig("junction page requires a valid identity and bounded pagination")
	}
	path := fmt.Sprintf("/%s/%s/%s?limit=%d&offset=%d&transferables=false", name, side, url.PathEscape(id), limit, offset)
	var v map[string]any
	if err := c.JSON(ctx, "GET", path, nil, &v); err != nil {
		return nil, err
	}
	return validateJunctionCollection(f, field, map[string]int{id: -1}, v, limit)
}

// Bulk pages have an input-ID cap but NO row limit. Require caller-observed
// counts and a small summed row budget before asking OS to materialize them.
// Large owners must use readJunctionPage instead. This still cannot make counts
// and subsequent reads atomic; growth/truncation/missing rows fail validation.
func (c *Client) readJunctionBulk(ctx context.Context, name, side string, counts map[string]int) ([]map[string]any, error) {
	f, field, err := junctionSide(name, side)
	if err != nil {
		return nil, err
	}
	if len(counts) < 1 || len(counts) > maxJunctionBulkIDs {
		return nil, errConfig("junction bulk requires 1–50 counted source identities")
	}
	ids := make([]string, 0, len(counts))
	total := 0
	for id, count := range counts {
		if !validAssociationIdentity(id) || count < 0 || count > maxJunctionBulkRows || total > maxJunctionBulkRows-count {
			return nil, errConfig("junction bulk row budget exceeded or counts are invalid; use bounded pages")
		}
		ids, total = append(ids, id), total+count
	}
	sort.Strings(ids)
	var v map[string]any
	if err := c.JSON(ctx, "POST", "/"+name+"/"+side+"/bulk", map[string]any{"iterable": ids}, &v); err != nil {
		return nil, err
	}
	rows, err := validateJunctionCollection(f, field, counts, v, total)
	if err != nil {
		return nil, err
	}
	got := map[string]int{}
	for _, row := range rows {
		got[fieldString(row, field)]++
	}
	for id, count := range counts {
		if got[id] != count {
			return nil, errConfig("junction bulk rows differ from the observed per-owner counts")
		}
	}
	return rows, nil
}

func validateJunctionCollection(f associationFamily, sideField string, owners map[string]int, v map[string]any, limit int) ([]map[string]any, error) {
	rows, ok := v["iterable"].([]any)
	if !ok || len(rows) > limit || !associationCollectionValid(v) {
		return nil, errConfig("junction response has an invalid or excessive collection")
	}
	if raw, exists := v["truncated"]; exists && raw != nil {
		flag, ok := raw.(bool)
		if !ok || flag {
			return nil, errConfig("junction response was truncated or has invalid truncation metadata")
		}
	}
	seen := map[string]bool{}
	result := make([]map[string]any, 0, len(rows))
	for _, raw := range rows {
		record, ok := raw.(map[string]any)
		if !ok {
			return nil, errConfig("junction response has an invalid record")
		}
		ref := associationReference{left: fieldString(record, f.leftField), right: fieldString(record, f.rightField)}
		_, owned := owners[fieldString(record, sideField)]
		if !owned || !validAssociationIdentity(ref.left) || !validAssociationIdentity(ref.right) || validateAssociationRecord(f, record, ref) != nil {
			return nil, errConfig("junction response has an invalid identity, timestamp or endpoint binding")
		}
		id := fieldString(record, "id")
		if seen[id] {
			return nil, errConfig("junction response repeats an association identity")
		}
		seen[id] = true
		result = append(result, record)
	}
	active := references(v)
	if len(active) != len(seen) {
		return nil, errConfig("junction collection references contradict returned rows")
	}
	for _, id := range active {
		if !seen[id] {
			return nil, errConfig("junction collection references an unreturned row")
		}
	}
	return result, nil
}
