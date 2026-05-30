#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
claims_doc="$root/docs/CLAIMS_AND_INFRASTRUCTURE.md"
ops_doc="$root/docs/CLAIMS_OPERATIONS.md"

required_claims_sections=(
  "19.10 Phase 9 - UI, Agent Adoption, and Legacy Coexistence"
  "19.11 Phase 10 - Recovery, Cancellation, Shutdown, and Replay Audits"
  "19.12 Phase 11 - Observability, CI Enforcement, and Production Rollout"
  "mockery drift"
  "Critical shadow diffs block promotion to enabled"
  "Remaining planned inventory entries have owner claims and deadlines"
)

required_ops_terms=(
  "recovery"
  "cancellation"
  "shutdown"
  "rollout"
  "mockery"
)

for needle in "${required_claims_sections[@]}"; do
  grep -Fq "$needle" "$claims_doc"
done

for needle in "${required_ops_terms[@]}"; do
  grep -Fiq "$needle" "$ops_doc"
done
