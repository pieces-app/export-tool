package exporter

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in coverage check for the authoritative account mapping. Keep identities
// in memory only; output contains booleans, counts, and request measurements.
func TestLiveUserPersonMapping(t *testing.T) {
	base := os.Getenv("PIECES_EXPORT_LIVE_USER_MAPPING_URL")
	if base == "" {
		t.Skip("set PIECES_EXPORT_LIVE_USER_MAPPING_URL to opt into local reads")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := NewClient(base, 8*time.Second, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ConfigurePerformance("adaptive", 5, 500*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal("OS readiness check failed")
	}
	userID, err := currentUserID(ctx, c)
	if err != nil || userID == "" {
		t.Fatal("current user mapping unavailable (identities and response omitted)")
	}
	personID, err := userPersonID(ctx, c, userID)
	if err != nil || personID == "" {
		t.Fatal("user-to-person mapping unavailable (identities and response omitted)")
	}
	m, _ := materialByType("PERSONS")
	ids, err := c.IDs(ctx, m, Window{})
	if err != nil {
		t.Fatal("person inventory read failed")
	}
	present := false
	for _, id := range ids {
		present = present || id == personID
	}
	if _, err := c.Probe(ctx); err != nil {
		t.Fatal("final OS readiness check failed")
	}
	t.Logf("user_mapping_available=true mapped_person_in_inventory=%t persons=%d performance=%+v", present, len(ids), c.Performance())
	if !present {
		t.Fatal("mapped person was absent from the returned person inventory")
	}
}
