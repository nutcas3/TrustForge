package server

import (
	"sync"
	"time"
)

const (
	MaxConnectionsPerMinute = 10
)

// Rate limiting state
var (
	connectionTimes []time.Time
	connMutex       sync.Mutex
)

// CheckRateLimit returns true if the connection should be allowed
func CheckRateLimit() bool {
	connMutex.Lock()
	defer connMutex.Unlock()
	
	now := time.Now()
	// Remove connections older than 1 minute
	cutoff := now.Add(-time.Minute)
	validConnections := []time.Time{}
	for _, t := range connectionTimes {
		if t.After(cutoff) {
			validConnections = append(validConnections, t)
		}
	}
	
	// Check if we've exceeded the limit
	if len(validConnections) >= MaxConnectionsPerMinute {
		return false
	}
	
	// Add current connection time
	connectionTimes = append(validConnections, now)
	return true
}
