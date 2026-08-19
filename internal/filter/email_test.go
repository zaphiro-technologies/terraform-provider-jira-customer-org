package filter

import "testing"

func TestNormalizeEmail(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{
		{input: " USER@EXAMPLE.COM ", want: "user@example.com", valid: true},
		{input: "", valid: false},
		{input: "User <user@example.com>", valid: false},
		{input: "user@@example.com", valid: false},
		{input: "@example.com", valid: false},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, valid := NormalizeEmail(test.input)
			if got != test.want || valid != test.valid {
				t.Errorf("NormalizeEmail(%q) = %q, %v; want %q, %v", test.input, got, valid, test.want, test.valid)
			}
		})
	}
}
