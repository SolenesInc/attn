package daemon

import (
	"slices"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) askSeedMentions(askID, profileID string) ([]protocol.AskSeedMention, error) {
	stored, err := d.askShownTo(askID, profileID)
	if err != nil {
		return nil, err
	}
	all, err := d.rememberedGardenSeeds()
	if err != nil {
		return nil, err
	}
	wordsInAsk := map[string]bool{}
	for _, word := range strings.FieldsFunc(stored.Subject+"\n"+stored.Markdown, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		wordsInAsk[word] = true
	}
	ids := map[string]bool{}
	slugs := map[string]string{}
	for _, seed := range all.seeds {
		if seed.ProfileID != profileID {
			continue
		}
		if wordsInAsk[seed.ID] {
			ids[seed.ID] = true
		}
		if seed.StepSlug != "" && wordsInAsk[seed.StepSlug] {
			if _, exists := slugs[seed.StepSlug]; exists {
				slugs[seed.StepSlug] = ""
			} else {
				slugs[seed.StepSlug] = seed.ID
			}
		}
	}
	names := map[string]string{}
	for word := range wordsInAsk {
		if ids[word] {
			names[word] = word
		} else if id := slugs[word]; id != "" {
			names[word] = id
		}
	}
	words := make([]string, 0, len(names))
	for word := range names {
		words = append(words, word)
	}
	slices.Sort(words)
	mentions := make([]protocol.AskSeedMention, 0, len(words))
	for _, word := range words {
		mentions = append(mentions, protocol.AskSeedMention{Text: word, SeedID: names[word]})
	}
	return mentions, nil
}

func (d *Daemon) handleAskSeedMentions(client *wsClient, msg *protocol.AskSeedMentionsMessage) {
	result := protocol.AskSeedMentionsResultMessage{Event: protocol.EventAskSeedMentionsResult, RequestID: msg.RequestID, Mentions: []protocol.AskSeedMention{}}
	mentions, err := d.askSeedMentions(strings.TrimSpace(msg.AskID), client.selectedProfile())
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Mentions = mentions
	}
	d.sendToClient(client, result)
}
