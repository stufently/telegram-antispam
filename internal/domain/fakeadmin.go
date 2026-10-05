package domain

import (
	"fmt"
	"regexp"
)

// Fake-admin details identify fields, never the names those fields contain.
const (
	FakeAdminFieldUsername    = "username"
	FakeAdminFieldDisplayName = "display_name"
	FakeAdminFieldCustomTitle = "custom_title"
	FakeAdminTagDetail        = "sender_tag~suspicious_tags"
)

// FakeAdminMatchDetail describes a match using field names and the admin ID.
// Callers supply the field constants above and compare lowercased values.
func FakeAdminMatchDetail(senderField, adminField string, adminID int64, exact bool) string {
	kind := "fuzzy"
	if exact {
		kind = "exact"
	}
	return fmt.Sprintf("%s~%s admin_id=%d %s", senderField, adminField, adminID, kind)
}

var fakeAdminDetailPattern = regexp.MustCompile(`^(username|display_name)~(username|display_name|custom_title) admin_id=-?[0-9]{1,20} (exact|fuzzy)$`)

// IsFakeAdminDetail permits only the structured grammar safe for outcome logs.
// Legacy details containing names must never pass this boundary.
func IsFakeAdminDetail(s string) bool {
	return s == FakeAdminTagDetail || fakeAdminDetailPattern.MatchString(s)
}
