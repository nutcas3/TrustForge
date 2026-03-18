package executor

import (
	"strconv"
	"strings"
	"time"
)

// Result represents the outcome of verifier execution
type Result struct {
	SubmissionID string  `json:"submission_id"`
	ExitCode     int     `json:"exit_code"`
	Stdout       string  `json:"stdout"`
	Stderr       string  `json:"stderr"`
	Score        float64 `json:"score"`
	ElapsedMs    int64   `json:"elapsed_ms"`
	MemUsedKB    int64   `json:"mem_used_kb"`
	TimedOut     bool    `json:"timed_out"`
}

// ParseScore extracts "SCORE: <float>" from the verifier's stdout.
// Convention: verifier.py must print this as its final output line.
func ParseScore(stdout string) float64 {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if after, ok := strings.CutPrefix(line, "SCORE:"); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(after), 64); err == nil {
				return clamp(f, 0.0, 1.0)
			}
		}
	}
	return 0.0
}

// clamp constrains a value between min and max
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// NewResult creates a new result with the given submission ID and start time
func NewResult(submissionID string) *Result {
	return &Result{
		SubmissionID: submissionID,
	}
}

// Finalize calculates timing and processes the result
func (r *Result) Finalize(start time.Time) {
	r.ElapsedMs = time.Since(start).Milliseconds()
	r.Score = ParseScore(r.Stdout)
}
