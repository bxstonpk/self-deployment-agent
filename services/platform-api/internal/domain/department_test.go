package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeDepartmentName(t *testing.T) {
	cases := map[string]string{
		"Innovation":            "Innovation",
		"  Human   Resources ":  "Human Resources",
		"ฝ่ายบัญชี":             "ฝ่ายบัญชี",
		"\tFinance\n":           "Finance",
		strings.Repeat("a", 80): strings.Repeat("a", 80),
	}
	for in, want := range cases {
		got, err := NormalizeDepartmentName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeDepartmentName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// The last two: Thai sent in a Windows code page instead of UTF-8, and
	// what such bytes become once decoded.
	for _, bad := range []string{"", "   ", strings.Repeat("a", 81), "IT\x00", "\xbf\xe8\xd2\xc2", "ab�cd"} {
		if _, err := NormalizeDepartmentName(bad); !errors.Is(err, ErrInvalidDepartmentName) {
			t.Errorf("NormalizeDepartmentName(%q) should be refused, got %v", bad, err)
		}
	}
}
