package utils

import (
	"fmt"
	"time"
)

func GenerateDownloadID(url string) string {
	// Simple hash-based ID generation
	return fmt.Sprintf("dl_%d", time.Now().UnixNano())
}
