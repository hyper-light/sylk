
Test Priority Score Calculation:

Score = (Coverage × 0.30) +
        (Complexity × 0.20) +
        (Changes × 0.25) +
        (BugHistory × 0.15) +
        (Speed × 0.10)

Where:
- Coverage: Unique lines covered by this test
- Complexity: Cyclomatic complexity of covered code
- Changes: Recency and frequency of changes to covered code
- BugHistory: Number of bugs found by this test historically
- Speed: Inverse of execution time (faster = higher)

All factors normalized to 0-1 range before weighting.
