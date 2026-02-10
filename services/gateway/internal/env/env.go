package env

import "os"

// Str returns the value of the environment variable key, or fallback if unset/empty.
func Str(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}
