package reading

import (
	"context"
	"strings"

	"github.com/victorarias/attn/docs/guide"
	"github.com/victorarias/attn/internal/attnskill"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/prompts"
)

func Handlers(commandRules func(name string) (string, bool)) map[string]command.Handler {
	return map[string]command.Handler{
		"skill": command.Typed(func(_ context.Context, _ command.Caller, args command.SkillArgs) (any, error) {
			return runSkill(args, commandRules)
		}),
		"guide": command.Typed(runGuide),
	}
}

func runSkill(args command.SkillArgs, commandRules func(name string) (string, bool)) (any, error) {
	reference, name := strings.TrimSpace(args.Reference), strings.TrimSpace(args.Command)
	switch {
	case name != "" && (args.List || reference != ""):
		return nil, command.Usage("pass one of {reference}, {list} and {command}")
	case name != "":
		rules, ok := commandRules(name)
		if !ok {
			return nil, command.Refuse(command.CodeNoSuchCommand, "%s is not a command or a family of commands", name)
		}
		return rules, nil
	case args.List && reference != "":
		return nil, command.Refuse(command.CodeListAndReference, "{list} and {reference} are mutually exclusive; pass one")
	case args.List:
		return strings.Join(attnskill.SkillReferenceNames(), "\n") + "\n", nil
	case reference == "":
		return command.Teaching(prompts.RenderText("session", "attn-map", prompts.Values{}) + "\n"), nil
	}
	content, err := attnskill.SkillFile("references/" + strings.TrimSuffix(reference, ".md") + ".md")
	if err != nil {
		return nil, command.Refuse(command.CodeNoSuchReference, "no bundled reference %q; bundled references: %s", reference, strings.Join(attnskill.SkillReferenceNames(), ", "))
	}
	return command.Teaching(content), nil
}

func runGuide(_ context.Context, _ command.Caller, args command.GuideArgs) (any, error) {
	if args.Topic == "" {
		return string(guide.Index()), nil
	}
	page, err := guide.Topic(args.Topic)
	if err != nil {
		return nil, command.Refuse(command.CodeNoSuchTopic, "%s", err.Error())
	}
	return string(page), nil
}
