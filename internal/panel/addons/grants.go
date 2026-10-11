package addons

import (
	"encoding/json"
	"slices"
)

// The admin approves the permissions of an addon of protocol 2 as a set: the table
// addon_grants keeps them sorted, as JSON.

// EncodePermissions is the stored form: sorted, without repeats.
func EncodePermissions(perms []string) string {
	s := slices.Clone(perms)
	slices.Sort(s)
	data, _ := json.Marshal(slices.Compact(s))
	return string(data)
}

// DecodePermissions reads what EncodePermissions stored.
func DecodePermissions(stored string) ([]string, error) {
	var perms []string
	err := json.Unmarshal([]byte(stored), &perms)
	return perms, err
}

// Covers: every permission asked for was approved.
func Covers(approved, asked []string) bool {
	for _, p := range asked {
		if !slices.Contains(approved, p) {
			return false
		}
	}
	return true
}

// SamePermissions: the two hold the same set.
func SamePermissions(a, b []string) bool {
	return Covers(a, b) && Covers(b, a)
}
