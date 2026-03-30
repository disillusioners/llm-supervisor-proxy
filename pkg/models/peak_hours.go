package models

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Time format regex: HH:MM where HH is 00-23 and MM is 00-59
var timeFormatRegex = regexp.MustCompile(`^(\d{2}):(\d{2})$`)

// UTC offset regex: required + or -, 1-2 digit integer, optional 1-2 digit decimal
// Examples: +7, -5, +5.5, -3.75
var utcOffsetRegex = regexp.MustCompile(`^[+-]\d{1,2}(\.\d{1,2})?$`)

// parseUTCOffset parses a UTC offset string like "+7", "-5", "+5.5" into float64 hours.
func parseUTCOffset(offset string) (float64, error) {
	if offset == "" {
		return 0, nil
	}

	offset = strings.TrimSpace(offset)
	if !utcOffsetRegex.MatchString(offset) {
		return 0, fmt.Errorf("invalid UTC offset format: %s", offset)
	}

	// Parse the float value
	value, err := strconv.ParseFloat(offset, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid UTC offset value: %s", offset)
	}

	// Validate reasonable bounds (-12 to +14 covers all valid timezone offsets)
	if value < -12 || value > 14 {
		return 0, fmt.Errorf("UTC offset out of range (-12 to +14): %s", offset)
	}

	return value, nil
}

// parseTime parses a time string in HH:MM format and returns hour and minute as integers.
func parseTime(s string) (h, m int, err error) {
	if s == "" {
		return 0, 0, fmt.Errorf("time string cannot be empty")
	}
	if err := validateTimeFormat(s); err != nil {
		return 0, 0, err
	}

	h, err = strconv.Atoi(s[:2])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hour: %s", s[:2])
	}

	m, err = strconv.Atoi(s[3:])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minute: %s", s[3:])
	}

	return h, m, nil
}

// isWithinWindow checks if a given current time (in minutes from midnight) falls
// within the window defined by start and end times (also in minutes from midnight).
// The window is half-open [start, end) - includes start, excludes end.
// Handles cross-midnight windows (e.g., 22:00 to 06:00).
func isWithinWindow(current, start, end int) bool {
	if start <= end {
		// Normal window: e.g., 09:00 to 17:00
		return current >= start && current < end
	}

	// Cross-midnight window: e.g., 22:00 to 06:00
	// Current is in window if it's >= start OR < end
	return current >= start || current < end
}

// validateTimeFormat validates that a string is in HH:MM format with valid values.
func validateTimeFormat(s string) error {
	if s == "" {
		return nil // Empty is allowed (optional field)
	}

	if !timeFormatRegex.MatchString(s) {
		return fmt.Errorf("invalid time format (expected HH:MM): %s", s)
	}

	h, err := strconv.Atoi(s[:2])
	if err != nil {
		return fmt.Errorf("invalid hour: %s", s[:2])
	}

	m, err := strconv.Atoi(s[3:])
	if err != nil {
		return fmt.Errorf("invalid minute: %s", s[3:])
	}

	if h < 0 || h > 23 {
		return fmt.Errorf("hour out of range (0-23): %d", h)
	}

	if m < 0 || m > 59 {
		return fmt.Errorf("minute out of range (0-59): %d", m)
	}

	return nil
}

// validateUTCOffset validates that a string is a valid UTC offset.
func validateUTCOffset(s string) error {
	if s == "" {
		return nil // Empty is allowed
	}

	s = strings.TrimSpace(s)
	if !utcOffsetRegex.MatchString(s) {
		return fmt.Errorf("invalid UTC offset format (expected +N, -N, or decimal): %s", s)
	}

	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid UTC offset value: %s", s)
	}

	if value < -12 || value > 14 {
		return fmt.Errorf("UTC offset out of valid range (-12 to +14): %s", s)
	}

	return nil
}

// timeToMinutes converts hour and minute to minutes from midnight.
func timeToMinutes(h, m int) int {
	return h*60 + m
}

// ResolvePeakHourModel returns the peak hour model name if the current time falls
// within the configured peak hour window. Returns empty string if:
// - PeakHourEnabled is false
// - Model is not internal (peak hours only apply to internal upstream)
// - Current time is outside the peak hour window
// - PeakHourModel is empty
func (m *ModelConfig) ResolvePeakHourModel() string {
	// Check if peak hours are enabled
	if !m.PeakHourEnabled {
		return ""
	}

	// Peak hours only apply to internal upstream models
	if !m.Internal {
		return ""
	}

	// Must have a peak hour model specified
	if m.PeakHourModel == "" {
		return ""
	}

	// Parse UTC offset (default to 0 if not specified)
	utcOffset, err := parseUTCOffset(m.PeakHourTimezone)
	if err != nil {
		return ""
	}

	// Parse start and end times
	startH, startM, err := parseTime(m.PeakHourStart)
	if err != nil {
		return ""
	}

	endH, endM, err := parseTime(m.PeakHourEnd)
	if err != nil {
		return ""
	}

	// Convert to minutes from midnight
	startMinutes := timeToMinutes(startH, startM)
	endMinutes := timeToMinutes(endH, endM)

	// Calculate current local time
	now := time.Now().UTC()
	// Apply UTC offset to get local time minutes
	offsetMinutes := int(utcOffset * 60)
	currentMinutes := (now.Hour()*60 + now.Minute() + offsetMinutes) % (24 * 60)
	if currentMinutes < 0 {
		currentMinutes += 24 * 60
	}

	// Check if current time is within window
	if !isWithinWindow(currentMinutes, startMinutes, endMinutes) {
		return ""
	}

	return m.PeakHourModel
}
