package testhistory

import (
	"sort"
	"strings"
)

type CostKey struct {
	Lane    Lane
	Machine string
}

type Costs struct {
	key      CostKey
	goSetup  []int64
	packages map[string][]int64
	units    map[string][]int64
}

func BuildCosts(receipts []Receipt, key CostKey) Costs {
	costs := &Costs{key: key, packages: map[string][]int64{}, units: map[string][]int64{}}
	for _, receipt := range receipts {
		exit := receipt.Command.Exit
		if receipt.Lane != key.Lane || receipt.Diagnostics.MachineClass != key.Machine || receipt.Command.WallMS == nil || exit == nil || exit.Code != 0 || exit.Signal != "" {
			continue
		}
		if receipt.Runner == RunnerGo {
			costs.addGo(receipt)
		} else {
			costs.addCommand(receipt)
		}
	}
	return *costs
}

func wholeUnits(runner Runner, argv []string) bool {
	for i, arg := range argv {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		flag, value, inline := strings.Cut(strings.TrimPrefix(arg, "-"), "=")
		flag = strings.TrimPrefix(flag, "-")
		if !inline && i+1 < len(argv) {
			value = argv[i+1]
		}
		switch {
		case runner == RunnerGo && (flag == "skip" || flag == "run" && strings.Contains(value, "/")):
			return false
		case runner == RunnerVitest && (flag == "t" || flag == "testNamePattern"):
			return false
		case runner == RunnerPlaywright && (flag == "g" || flag == "grep" || flag == "grep-invert"):
			return false
		}
	}
	return true
}

func (c *Costs) addGo(receipt Receipt) {
	if !wholeUnits(RunnerGo, receipt.Command.Argv) {
		return
	}
	packageElapsed := map[string]int64{}
	testElapsed := map[string]int64{}
	var tests []Result
	for _, result := range receipt.Results {
		if result.Cached || result.Attempt > 1 {
			return
		}
		if result.Case != "" || result.ElapsedMS == nil {
			continue
		}
		fields := strings.Fields(result.Unit)
		switch {
		case len(fields) == 2 && result.Outcome == Pass:
			packageElapsed[result.Unit] = *result.ElapsedMS
		case len(fields) == 3:
			testElapsed["go "+fields[1]] += *result.ElapsedMS
			if result.Outcome == Pass {
				tests = append(tests, result)
			}
		}
	}
	if len(packageElapsed) == 0 {
		return
	}
	var longest int64
	for pkg, elapsed := range packageElapsed {
		longest = max(longest, elapsed)
		c.packages[pkg] = append(c.packages[pkg], max(0, elapsed-testElapsed[pkg]))
	}
	c.goSetup = append(c.goSetup, max(0, *receipt.Command.WallMS-longest))
	for _, test := range tests {
		if _, ran := packageElapsed[goPackageUnit(test.Unit)]; ran {
			c.units[test.Unit] = append(c.units[test.Unit], *test.ElapsedMS)
		}
	}
}

func (c *Costs) addCommand(receipt Receipt) {
	if !wholeUnits(receipt.Runner, receipt.Command.Argv) {
		return
	}
	unit := ""
	for _, result := range receipt.Results {
		if result.Case != "" {
			continue
		}
		if unit != "" && result.Unit != unit {
			return
		}
		unit = result.Unit
		if result.Outcome != Pass {
			return
		}
	}
	if unit != "" {
		c.units[unit] = append(c.units[unit], *receipt.Command.WallMS)
	}
}

func goPackageUnit(unit string) string {
	fields := strings.Fields(unit)
	return fields[0] + " " + fields[1]
}

func median(samples []int64) (int64, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2], true
}

func (c Costs) Priced(unit string) bool {
	if _, ok := median(c.units[unit]); !ok {
		return false
	}
	if UnitRunner(unit) != RunnerGo {
		return true
	}
	_, setup := median(c.goSetup)
	_, overhead := median(c.packages[goPackageUnit(unit)])
	return setup && overhead
}

func (c Costs) Samples(unit string) int { return len(c.units[unit]) }

func (c Costs) Estimate(units []string) (int64, []string) {
	var total int64
	var unpriced []string
	packageTime := map[string]int64{}
	for _, unit := range units {
		if !c.Priced(unit) {
			unpriced = append(unpriced, unit)
			continue
		}
		elapsed, _ := median(c.units[unit])
		if UnitRunner(unit) != RunnerGo {
			total += elapsed
			continue
		}
		pkg := goPackageUnit(unit)
		if _, started := packageTime[pkg]; !started {
			packageTime[pkg], _ = median(c.packages[pkg])
		}
		packageTime[pkg] += elapsed
	}
	if len(packageTime) > 0 {
		setup, _ := median(c.goSetup)
		var longest int64
		for _, elapsed := range packageTime {
			longest = max(longest, elapsed)
		}
		total += setup + longest
	}
	return total, unpriced
}
