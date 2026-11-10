//go:build darwin && cgo

package diag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

type footprintReport struct {
	Processes []struct {
		PID        int                          `json:"pid"`
		Categories map[string]footprintCategory `json:"categories"`
	} `json:"processes"`
	Errors json.RawMessage `json:"errors"`
}

type footprintCategory struct {
	Dirty   uint64 `json:"dirty"`
	Swapped uint64 `json:"swapped"`
}

func (c footprintCategory) footprintBytes() uint64 { return c.Dirty + c.Swapped }

func (r RoleMemories) SplitWebContent(ctx context.Context) error {
	content := r[RoleWebKitContent]
	if content == nil {
		return nil
	}
	report, err := readFootprint(ctx, content.pids)
	if err != nil {
		return err
	}
	if len(report.Processes) != len(content.pids) {
		return fmt.Errorf("footprint measured %d of the %d WebContent processes %v, errors %s", len(report.Processes), len(content.pids), content.pids, report.Errors)
	}
	split := WebContentSplit{}
	for _, process := range report.Processes {
		categories := process.Categories
		split.WebKitMallocBytes += categories["WebKit malloc"].footprintBytes()
		split.OwnedGraphicsBytes += categories["Owned physical footprint (unmapped) (graphics)"].footprintBytes()
		split.GigacageBytes += categories["JS VM Gigacage"].footprintBytes()
	}
	content.Split = &split
	return nil
}

func readFootprint(ctx context.Context, pids []int) (footprintReport, error) {
	dir, err := os.MkdirTemp("", "attn-footprint-")
	if err != nil {
		return footprintReport{}, err
	}
	defer os.RemoveAll(dir)
	output := filepath.Join(dir, "footprint.json")
	args := []string{"-j", output}
	for _, pid := range pids {
		args = append(args, "-p", strconv.Itoa(pid))
	}
	if message, err := exec.CommandContext(ctx, "/usr/bin/footprint", args...).CombinedOutput(); err != nil {
		return footprintReport{}, fmt.Errorf("footprint %v: %w: %s", args, err, message)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return footprintReport{}, err
	}
	var report footprintReport
	if err := json.Unmarshal(data, &report); err != nil {
		return footprintReport{}, fmt.Errorf("reading footprint's report: %w", err)
	}
	return report, nil
}
