package claims

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ClaimLifecycleStatus is the fine-grained workflow state for a claim.
// ClaimStatus remains the coarse compatibility projection used by older
// board queries; lifecycle status is the authoritative claims/testaments
// lifecycle defined in docs/CLAIMS_AND_TESTAMENTS_LIFECYCLE.md.
type ClaimLifecycleStatus string

const (
	ClaimLifecycleGenerated                      ClaimLifecycleStatus = "generated"
	ClaimLifecycleGenerationFailed               ClaimLifecycleStatus = "generation_failed"
	ClaimLifecyclePosted                         ClaimLifecycleStatus = "posted"
	ClaimLifecyclePostFailed                     ClaimLifecycleStatus = "post_failed"
	ClaimLifecycleReceived                       ClaimLifecycleStatus = "received"
	ClaimLifecycleReceiptFailed                  ClaimLifecycleStatus = "receipt_failed"
	ClaimLifecycleProgressed                     ClaimLifecycleStatus = "progressed"
	ClaimLifecycleProgressFailed                 ClaimLifecycleStatus = "progress_failed"
	ClaimLifecycleTestamentGenerated             ClaimLifecycleStatus = "testament_generated"
	ClaimLifecycleTestamentGenerationFailed      ClaimLifecycleStatus = "testament_generation_failed"
	ClaimLifecycleTestamentAcknowledged          ClaimLifecycleStatus = "testament_acknowledged"
	ClaimLifecycleTestamentAcknowledgementFailed ClaimLifecycleStatus = "testament_acknowledgement_failed"
	ClaimLifecycleValidating                     ClaimLifecycleStatus = "validating"
	ClaimLifecycleSatisfied                      ClaimLifecycleStatus = "satisfied"
	ClaimLifecycleValidationIncomplete           ClaimLifecycleStatus = "validation_incomplete"
	ClaimLifecycleValidationFailed               ClaimLifecycleStatus = "validation_failed"
	ClaimLifecycleValidationErrored              ClaimLifecycleStatus = "validation_errored"
)

// TestamentLifecycleStatus is the fine-grained workflow state for a
// testament. It is independent from the parent claim's lifecycle while
// remaining linked through relations.
type TestamentLifecycleStatus string

const (
	TestamentLifecycleGenerated            TestamentLifecycleStatus = "generated"
	TestamentLifecyclePosted               TestamentLifecycleStatus = "posted"
	TestamentLifecycleReceived             TestamentLifecycleStatus = "received"
	TestamentLifecycleValidating           TestamentLifecycleStatus = "validating"
	TestamentLifecycleValidationIncomplete TestamentLifecycleStatus = "validation_incomplete"
	TestamentLifecycleValidationFailed     TestamentLifecycleStatus = "validation_failed"
	TestamentLifecycleValidationErrored    TestamentLifecycleStatus = "validation_errored"
	TestamentLifecycleValidated            TestamentLifecycleStatus = "validated"
)

func KnownClaimLifecycleStatuses() []ClaimLifecycleStatus {
	return []ClaimLifecycleStatus{
		ClaimLifecycleGenerated,
		ClaimLifecycleGenerationFailed,
		ClaimLifecyclePosted,
		ClaimLifecyclePostFailed,
		ClaimLifecycleReceived,
		ClaimLifecycleReceiptFailed,
		ClaimLifecycleProgressed,
		ClaimLifecycleProgressFailed,
		ClaimLifecycleTestamentGenerated,
		ClaimLifecycleTestamentGenerationFailed,
		ClaimLifecycleTestamentAcknowledged,
		ClaimLifecycleTestamentAcknowledgementFailed,
		ClaimLifecycleValidating,
		ClaimLifecycleSatisfied,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored,
	}
}

func KnownTestamentLifecycleStatuses() []TestamentLifecycleStatus {
	return []TestamentLifecycleStatus{
		TestamentLifecycleGenerated,
		TestamentLifecyclePosted,
		TestamentLifecycleReceived,
		TestamentLifecycleValidating,
		TestamentLifecycleValidationIncomplete,
		TestamentLifecycleValidationFailed,
		TestamentLifecycleValidationErrored,
		TestamentLifecycleValidated,
	}
}

func (s ClaimLifecycleStatus) Valid() bool {
	switch s {
	case ClaimLifecycleGenerated,
		ClaimLifecycleGenerationFailed,
		ClaimLifecyclePosted,
		ClaimLifecyclePostFailed,
		ClaimLifecycleReceived,
		ClaimLifecycleReceiptFailed,
		ClaimLifecycleProgressed,
		ClaimLifecycleProgressFailed,
		ClaimLifecycleTestamentGenerated,
		ClaimLifecycleTestamentGenerationFailed,
		ClaimLifecycleTestamentAcknowledged,
		ClaimLifecycleTestamentAcknowledgementFailed,
		ClaimLifecycleValidating,
		ClaimLifecycleSatisfied,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored:
		return true
	default:
		return false
	}
}

func (s TestamentLifecycleStatus) Valid() bool {
	switch s {
	case TestamentLifecycleGenerated,
		TestamentLifecyclePosted,
		TestamentLifecycleReceived,
		TestamentLifecycleValidating,
		TestamentLifecycleValidationIncomplete,
		TestamentLifecycleValidationFailed,
		TestamentLifecycleValidationErrored,
		TestamentLifecycleValidated:
		return true
	default:
		return false
	}
}

func (s ClaimLifecycleStatus) IsTerminal() bool {
	switch s {
	case ClaimLifecycleGenerationFailed,
		ClaimLifecyclePostFailed,
		ClaimLifecycleReceiptFailed,
		ClaimLifecycleProgressFailed,
		ClaimLifecycleTestamentGenerationFailed,
		ClaimLifecycleTestamentAcknowledgementFailed,
		ClaimLifecycleSatisfied,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored:
		return true
	default:
		return false
	}
}

