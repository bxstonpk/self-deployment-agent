package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxDepartmentNameLength bounds a department's name, in characters.
const MaxDepartmentNameLength = 80

var ErrInvalidDepartmentName = errors.New("a department name must be 1 to 80 characters of valid UTF-8 text, with no control characters")

// NormalizeDepartmentName is how every department name is stored and
// compared: trimmed, with runs of whitespace collapsed to one space, so
// "  Human   Resources " and "Human Resources" are the same department.
// Any language is fine — Thai department names included.
func NormalizeDepartmentName(name string) (string, error) {
	n := strings.Join(strings.Fields(name), " ")
	if n == "" || utf8.RuneCountInString(n) > MaxDepartmentNameLength {
		return "", ErrInvalidDepartmentName
	}
	for _, r := range n {
		// U+FFFD is what invalid UTF-8 decodes to: a name sent in another
		// encoding (found with a Thai name from a Windows console) would
		// otherwise be stored as replacement characters, for good.
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "", ErrInvalidDepartmentName
		}
	}
	return n, nil
}

// DepartmentSummary is a department with how many people and
// applications belong to it.
type DepartmentSummary struct {
	Department
	Users        int
	Applications int // not deleted
}
