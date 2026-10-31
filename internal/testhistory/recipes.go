package testhistory

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

type Group struct {
	Name    string
	Pattern *regexp.Regexp
	Units   []string
}

type Recipe struct {
	Unit  string
	Line  int
	Group string
}

type RecipeList struct {
	Groups  []Group
	Recipes []Recipe
}

var landingRunners = map[Runner]bool{RunnerGo: true, RunnerVitest: true, RunnerPlaywright: true}

func ParseRecipeList(text string) (RecipeList, error) {
	var list RecipeList
	scanner := bufio.NewScanner(strings.NewReader(text))
	for line := 1; scanner.Scan(); line++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if fields[0] == "group" {
			if len(fields) != 3 {
				return RecipeList{}, fmt.Errorf("line %d: want \"group <name> <pattern>\"", line)
			}
			pattern, err := regexp.Compile(fields[2])
			if err != nil {
				return RecipeList{}, fmt.Errorf("line %d: %w", line, err)
			}
			list.Groups = append(list.Groups, Group{Name: fields[1], Pattern: pattern})
			continue
		}
		if len(list.Groups) == 0 {
			return RecipeList{}, fmt.Errorf("line %d: %q comes before any group", line, scanner.Text())
		}
		unit := strings.Join(fields, " ")
		if err := checkLandingUnit(unit); err != nil {
			return RecipeList{}, fmt.Errorf("line %d: %w", line, err)
		}
		group := &list.Groups[len(list.Groups)-1]
		group.Units = append(group.Units, unit)
		list.Recipes = append(list.Recipes, Recipe{Unit: unit, Line: line, Group: group.Name})
	}
	return list, scanner.Err()
}

func checkLandingUnit(unit string) error {
	fields := strings.Fields(unit)
	runner := UnitRunner(unit)
	switch {
	case !landingRunners[runner]:
		return fmt.Errorf("%q: landing tests run go, vitest and playwright units", unit)
	case runner == RunnerGo && len(fields) != 3:
		return fmt.Errorf("%q: want \"go <package> <TestName>\"", unit)
	case runner != RunnerGo && len(fields) != 2:
		return fmt.Errorf("%q: want \"%s <file>\"", unit, runner)
	}
	return nil
}

func UnitRunner(unit string) Runner {
	runner, _, _ := strings.Cut(unit, " ")
	return Runner(runner)
}

func (list RecipeList) SelectedGroups(changed []string) []Group {
	var groups []Group
	for _, group := range list.Groups {
		for _, path := range changed {
			if group.Pattern.MatchString(path) {
				groups = append(groups, group)
				break
			}
		}
	}
	return groups
}

func RecipeCommand(unit string) string {
	if checkLandingUnit(unit) != nil {
		return ""
	}
	fields := strings.Fields(unit)
	switch UnitRunner(unit) {
	case RunnerGo:
		return fmt.Sprintf("./scripts/test-go.sh --packages -count=1 -run '^%s$' %s", fields[2], fields[1])
	case RunnerVitest:
		return "pnpm --dir app exec vitest run " + fields[1]
	default:
		return "pnpm --dir app exec playwright test " + fields[1]
	}
}
