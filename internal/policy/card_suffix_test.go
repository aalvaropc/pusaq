package policy

import (
	"slices"
	"testing"
)

func TestCardSuffixBoundaries(t *testing.T) {
	for _, tt := range []struct {
		text  string
		match bool
	}{
		{"card 4111 1111 1111 1111 12/27", true},
		{"card 4111-1111-1111-1111 12/27", true},
		{"card 4111111111111111 123", true},
		{"card 4111 1111 1111 1111 123", true},
		{"card 4111 1111 1111 1111 exp 12/27", true},
		{"card 5500 0000 0000 0004 12/27", true},
		{"card 5500000000000004 123", true},
		{"card 3782 822463 10005 12/27", true},
		{"card 378282246310005 1234", true},
		{"card 6011 0009 9013 9424 12/27", true},
		{"card 6011000990139424 123", true},
		{"card 4222 2222 22222 12/27", true},
		{"card 4000000000000000006 123", true},
		{"cards 1234 5678 9012 3456 and 4111 1111 1111 1111 123", true},
		{"card 4111 1111 1111 1112 12/27", false},
		{"card 4111 1111 1111 1112 123", false},
		{"card 5500000000000005 123", false},
		{"card 378282246310006 1234", false},
		{"card 6011000990139425 12/27", false},
		{"id 41111111111111110", false},
		{"id 411111111111111111", false},
		{"id 4111111111111111123", false},
		{"id 40000000000000000060", false},
		{"id x4111111111111111y", false},
		{"order 123456789012", false},
		{"order 123456789012345678901", false},
		{"card 4111-1111-1111-1111-123", true},
		{"card 9111111111111110 123", false},
	} {
		if got := slices.Contains(Sensitive(tt.text), "pii:card"); got != tt.match {
			t.Errorf("card match for %q = %v, want %v", tt.text, got, tt.match)
		}
	}
}
