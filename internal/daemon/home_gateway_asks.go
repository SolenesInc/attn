package daemon

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/homegateway"
	"github.com/victorarias/attn/internal/store"
)

// homeGatewayImageChunkBytes keeps a chunk's base64 (54,616 bytes) and its fields inside one Home Gateway message.
const homeGatewayImageChunkBytes = 40 << 10

const homeGatewayAsksAPI = 3

const homeGatewayDecodedImageSourcesAPI = 5

func (d *Daemon) homeGatewaySendAsks(conn *homeGatewayConn) error {
	if conn.api < homeGatewayAsksAPI {
		return nil
	}
	ids := []string{}
	frames := map[string][]byte{}
	for _, shown := range d.asks() {
		if !askShownIn(conn.phone.ProfileID, shown.profileID) {
			continue
		}
		frame, err := homeGatewayAskFrame(shown.wire)
		if err != nil {
			return err
		}
		ids = append(ids, shown.wire.ID)
		frames[shown.wire.ID] = frame
	}
	changed := conn.sentAskIDs == nil || !slices.Equal(conn.sentAskIDs, ids)
	for _, id := range ids {
		if bytes.Equal(conn.sentAsks[id], frames[id]) {
			continue
		}
		if err := conn.stream.Send(frames[id]); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	if err := homeGatewaySend(conn, homegateway.HomeGatewayAsks{Event: "asks", Ids: ids}); err != nil {
		return err
	}
	conn.sentAsks, conn.sentAskIDs = frames, ids
	return nil
}

func homeGatewayAskFrame(wire protocol.Ask) ([]byte, error) {
	out := homeGatewayAsk(wire)
	frame, err := homeGatewayMarshal(homegateway.HomeGatewayAskEvent{Event: "ask", Ask: out})
	if err != nil || len(frame) <= cloudlink.MaxMessageBytes {
		return frame, err
	}
	return homeGatewayMarshal(homegateway.HomeGatewayAskEvent{Event: "ask", Ask: tooLargeAsk(out, len(frame))})
}

// homeGatewayTooLargeSubjectRunes keeps the stand-in for an ask too large for a frame within a few KiB, whatever escaping
// its subject needs.
const homeGatewayTooLargeSubjectRunes = 200

func tooLargeAsk(full homegateway.HomeGatewayAsk, size int) homegateway.HomeGatewayAsk {
	subject := []rune(full.Subject)
	if len(subject) > homeGatewayTooLargeSubjectRunes {
		subject = append(subject[:homeGatewayTooLargeSubjectRunes-1], '…')
	}
	out := homegateway.HomeGatewayAsk{
		ID:            full.ID,
		SessionID:     full.SessionID,
		Subject:       string(subject),
		Parts:         []homegateway.HomeGatewayAskPart{},
		CreatedAt:     full.CreatedAt,
		SeedSlug:      full.SeedSlug,
		CrewMember:    full.CrewMember,
		TooLargeBytes: protocol.Ptr(size),
		MaxFrameBytes: protocol.Ptr(cloudlink.MaxMessageBytes),
	}
	if full.Reply != nil {
		out.Reply = &homegateway.HomeGatewayAskReply{Answers: []homegateway.HomeGatewayAskAnswer{}, AnsweredAt: full.Reply.AnsweredAt}
	}
	return out
}

func homeGatewayAsk(wire protocol.Ask) homegateway.HomeGatewayAsk {
	out := homegateway.HomeGatewayAsk{
		ID:         wire.ID,
		SessionID:  string(wire.SessionID),
		Subject:    wire.Subject,
		Parts:      make([]homegateway.HomeGatewayAskPart, 0, len(wire.Parts)),
		CreatedAt:  parseAskTime(wire.CreatedAt),
		SeedSlug:   wire.SeedSlug,
		SeedTitle:  wire.SeedTitle,
		CrewMember: wire.CrewMember,
	}
	for _, part := range wire.Parts {
		out.Parts = append(out.Parts, homegateway.HomeGatewayAskPart{Markdown: part.Markdown, Question: homeGatewayAskQuestion(part.Question)})
	}
	if wire.Reply != nil {
		out.Reply = &homegateway.HomeGatewayAskReply{
			Answers:    homeGatewayAskAnswers(wire.Reply.Answers),
			Note:       wire.Reply.Note,
			AnsweredAt: parseAskTime(wire.Reply.AnsweredAt),
		}
	}
	return out
}

func parseAskTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed.UTC()
}

