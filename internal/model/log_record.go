package model

import "time"

// LogRecord represents a single log entry matched across groups.
type LogRecord struct {
	Timestamp time.Time
	LogGroup  string
	LogStream string
	Message   string
}
