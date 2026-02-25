package logger

import (
	"log"
	"os"
	"strings"
	"sync"
)

// Level represents log severity level
type Level int

const (
	LevelInfo Level = iota
	LevelDebug
)

var (
	currentLevel Level
	once         sync.Once
)

// initLevel reads LOG_LEVEL env var once at startup
func initLevel() {
	env := os.Getenv("LOG_LEVEL")
	switch strings.ToLower(env) {
	case "debug":
		currentLevel = LevelDebug
	default:
		currentLevel = LevelInfo
	}
}

// IsDebug returns true if debug logging is enabled
func IsDebug() bool {
	once.Do(initLevel)
	return currentLevel >= LevelDebug
}

// Debugf logs a formatted message if debug level is enabled
func Debugf(format string, args ...interface{}) {
	if IsDebug() {
		log.Printf(format, args...)
	}
}

// Debugln logs a message if debug level is enabled
func Debugln(args ...interface{}) {
	if IsDebug() {
		log.Println(args...)
	}
}
