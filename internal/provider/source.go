package provider

import "strings"

// SourceName resolves a provider ID to its display name, independent of the
// transport (native or bridge). Unknown providers retain their own ID.
func SourceName(id string) string {
	id = strings.TrimSpace(id)
	switch id {
	case "netease":
		return "网易云"
	default:
		return id
	}
}