func (s TestamentLifecycleStatus) IsTerminal() bool {
	switch s {
	case TestamentLifecycleValidationIncomplete,
		TestamentLifecycleValidationFailed,
		TestamentLifecycleValidationErrored,
		TestamentLifecycleValidated:
		return true
	default:
		return false
	}
}

func (s ClaimLifecycleStatus) IsFailure() bool {
	return strings.HasSuffix(string(s), "_failed") ||
		s == ClaimLifecycleValidationIncomplete ||
		s == ClaimLifecycleValidationErrored
}

func (s TestamentLifecycleStatus) IsFailure() bool {
	return s == TestamentLifecycleValidationIncomplete ||
		s == TestamentLifecycleValidationFailed ||
		s == TestamentLifecycleValidationErrored
}

func IsClaimLifecycleActionable(status ClaimLifecycleStatus) bool {
	return status == ClaimLifecyclePosted
}

func IsClaimLifecycleProgress(status ClaimLifecycleStatus) bool {
	return status == ClaimLifecycleProgressed
}

func IsClaimLifecycleReceipt(status ClaimLifecycleStatus) bool {
	return status == ClaimLifecycleReceived || status == ClaimLifecycleTestamentAcknowledged
}

func IsClaimLifecycleValidation(status ClaimLifecycleStatus) bool {
	switch status {
	case ClaimLifecycleValidating,
		ClaimLifecycleSatisfied,
		ClaimLifecycleValidationIncomplete,
		ClaimLifecycleValidationFailed,
		ClaimLifecycleValidationErrored:
		return true
	default:
		return false
	}
}

func CanTransitionClaimLifecycle(from, to ClaimLifecycleStatus) bool {
	if to == "" || !to.Valid() {
		return false
	}
	if from == "" {
		return to == ClaimLifecycleGenerated || to == ClaimLifecycleGenerationFailed
	}
	if from == to {
		return true
	}
	if from.IsTerminal() {
		return false
	}
	switch from {
	case ClaimLifecycleGenerated:
		return oneOfClaimLifecycle(to, ClaimLifecyclePosted, ClaimLifecyclePostFailed, ClaimLifecycleGenerationFailed)
	case ClaimLifecyclePosted:
		return oneOfClaimLifecycle(to, ClaimLifecycleReceived, ClaimLifecycleReceiptFailed, ClaimLifecycleProgressed, ClaimLifecycleProgressFailed, ClaimLifecycleTestamentGenerated, ClaimLifecycleTestamentGenerationFailed)
	case ClaimLifecycleReceived:
		return oneOfClaimLifecycle(to, ClaimLifecycleProgressed, ClaimLifecycleProgressFailed, ClaimLifecycleTestamentGenerated, ClaimLifecycleTestamentGenerationFailed)
	case ClaimLifecycleProgressed:
		return oneOfClaimLifecycle(to, ClaimLifecycleProgressed, ClaimLifecycleProgressFailed, ClaimLifecycleTestamentGenerated, ClaimLifecycleTestamentGenerationFailed)
	case ClaimLifecycleTestamentGenerated:
		return oneOfClaimLifecycle(to, ClaimLifecycleTestamentAcknowledged, ClaimLifecycleTestamentAcknowledgementFailed, ClaimLifecycleValidating, ClaimLifecycleSatisfied, ClaimLifecycleValidationIncomplete, ClaimLifecycleValidationFailed, ClaimLifecycleValidationErrored)
	case ClaimLifecycleTestamentAcknowledged:
		return oneOfClaimLifecycle(to, ClaimLifecycleValidating, ClaimLifecycleValidationErrored)
	case ClaimLifecycleValidating:
		return oneOfClaimLifecycle(to, ClaimLifecycleSatisfied, ClaimLifecycleValidationIncomplete, ClaimLifecycleValidationFailed, ClaimLifecycleValidationErrored)
	default:
		return false
	}
}

func CanTransitionTestamentLifecycle(from, to TestamentLifecycleStatus) bool {
	if to == "" || !to.Valid() {
		return false
	}
	if from == "" {
		return to == TestamentLifecycleGenerated
	}
	if from == to {
		return true
	}
	if from.IsTerminal() {
		return false
	}
	switch from {
	case TestamentLifecycleGenerated:
		return to == TestamentLifecyclePosted
	case TestamentLifecyclePosted:
		return to == TestamentLifecycleReceived || to == TestamentLifecycleValidating
	case TestamentLifecycleReceived:
		return to == TestamentLifecycleValidating
	case TestamentLifecycleValidating:
		return oneOfTestamentLifecycle(to, TestamentLifecycleValidated, TestamentLifecycleValidationIncomplete, TestamentLifecycleValidationFailed, TestamentLifecycleValidationErrored)
	default:
		return false
	}
}

func oneOfClaimLifecycle(status ClaimLifecycleStatus, allowed ...ClaimLifecycleStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func oneOfTestamentLifecycle(status TestamentLifecycleStatus, allowed ...TestamentLifecycleStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func (s *ClaimLifecycleStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	status := ClaimLifecycleStatus(strings.TrimSpace(raw))
	if status != "" && !status.Valid() {
		return fmt.Errorf("unknown claim lifecycle status %q", raw)
	}
	*s = status
	return nil
}

func (s *TestamentLifecycleStatus) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	status := TestamentLifecycleStatus(strings.TrimSpace(raw))
	if status != "" && !status.Valid() {
		return fmt.Errorf("unknown testament lifecycle status %q", raw)
	}
	*s = status
	return nil
}
