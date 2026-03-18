package worker

import (
	"github.com/nutcas3/trustforge/pkg/models"
)

// WorkerJob represents a job to be processed by the worker pool
type WorkerJob = models.WorkerJob

// JobStatus represents the current status of a job
type JobStatus string

const (
	JobStatusPending    JobStatus = "PENDING"
	JobStatusSandboxing JobStatus = "SANDBOXING"
	JobStatusRunning    JobStatus = "RUNNING"
	JobStatusRedTeam    JobStatus = "RED_TEAM"
	JobStatusTrusted    JobStatus = "TRUSTED"
	JobStatusRejected   JobStatus = "REJECTED"
	JobStatusFailed     JobStatus = "FAILED"
)

// Evaluation stages
const (
	StageSandboxPrep = "sandbox prep"
	StageVMExecution  = "vm execution"
	StageRedTeam      = "red-team analysis"
	StageScoring      = "scoring"
)
