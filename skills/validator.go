package skills

import (
	"path/filepath"
	"regexp"
	"strings"
)

const (
	MaxNameLength   = 64
	MaxDescLength   = 1024
	MaxCompatLength = 500
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Validate checks a skill directory for spec compliance
func Validate(skillDir string) error {
	skill, err := ReadProperties(skillDir)
	if err != nil {
		return err
	}
	return ValidateSkill(skill, filepath.Base(skillDir))
}

// ValidateSkill checks a skill struct for spec compliance
func ValidateSkill(s Skill, dirName string) error {
	if err := validateName(s.Name, dirName); err != nil {
		return err
	}
	if err := validateDescription(s.Description); err != nil {
		return err
	}
	return validateCompatibility(s.Compatibility)
}

func validateName(name, dirName string) error {
	if name == "" {
		return ErrMissingName
	}
	if len(name) > MaxNameLength {
		return ErrNameTooLong
	}
	if !namePattern.MatchString(name) {
		return ErrInvalidName
	}
	if name != dirName {
		return ErrNameMismatch
	}
	return nil
}

func validateDescription(desc string) error {
	if desc == "" {
		return ErrMissingDesc
	}
	if len(desc) > MaxDescLength {
		return ErrDescTooLong
	}
	return nil
}

func validateCompatibility(compat string) error {
	if compat == "" {
		return nil
	}
	if len(compat) > MaxCompatLength {
		return ErrCompatTooLong
	}
	return nil
}

func isNormalized(s string) bool {
	return s == strings.ToLower(s)
}
