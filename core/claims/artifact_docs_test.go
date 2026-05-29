package claims

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestArtifactsAndValidationsDocsCrossReferenceLifecycleContract(t *testing.T) {
	root := repoRootForClaimsTest(t)
	docs := []string{
		"docs/ARTIFACTS_AND_VALIDATIONS.md",
		"docs/CLAIMS.md",
		"docs/CLAIMS_AND_DELTAS.md",
		"docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md",
		"docs/CLAIMS_VISIBILITY.md",
		"docs/CLAIMS_AND_INFRASTRUCTURE.md",
		"docs/CLAIMS_OPERATIONS.md",
	}
	statuses := append(artifactStatusDocNames(), validationStatusDocNames()...)
	for _, doc := range docs {
		body := readDocForClaimsTest(t, root, doc)
		for _, status := range statuses {
			if !strings.Contains(body, status) {
				t.Fatalf("%s missing lifecycle status %s", doc, status)
			}
		}
	}
}

func TestArtifactsAndValidationsDocsListBuiltInDataTypes(t *testing.T) {
	root := repoRootForClaimsTest(t)
	body := readDocForClaimsTest(t, root, "docs/ARTIFACTS_AND_VALIDATIONS.md")
	for _, dataType := range []string{
		ArtifactDataTypePlanMarkdown,
		ArtifactDataTypeExpectedToolInvocation,
		ArtifactDataTypeExpectedToolOutput,
		ArtifactDataTypeExpectedToolSkipped,
		ArtifactDataTypeCarryForwardWorkingContext,
		ArtifactDataTypeCarryForwardEvidenceDigest,
		ArtifactDataTypeCarryForwardSourceIndex,
		ArtifactDataTypeCarryForwardContinuity,
		ArtifactDataTypeCarryForwardSessionCursor,
		ArtifactDataTypePresentationEvidence,
		ArtifactDataTypeKnowledgeReadiness,
		ArtifactDataTypeIdentityAllocation,
		ArtifactDataTypeIdentityLineage,
		ArtifactDataTypeActivationRecord,
		ArtifactDataTypeDAGOperation,
		ArtifactDataTypeVFSOperation,
		ArtifactDataTypeToolRuntimeExecution,
		ArtifactDataTypeKnowledgeOperation,
		ArtifactDataTypeMemoryContinuity,
		ArtifactDataTypeDocumentOperation,
		ArtifactDataTypeGuardianDecision,
		ArtifactDataTypeProviderGatewayCall,
		ArtifactDataTypeExternalAdapterEvent,
	} {
		if !strings.Contains(body, dataType) {
			t.Fatalf("ARTIFACTS_AND_VALIDATIONS.md missing data type %s", dataType)
		}
	}
}

func TestArtifactsAndValidationsDocsCrossReferenceDeltaActions(t *testing.T) {
	root := repoRootForClaimsTest(t)
	for _, doc := range []string{
		"docs/CLAIMS.md",
		"docs/CLAIMS_AND_DELTAS.md",
		"docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md",
	} {
		body := readDocForClaimsTest(t, root, doc)
		for _, action := range phaseZeroArtifactValidationDeltaActions() {
			if !strings.Contains(body, action) {
				t.Fatalf("%s missing artifact/validation delta action %s", doc, action)
			}
		}
		for _, action := range phaseZeroPropagationDeltaActions() {
			if !strings.Contains(body, action) {
				t.Fatalf("%s missing propagation delta action %s", doc, action)
			}
		}
	}
}

func TestArtifactsAndValidationsDocsMatchStatusChangeFields(t *testing.T) {
	root := repoRootForClaimsTest(t)
	docs := []string{
		readDocForClaimsTest(t, root, "docs/CLAIMS.md"),
		readDocForClaimsTest(t, root, "docs/ARTIFACTS_AND_VALIDATIONS.md"),
	}
	statusChange := reflect.TypeOf(StatusChange{})
	for i := 0; i < statusChange.NumField(); i++ {
		field := statusChange.Field(i)
		if field.PkgPath != "" {
			continue
		}
		for _, body := range docs {
			if !strings.Contains(body, field.Name) {
				t.Fatalf("docs missing StatusChange field %s", field.Name)
			}
		}
	}
}

func artifactStatusDocNames() []string {
	out := make([]string, 0, len(KnownArtifactStatuses()))
	for _, status := range KnownArtifactStatuses() {
		out = append(out, "artifact."+string(status))
	}
	return out
}

func validationStatusDocNames() []string {
	out := make([]string, 0, len(KnownValidationLifecycleStatuses()))
	for _, status := range KnownValidationLifecycleStatuses() {
		out = append(out, "validation."+string(status))
	}
	return out
}

func phaseZeroArtifactValidationDeltaActions() []string {
	out := append(artifactStatusDocNames(), validationStatusDocNames()...)
	return out
}

func phaseZeroPropagationDeltaActions() []string {
	return []string{
		"testament.validated",
		"testament.validation_failed",
		"testament.validation_incomplete",
		"testament.validation_errored",
		"claim.satisfied",
		"claim.validation_failed",
		"claim.validation_incomplete",
		"claim.validation_errored",
	}
}

func repoRootForClaimsTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func readDocForClaimsTest(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
