package skills

import "errors"

var (
	ErrSkillNotFound   = errors.New("SKILL.md not found")
	ErrParseFailed     = errors.New("failed to parse SKILL.md")
	ErrMissingName     = errors.New("missing required field: name")
	ErrMissingDesc     = errors.New("missing required field: description")
	ErrInvalidName     = errors.New("invalid skill name")
	ErrNameTooLong     = errors.New("name exceeds 64 characters")
	ErrDescTooLong     = errors.New("description exceeds 1024 characters")
	ErrCompatTooLong   = errors.New("compatibility exceeds 500 characters")
	ErrNameMismatch    = errors.New("skill name must match directory name")
	ErrUnknownField    = errors.New("unknown frontmatter field")
)
