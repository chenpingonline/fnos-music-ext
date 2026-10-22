package provider

// AccountSource carries the private cache namespace of an authorized account.
type AccountSource struct {
	Adapter Adapter
	Scope   string
}
