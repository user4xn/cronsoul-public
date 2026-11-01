package runner

import (
	"math/rand"
	"strings"
	"time"
)

// RetryDelay is bounded exponential backoff (5, 10, 20, 40, then 60 seconds).
func RetryDelay(attempt int, fixed bool, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	seconds := 5
	if !fixed {
		for i := 1; i < attempt && seconds < 60; i++ {
			seconds *= 2
		}
		if seconds > 60 {
			seconds = 60
		}
	}
	factor := 1 + jitter*(rand.Float64()*2-1)
	return time.Duration(float64(seconds) * factor * float64(time.Second))
}
func Transient(err error, status int, retryOn []string) bool {
	if len(retryOn) == 0 {
		retryOn = []string{"timeout", "network_error", "http_5xx"}
	}
	for _, rule := range retryOn {
		switch strings.ToLower(rule) {
		case "http_5xx":
			if status >= 500 && status <= 599 {
				return true
			}
		case "timeout":
			if err != nil && (strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")) {
				return true
			}
		case "network_error":
			if err != nil {
				return true
			}
		default:
			if len(rule) == 3 && status == int(rule[0]-'0')*100+int(rule[1]-'0')*10+int(rule[2]-'0') {
				return true
			}
		}
	}
	return false
}
