package recipe

import (
	"errors"
	"testing"
)

func TestValidate_NilRecipe(t *testing.T) {
	if err := Validate(nil); !errors.Is(err, ErrNilRecipe) {
		t.Fatalf("Validate(nil): got %v, want ErrNilRecipe", err)
	}
}

func TestValidate_HappyPath(t *testing.T) {
	r := sampleRecipe()
	if err := Validate(&r); err != nil {
		t.Fatalf("Validate(sampleRecipe): %v", err)
	}
}

func TestValidate_UnsupportedSchemaVersion(t *testing.T) {
	r := sampleRecipe()
	r.SchemaVersion = 999
	err := Validate(&r)
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("expected ErrUnsupportedSchemaVersion, got %v", err)
	}
}

func TestValidate_NoFetchSpec(t *testing.T) {
	r := sampleRecipe()
	r.Fetch = nil
	err := Validate(&r)
	if !errors.Is(err, ErrNoFetchSpec) {
		t.Fatalf("expected ErrNoFetchSpec, got %v", err)
	}
}

func TestValidate_EmptyFetchSource(t *testing.T) {
	r := sampleRecipe()
	r.Fetch[0].Source = "  "
	err := Validate(&r)
	if !errors.Is(err, ErrEmptyFetchSource) {
		t.Fatalf("expected ErrEmptyFetchSource, got %v", err)
	}
}

func TestValidate_EmptyFetchHash(t *testing.T) {
	r := sampleRecipe()
	r.Fetch[0].Hash = Hash{}
	err := Validate(&r)
	if !errors.Is(err, ErrEmptyFetchHash) {
		t.Fatalf("expected ErrEmptyFetchHash, got %v", err)
	}
}

func TestValidate_UnknownExtractFormat(t *testing.T) {
	r := sampleRecipe()
	r.Extract.Format = "made-up-format"
	err := Validate(&r)
	if !errors.Is(err, ErrUnknownExtractFormat) {
		t.Fatalf("expected ErrUnknownExtractFormat, got %v", err)
	}
}

func TestValidate_NoOutputMappings(t *testing.T) {
	r := sampleRecipe()
	r.Output = nil
	err := Validate(&r)
	if !errors.Is(err, ErrNoOutputMappings) {
		t.Fatalf("expected ErrNoOutputMappings, got %v", err)
	}
}

func TestValidate_EmptyOutputPath(t *testing.T) {
	r := sampleRecipe()
	r.Output[0].SubstratePath = ""
	err := Validate(&r)
	if !errors.Is(err, ErrEmptyOutputPath) {
		t.Fatalf("expected ErrEmptyOutputPath, got %v", err)
	}
}

func TestValidate_DuplicateOutputPath(t *testing.T) {
	r := sampleRecipe()
	r.Output = []OutputMapping{
		{SubstratePath: "/dup", SourcePath: "/a"},
		{SubstratePath: "/dup", SourcePath: "/b"},
	}
	err := Validate(&r)
	if !errors.Is(err, ErrDuplicateOutputPath) {
		t.Fatalf("expected ErrDuplicateOutputPath, got %v", err)
	}
}

func TestValidate_EmptyBuildCmd(t *testing.T) {
	r := sampleRecipeWithBuild()
	r.Build[1].Cmd = nil
	err := Validate(&r)
	if !errors.Is(err, ErrEmptyBuildCmd) {
		t.Fatalf("expected ErrEmptyBuildCmd, got %v", err)
	}
}

func TestValidate_AggregatesAllErrors(t *testing.T) {
	r := sampleRecipe()
	r.SchemaVersion = 999
	r.Fetch[0].Source = ""
	r.Output[0].SubstratePath = ""
	err := Validate(&r)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Errorf("expected aggregate to contain ErrUnsupportedSchemaVersion, got %v", err)
	}
	if !errors.Is(err, ErrEmptyFetchSource) {
		t.Errorf("expected aggregate to contain ErrEmptyFetchSource, got %v", err)
	}
	if !errors.Is(err, ErrEmptyOutputPath) {
		t.Errorf("expected aggregate to contain ErrEmptyOutputPath, got %v", err)
	}
}
