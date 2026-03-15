package models

import (
	"time"
)

type SubmissionStatus string

const (
	StatusPending    SubmissionStatus = "PENDING"
	StatusSandboxing SubmissionStatus = "SANDBOXING"
	StatusRunning    SubmissionStatus = "RUNNING"
	StatusRedTeam    SubmissionStatus = "RED_TEAM"
	StatusTrusted    SubmissionStatus = "TRUSTED"
	StatusRejected   SubmissionStatus = "REJECTED"
	StatusFailed     SubmissionStatus = "FAILED"
)

type Submission struct {
	ID            string           `json:"id" db:"id"`
	ContributorID string           `json:"contributor_id" db:"contributor_id"`
	Status        SubmissionStatus `json:"status" db:"status"`
	VerifierCode  string           `json:"verifier_code" db:"verifier_code"`
	ModelOutput   string           `json:"model_output" db:"model_output"`
	Score         *float64         `json:"score,omitempty" db:"score"`
	RedTeamReport *RedTeamReport   `json:"red_team_report,omitempty"`
	CreatedAt     time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at" db:"updated_at"`
	CompletedAt   *time.Time       `json:"completed_at,omitempty" db:"completed_at"`
}

type RedTeamReport struct {
	AnalyzedAt      time.Time        `json:"analyzed_at"`
	RiskScore       float64          `json:"risk_score"` // 0.0 (safe) to 1.0 (malicious)
	Findings        []Finding        `json:"findings"`
	RewardHacks     []RewardHack     `json:"reward_hacks"`
	Recommendation  string           `json:"recommendation"`
	PassedRedTeam   bool             `json:"passed_red_team"`
}

type Finding struct {
	Severity    string `json:"severity"` // LOW, MEDIUM, HIGH, CRITICAL
	Category    string `json:"category"`
	Description string `json:"description"`
	LineNumber  int    `json:"line_number,omitempty"`
}

type RewardHack struct {
	Pattern     string  `json:"pattern"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
}

type VMSnapshot struct {
	ID           string    `json:"id"`
	MemFilePath  string    `json:"mem_file_path"`
	SnapFilePath string    `json:"snap_file_path"`
	BaseImageID  string    `json:"base_image_id"`
	CreatedAt    time.Time `json:"created_at"`
	PythonReady  bool      `json:"python_ready"`
}

type EvaluationResult struct {
	SubmissionID string  `json:"submission_id"`
	ExitCode     int     `json:"exit_code"`
	Stdout       string  `json:"stdout"`
	Stderr       string  `json:"stderr"`
	Score        float64 `json:"score"`
	ElapsedMs    int64   `json:"elapsed_ms"`
	MemUsedKB    int64   `json:"mem_used_kb"`
	TimedOut     bool    `json:"timed_out"`
}

type WorkerJob struct {
	Submission *Submission
	ResultCh   chan<- *EvaluationResult
	ErrCh      chan<- error
}
