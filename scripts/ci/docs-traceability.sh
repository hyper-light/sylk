#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ops_doc="$root/docs/CLAIMS_OPERATIONS.md"
infra_doc="$root/docs/CLAIMS_AND_INFRASTRUCTURE.md"

require_in_file() {
  local file="$1"
  local needle="$2"
  grep -Fq "$needle" "$file"
}

require_phase_item() {
  local heading="$1"
  local section
  section="$(awk -v h="$heading" '
    $0 ~ "^#### " && index($0, h) { in_section=1; next }
    in_section && $0 ~ "^#### " { exit }
    in_section { print }
  ' "$ops_doc")"
  [[ -n "$section" ]]
  grep -Fq "Description:" <<<"$section"
  grep -Fq "File references:" <<<"$section"
  grep -Fq "Acceptance criteria:" <<<"$section"
  grep -Fq "Test cases:" <<<"$section"
  grep -Fiq "Integration with mockery" <<<"$section"
  grep -Fiq "E2E with mockery" <<<"$section"
  grep -Fiq "Race" <<<"$section"
}

required_phase_items=(
  "12.7.1 Implement the telemetry catalog"
  "12.7.2 Add operator skills and repair commands"
  "12.7.3 Validate runbooks against simulated incidents"
  "12.8.1 Add claims operations analyzers"
  "12.8.2 Enforce lifecycle and delta partitions"
  "12.8.3 Add CI commands and documentation gates"
  "12.9.1 Make UI operations state claims-driven"
  "12.9.2 Complete agent adoption and compatibility cleanup"
  "12.9.3 Roll out with feature gates, shadow mode, and rollback"
  "12.9.4 Final production readiness review"
)

for item in "${required_phase_items[@]}"; do
  require_phase_item "$item"
done

required_ops_terms=(
  "claims_board_transitions_total"
  "claims_delta_emitted_total"
  "claims_validation_handler_duration_seconds"
  "claims_boot_phase_completed_total"
  "claims-infra-ci"
  "mockery drift"
)

for needle in "${required_ops_terms[@]}"; do
  require_in_file "$ops_doc" "$needle"
done

required_claims_sections=(
  "19.10 Phase 9 - UI, Agent Adoption, and Legacy Coexistence"
  "19.11 Phase 10 - Recovery, Cancellation, Shutdown, and Replay Audits"
  "19.12 Phase 11 - Observability, CI Enforcement, and Production Rollout"
)

for needle in "${required_claims_sections[@]}"; do
  require_in_file "$infra_doc" "$needle"
done
