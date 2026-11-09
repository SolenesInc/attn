package main

import (
	"strings"
	"time"
)

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func firstLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	return line
}

func shortStamp(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.Local().Format("2006-01-02 15:04")
}

func conversationDate(stamp string) string {
	if at, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		return at.Format("2006-01-02")
	}
	return stamp
}