func homeGatewayAskQuestion(question *protocol.AskQuestion) *homegateway.HomeGatewayAskQuestion {
	if question == nil {
		return nil
	}
	out := &homegateway.HomeGatewayAskQuestion{
		Key:         question.Key,
		Kind:        homegateway.HomeGatewayAskQuestionKind(question.Kind),
		Prompt:      question.Prompt,
		Context:     question.Context,
		Choices:     make([]homegateway.HomeGatewayAskChoice, 0, len(question.Choices)),
		Recommended: question.Recommended,
	}
	for _, choice := range question.Choices {
		out.Choices = append(out.Choices, homegateway.HomeGatewayAskChoice{Label: choice.Label, Description: choice.Description})
	}
	return out
}

func homeGatewayAskAnswers(answers []protocol.AskAnswer) []homegateway.HomeGatewayAskAnswer {
	out := make([]homegateway.HomeGatewayAskAnswer, 0, len(answers))
	for _, answer := range answers {
		out = append(out, homegateway.HomeGatewayAskAnswer{Key: answer.Key, Picked: answer.Picked, Other: answer.Other, Text: answer.Text})
	}
	return out
}

func (d *Daemon) homeGatewayAnswerAsk(conn *homeGatewayConn, command homegateway.HomeGatewayAskAnswerCommand) error {
	answers := make([]protocol.AskAnswer, 0, len(command.Answers))
	for _, answer := range command.Answers {
		answers = append(answers, protocol.AskAnswer{Key: answer.Key, Picked: answer.Picked, Other: answer.Other, Text: answer.Text})
	}
	result := homegateway.HomeGatewayAskAnswerResult{Event: "ask_answer_result", RequestID: command.RequestID, AskID: command.AskID}
	err := d.answerAsk(&protocol.AskAnswerMessage{AskID: command.AskID, Answers: answers, Note: command.Note}, conn.phone.ProfileID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return homeGatewaySend(conn, result)
}

func (d *Daemon) homeGatewayAskImage(conn *homeGatewayConn, command homegateway.HomeGatewayAskImage) error {
	mimeType, data, err := d.homeGatewayAskImageBytes(conn, strings.TrimSpace(command.AskID), command.Src)
	if err == nil && (command.Offset < 0 || command.Offset > len(data)) {
		err = fmt.Errorf("offset %d is outside %s, which holds %d bytes", command.Offset, command.Src, len(data))
	}
	if err != nil {
		return homeGatewaySend(conn, homegateway.HomeGatewayAskImageError{Event: "ask_image_error", RequestID: command.RequestID, Message: err.Error()})
	}
	chunk := data[command.Offset:min(len(data), command.Offset+homeGatewayImageChunkBytes)]
	return homeGatewaySend(conn, homegateway.HomeGatewayAskImageChunk{
		Event:      "ask_image_chunk",
		RequestID:  command.RequestID,
		Offset:     command.Offset,
		Size:       len(data),
		MimeType:   mimeType,
		DataBase64: base64.StdEncoding.EncodeToString(chunk),
	})
}

func (d *Daemon) homeGatewayAskImageBytes(conn *homeGatewayConn, askID, src string) (string, []byte, error) {
	stored, err := d.askShownTo(askID, conn.phone.ProfileID)
	if err != nil {
		return "", nil, err
	}
	if conn.api < homeGatewayDecodedImageSourcesAPI {
		if src, err = partlyDecodedImageSource(stored, src); err != nil {
			return "", nil, err
		}
	}
	return storedAskImage(stored, src)
}

func partlyDecodedImageSource(stored store.Ask, src string) (string, error) {
	decoded := html.UnescapeString(src)
	if decoded == src {
		return src, nil
	}
	parts, err := ask.Parse(stored.Markdown)
	if err != nil {
		return "", err
	}
	shown := ask.ImagePaths(parts, stored.BaseDir)
	asSent, sentShown := shownImage(shown, stored.BaseDir, src)
	asDecoded, decodedShown := shownImage(shown, stored.BaseDir, decoded)
	switch {
	case sentShown && decodedShown:
		return "", fmt.Errorf("ask %s shows both %s and %s, and this phone's attn sends %s for either; update attn on the phone to tell them apart", stored.ID, asSent, asDecoded, src)
	case decodedShown:
		return decoded, nil
	}
	return src, nil
}

func shownImage(shown []string, baseDir, src string) (string, bool) {
	imagePath, ok := ask.ResolveImage(baseDir, src)
	return imagePath, ok && slices.Contains(shown, imagePath)
}

func storedAskImage(stored store.Ask, src string) (string, []byte, error) {
	imagePath, ok := ask.ResolveImage(stored.BaseDir, src)
	if !ok {
		return "", nil, fmt.Errorf("%s is not a local image with one of the extensions an ask shows", src)
	}
	return askImageCopy(stored.ID, imagePath)
}
