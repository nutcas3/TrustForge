package server

import "fmt"

// Command is sent from host to guest agent via vsock
type Command struct {
	Type         string `json:"type"`
	SubmissionID string `json:"submission_id"`
}

// ValidateCommand validates the incoming command for security
func ValidateCommand(cmd *Command) error {
	// Validate command type
	switch cmd.Type {
	case "RUN", "PING":
		// Valid command types
	default:
		return fmt.Errorf("unknown command type: %q", cmd.Type)
	}

	// Validate submission ID for RUN commands
	if cmd.Type == "RUN" {
		if cmd.SubmissionID == "" {
			return fmt.Errorf("submission_id cannot be empty")
		}
		if len(cmd.SubmissionID) > 100 {
			return fmt.Errorf("submission_id too long (max 100 chars)")
		}
		// Only allow alphanumeric characters, hyphens, and underscores
		for _, r := range cmd.SubmissionID {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				return fmt.Errorf("submission_id contains invalid characters")
			}
		}
	}

	return nil
}
