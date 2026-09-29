package exporter

import (
	"context"
	"errors"
	"net/url"
)

func currentUserID(ctx context.Context, c *Client) (string, error) {
	var out map[string]any
	if err := c.JSON(ctx, "GET", "/user", nil, &out); err != nil {
		return "", err
	}
	if id := fieldString(object(out, "user"), "id"); id != "" {
		return id, nil
	}
	return fieldString(out, "id"), nil
}

func userPersonID(ctx context.Context, c *Client, userID string) (string, error) {
	var out map[string]any
	path := "/user/" + url.PathEscape(userID) + "/person?transferables=false"
	if err := c.JSON(ctx, "GET", path, nil, &out); err != nil {
		return "", err
	}
	return fieldString(out, "id"), nil
}

func isNotFound(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == 404
}

// resolveUserPeople only accepts the explicit user-to-person endpoint. A
// matching display name, email, or platform field never moves a person into a
// users folder.
func (r *run) resolveUserPeople() error {
	if _, selected := r.coverage["PERSONS"]; !selected {
		return nil
	}
	userIDs := map[string]bool{}
	current, err := currentUserID(r.ctx, r.client)
	if err != nil {
		if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
			return err
		}
		if !isNotFound(err) {
			r.manifest.Warnings = append(r.manifest.Warnings, "The current user could not be read, so users/ folders could not be verified from the user-to-person endpoint.")
		}
	} else if current != "" {
		userIDs[current] = true
	}
	// When the optional USERS collection is selected, each retained user record
	// is also checked through the same authoritative mapping route. This permits
	// multiple verified user folders without treating an identity field as proof.
	for _, m := range r.sortedMeta() {
		if m.Type == "USERS" && m.State == "included" {
			userIDs[m.ID] = true
		}
	}
	ids := make([]string, 0, len(userIDs))
	for id := range userIDs {
		ids = append(ids, id)
	}
	for _, userID := range unique(ids) {
		personID, err := userPersonID(r.ctx, r.client, userID)
		if err != nil {
			if errors.Is(err, ErrOSBusy) || r.ctx.Err() != nil {
				return err
			}
			if !isNotFound(err) {
				r.manifest.Warnings = append(r.manifest.Warnings, "A user-to-person mapping could not be read; only successfully verified mappings appear under users/.")
			}
			continue
		}
		if personID == "" {
			r.manifest.Warnings = append(r.manifest.Warnings, "A user-to-person response omitted its person identity; no users/ folder was inferred.")
			continue
		}
		key := "PERSONS\x00" + personID
		if r.meta[key] == nil {
			material, _ := materialByType("PERSONS")
			if err := r.fetch(material, []string{personID}); err != nil {
				return err
			}
		}
		if person := r.meta[key]; person != nil && person.State == "included" {
			r.userPersonIDs[personID] = true
		}
	}
	return nil
}
