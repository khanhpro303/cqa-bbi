package cloudifyorders

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Previous is the stored state of one order the diff compares against.
type Previous struct {
	Status      string
	Fingerprint string
}

// Change is one detected difference between a fetched order and its snapshot.
type Change struct {
	Kind            string
	Order           Order
	PrevStatus      string
	PrevFingerprint string
}

// DiffOptions controls which differences become events.
type DiffOptions struct {
	// Seeding stores snapshots without emitting any event (first poll).
	Seeding bool
	// CreatedCutoff: an order seen for the first time whose order date is
	// before this day is old data entering the window (for example after the
	// window was widened), not a new order. Zero disables the check.
	CreatedCutoff time.Time
}

// Diff compares fetched orders with their previous snapshots. Every fetched
// order whose fingerprint changed is returned in Updates (to store), and the
// ones worth telling someone about are returned in Changes.
func Diff(prev map[int64]Previous, orders []Order, opts DiffOptions) (changes []Change, updates []Order) {
	seen := map[int64]bool{}
	for _, order := range orders {
		if seen[order.ID] {
			continue
		}
		seen[order.ID] = true
		before, known := prev[order.ID]
		if known && before.Fingerprint == order.Fingerprint {
			continue
		}
		updates = append(updates, order)
		if opts.Seeding {
			continue
		}
		switch {
		case !known:
			if isBefore(order.Date, opts.CreatedCutoff) {
				continue
			}
			changes = append(changes, Change{Kind: KindCreated, Order: order})
		case before.Status != order.Status:
			changes = append(changes, Change{Kind: KindStatusChanged, Order: order, PrevStatus: before.Status, PrevFingerprint: before.Fingerprint})
		default:
			changes = append(changes, Change{Kind: KindUpdated, Order: order, PrevStatus: before.Status, PrevFingerprint: before.Fingerprint})
		}
	}
	return changes, updates
}

// DedupKey identifies one transition of one order. Two sources that observe
// the same transition produce the same key, so only one event is stored.
func DedupKey(tenantID string, change Change) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		tenantID, strconv.FormatInt(change.Order.ID, 10), change.PrevFingerprint, change.Order.Fingerprint,
	}, "|")))
	return hex.EncodeToString(sum[:])
}

// isBefore reports whether an order date (Cloudify ships "2006-01-02",
// sometimes with a time) is before cutoff's day. Unparseable dates are
// treated as recent so a new order is never silently dropped.
func isBefore(date string, cutoff time.Time) bool {
	if cutoff.IsZero() {
		return false
	}
	date = strings.TrimSpace(date)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02", "02/01/2006"} {
		if t, err := time.ParseInLocation(layout, date, cutoff.Location()); err == nil {
			day := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, cutoff.Location())
			return t.Before(day)
		}
	}
	return false
}
