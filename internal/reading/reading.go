package reading

import (
	"context"
	"strings"

	"github.com/victorarias/attn/docs/guide"
	"github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/prompts"
)

func Handlers() map[string]command.Handler {
	return map[string]command.Handler{
		"skill": command.Typed(runSkill),
		"guide": command.Typed(runGuide),
	}
}

func runSkill(_ context.Context, _ command.Caller, args command.SkillArgs) (any, error) {
	reference := strings.TrimSpace(args.Reference)
	switch {
	case args.List && reference != "":
		return nil, command.Refuse(command.CodeListAndReference, "{list} and {reference} are mutually exclusive; pass one")
	case args.List:
		return strings.Join(agent.SkillReferenceNames(), "\n") + "\n", nil
	case reference == "":
		return prompts.RenderText("session", "attn-map", prompts.Values{}) + "\n", nil
	}
	content, err := agent.SkillFile("references/" + strings.TrimSuffix(reference, ".md") + ".md")
	if err != nil {
		return nil, command.Refuse(command.CodeNoSuchReference, "no bundled reference %q; bundled references: %s", reference, strings.Join(agent.SkillReferenceNames(), ", "))
	}
	return string(content), nil
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
