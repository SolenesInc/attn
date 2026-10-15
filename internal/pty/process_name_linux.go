package pty

import (
	"os"
	"strconv"
	"strings"
)

func foregroundProcessName(pid int) string {
	name, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	return strings.TrimSpace(string(name))
}
