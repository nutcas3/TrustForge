package constants

import "time"

const (
	VMBootTimeout = 10 * time.Second
	VMResumeTimeout = 5 * time.Second
	SnapshotWarmupTimeout = 15 * time.Second
)

const (
	MaxVerifierExecutionTime = 25 * time.Second
	MaxOutputBytes = 1024 * 1024 // 1MB
	MaxVerifierCodeSize = 100 * 1024 // 100KB
)

const (
	CPULimitSoft = 20
	
	CPULimitHard = 25
	
	MemoryLimitBytes = 512 * 1024 * 1024 // 512MB
	
	MaxFileSizeBytes = 10 * 1024 * 1024 // 10MB
	
	MaxOpenFiles = 64
	
	MaxProcesses = 32
)

const (
	VSockHostCID = 2
	
	VSockGuestCID = 3
	
	VSockPort = 52
	
	VSockReadyPort = 53
)

const (
	DefaultPageLimit = 50
	
	MaxPageLimit = 100
)
