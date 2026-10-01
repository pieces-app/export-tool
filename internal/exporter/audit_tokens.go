package exporter

import (
	"context"
	"strings"
)

const auditTokenEntries = 4096
const auditTokenBytes = 1 << 20
const auditTokenMaxValue = 2048

// A single JSON file is audited with fixed scanner inputs. Repeated decoded
// tokens use exactly the same empty-key scan context, including JSON numbers.
// Keep only complete approvals, with exact string equality and bounded owned
// bytes. This cache never survives a file, traversal, error or policy change.
type auditTokenCache struct {
	approved map[string]struct{}
	bytes    int
}

func (c *auditTokenCache) check(ctx context.Context, value string, scan func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := c.approved[value]; ok {
		return nil
	}
	if err := scan(value); err != nil {
		return err
	}
	if len(value) > auditTokenMaxValue || len(c.approved) >= auditTokenEntries || len(value) > auditTokenBytes-c.bytes {
		return nil
	}
	if c.approved == nil {
		c.approved = make(map[string]struct{})
	}
	c.approved[strings.Clone(value)] = struct{}{}
	c.bytes += len(value)
	return nil
}
