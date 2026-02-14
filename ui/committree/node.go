package committree

import "time"

// TreeNode represents a single commit in the tree visualization.
type TreeNode struct {
	Hash       string
	ShortHash  string
	Subject    string
	Branch     string   // Branch name if a ref points here (empty otherwise).
	Additions  int
	Deletions  int
	Author     string
	AuthorTime time.Time
	Parents    []string // Parent hashes for edge drawing.
	IsMerge    bool
}

// BranchNode represents a single branch in the branch tree view.
type BranchNode struct {
	Name              string    // Short branch name (e.g., "main").
	Hash              string    // Tip commit full hash.
	ShortHash         string    // Tip commit abbreviated hash.
	Subject           string    // Tip commit message first line.
	AuthorTime        time.Time // Tip commit author time.
	CreatedTime       time.Time // Branch ref creation time (from reflog).
	IsHead            bool      // True if this is the current branch (HEAD).
	CommitCount       int       // Number of commits reachable from tip.
	CommitCountCapped bool      // True if CommitCount hit the counting limit.
	BehindCount       int       // Commits in default branch not reachable from tip.
	BehindCountCapped bool      // True if BehindCount hit the counting limit.
	Parent            string    // Parent branch name (empty for root/default).
}

// relativeTime formats a time.Time as a human-readable relative duration.
// Thresholds derived from conventional breakpoints:
//
//	<60s  = "now"
//	<60m  = minutes
//	<24h  = hours
//	<30d  = days
//	<12mo = months
//	else  = years
func relativeTime(t time.Time) string {
	d := time.Since(t)

	const (
		minute = time.Minute
		hour   = time.Hour
		day    = hour * 24
		month  = day * 30
		year   = day * 365
	)

	switch {
	case d < minute:
		return "now"
	case d < hour:
		return itoa(int(d/minute)) + "m ago"
	case d < day:
		return itoa(int(d/hour)) + "h ago"
	case d < month:
		return itoa(int(d/day)) + "d ago"
	case d < year:
		return itoa(int(d/month)) + "mo ago"
	default:
		return itoa(int(d/year)) + "y ago"
	}
}

// itoa converts a non-negative int to its decimal string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	// max int64 has 19 digits; 20 bytes is sufficient.
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
