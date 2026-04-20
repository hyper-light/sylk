package recipe

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks that a recipe is well-formed enough for the runtime
// to attempt execution. Validation failures are reported as a flat list
// of named errors; callers may surface all of them at once rather than
// fix-and-retry one at a time.
//
// Validate does not check that referenced resources exist (e.g. that
// Requires recipe IDs are registered). That check is the runtime's
// responsibility at execution time, not the schema's.
func Validate(r *Recipe) error {
	if r == nil {
		return ErrNilRecipe
	}
	errs := newValidationErrors()
	validateSchemaVersion(r, errs)
	validateFetch(r, errs)
	validateExtract(r, errs)
	validateOutput(r, errs)
	validateBuild(r, errs)
	return errs.toError()
}

func validateSchemaVersion(r *Recipe, errs *validationErrors) {
	if r.SchemaVersion != SchemaVersion {
		errs.add(fmt.Errorf("%w: got %d, supported is %d", ErrUnsupportedSchemaVersion, r.SchemaVersion, SchemaVersion))
	}
}

func validateFetch(r *Recipe, errs *validationErrors) {
	if len(r.Fetch) == 0 {
		errs.add(ErrNoFetchSpec)
		return
	}
	for i := range r.Fetch {
		validateOneFetch(i, &r.Fetch[i], errs)
	}
}

func validateOneFetch(idx int, f *FetchSpec, errs *validationErrors) {
	if strings.TrimSpace(f.Source) == "" {
		errs.add(fmt.Errorf("%w: fetch[%d]", ErrEmptyFetchSource, idx))
	}
	if f.Hash.IsEmpty() {
		errs.add(fmt.Errorf("%w: fetch[%d]", ErrEmptyFetchHash, idx))
	}
}

func validateExtract(r *Recipe, errs *validationErrors) {
	if !isKnownExtractFormat(r.Extract.Format) {
		errs.add(fmt.Errorf("%w: %q", ErrUnknownExtractFormat, r.Extract.Format))
	}
}

func isKnownExtractFormat(f ExtractFormat) bool {
	switch f {
	case ExtractFormatTar, ExtractFormatTarGz, ExtractFormatTarXz, ExtractFormatTarZst,
		ExtractFormatZip, ExtractFormatRaw, ExtractFormatGitTree, ExtractFormatOCI:
		return true
	}
	return false
}

func validateOutput(r *Recipe, errs *validationErrors) {
	if len(r.Output) == 0 {
		errs.add(ErrNoOutputMappings)
		return
	}
	seen := make(map[string]struct{}, len(r.Output))
	for i := range r.Output {
		validateOneOutput(i, &r.Output[i], seen, errs)
	}
}

func validateOneOutput(idx int, o *OutputMapping, seen map[string]struct{}, errs *validationErrors) {
	if strings.TrimSpace(o.SubstratePath) == "" {
		errs.add(fmt.Errorf("%w: output[%d]", ErrEmptyOutputPath, idx))
		return
	}
	if _, dup := seen[o.SubstratePath]; dup {
		errs.add(fmt.Errorf("%w: %q", ErrDuplicateOutputPath, o.SubstratePath))
		return
	}
	seen[o.SubstratePath] = struct{}{}
}

func validateBuild(r *Recipe, errs *validationErrors) {
	for i := range r.Build {
		validateOneBuildStep(i, &r.Build[i], errs)
	}
}

func validateOneBuildStep(idx int, b *BuildStep, errs *validationErrors) {
	if len(b.Cmd) == 0 {
		errs.add(fmt.Errorf("%w: build[%d]", ErrEmptyBuildCmd, idx))
	}
}

// validationErrors accumulates failures for a flat report.
type validationErrors struct {
	items []error
}

func newValidationErrors() *validationErrors {
	return &validationErrors{}
}

func (v *validationErrors) add(err error) {
	v.items = append(v.items, err)
}

func (v *validationErrors) toError() error {
	if len(v.items) == 0 {
		return nil
	}
	return errors.Join(v.items...)
}

// Errors returned by Validate.
var (
	ErrNilRecipe                = errors.New("recipe: nil")
	ErrUnsupportedSchemaVersion = errors.New("recipe: unsupported schema version")
	ErrNoFetchSpec              = errors.New("recipe: at least one FetchSpec is required")
	ErrEmptyFetchSource         = errors.New("recipe: fetch source is empty")
	ErrEmptyFetchHash           = errors.New("recipe: fetch hash is empty")
	ErrUnknownExtractFormat     = errors.New("recipe: unknown extract format")
	ErrNoOutputMappings         = errors.New("recipe: at least one OutputMapping is required")
	ErrEmptyOutputPath          = errors.New("recipe: output substrate path is empty")
	ErrDuplicateOutputPath      = errors.New("recipe: duplicate output substrate path")
	ErrEmptyBuildCmd            = errors.New("recipe: build step Cmd is empty")
)
