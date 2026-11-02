package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/cloudlink"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/protocol/gateway"
	"github.com/victorarias/attn/internal/store"
)

// gatewayImageChunkBytes keeps a chunk's base64 (54,616 bytes) and its fields inside one gateway message.
const gatewayImageChunkBytes = 40 << 10

const gatewayAsksAPI = 3

func (d *Daemon) gatewaySendAsks(conn *gatewayConn) error {
	if conn.api < gatewayAsksAPI {
		return nil
	}
	ids := []string{}
	frames := map[string][]byte{}
	for _, shown := range d.asks() {
		if !askShownIn(conn.device.ProfileID, shown.profileID) {
			continue
		}
		frame, err := gatewayAskFrame(shown.wire)
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
	if err := gatewaySend(conn, gateway.GatewayAsks{Event: "asks", Ids: ids}); err != nil {
		return err
	}
	conn.sentAsks, conn.sentAskIDs = frames, ids
	return nil
}

func gatewayAskFrame(wire protocol.Ask) ([]byte, error) {
	out := gatewayAsk(wire)
	frame, err := json.Marshal(gateway.GatewayAskEvent{Event: "ask", Ask: out})
	if err != nil || len(frame) <= cloudlink.MaxMessageBytes {
		return frame, err
	}
	out.Parts = []gateway.GatewayAskPart{}
	out.TooLargeBytes = protocol.Ptr(len(frame))
	out.MaxFrameBytes = protocol.Ptr(cloudlink.MaxMessageBytes)
	return json.Marshal(gateway.GatewayAskEvent{Event: "ask", Ask: out})
}

func gatewayAsk(wire protocol.Ask) gateway.GatewayAsk {
	out := gateway.GatewayAsk{
		ID:         wire.ID,
		SessionID:  string(wire.SessionID),
		Subject:    wire.Subject,
		Parts:      make([]gateway.GatewayAskPart, 0, len(wire.Parts)),
		CreatedAt:  parseAskTime(wire.CreatedAt),
		SeedSlug:   wire.SeedSlug,
		SeedTitle:  wire.SeedTitle,
		CrewMember: wire.CrewMember,
		Chief:      wire.Chief,
	}
	for _, part := range wire.Parts {
		out.Parts = append(out.Parts, gateway.GatewayAskPart{Markdown: part.Markdown, Question: gatewayAskQuestion(part.Question)})
	}
	if wire.Reply != nil {
		out.Reply = &gateway.GatewayAskReply{
			Answers:    gatewayAskAnswers(wire.Reply.Answers),
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

func gatewayAskQuestion(question *protocol.AskQuestion) *gateway.GatewayAskQuestion {
	if question == nil {
		return nil
	}
	out := &gateway.GatewayAskQuestion{
		Key:         question.Key,
		Kind:        gateway.GatewayAskQuestionKind(question.Kind),
		Prompt:      question.Prompt,
		Context:     question.Context,
		Choices:     make([]gateway.GatewayAskChoice, 0, len(question.Choices)),
		Recommended: question.Recommended,
	}
	for _, choice := range question.Choices {
		out.Choices = append(out.Choices, gateway.GatewayAskChoice{Label: choice.Label, Description: choice.Description})
	}
	return out
}

func gatewayAskAnswers(answers []protocol.AskAnswer) []gateway.GatewayAskAnswer {
	out := make([]gateway.GatewayAskAnswer, 0, len(answers))
	for _, answer := range answers {
		out = append(out, gateway.GatewayAskAnswer{Key: answer.Key, Picked: answer.Picked, Other: answer.Other, Text: answer.Text})
	}
	return out
}

func (d *Daemon) gatewayAnswerAsk(conn *gatewayConn, command gateway.GatewayAskAnswerCommand) error {
	answers := make([]protocol.AskAnswer, 0, len(command.Answers))
	for _, answer := range command.Answers {
		answers = append(answers, protocol.AskAnswer{Key: answer.Key, Picked: answer.Picked, Other: answer.Other, Text: answer.Text})
	}
	result := gateway.GatewayAskAnswerResult{Event: "ask_answer_result", RequestID: command.RequestID, AskID: command.AskID}
	err := d.answerAsk(&protocol.AskAnswerMessage{AskID: command.AskID, Answers: answers, Note: command.Note}, conn.device.ProfileID)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	}
	return gatewaySend(conn, result)
}

func (d *Daemon) gatewayAskImage(conn *gatewayConn, command gateway.GatewayAskImage) error {
	mimeType, data, err := d.gatewayAskImageBytes(conn.device.ProfileID, strings.TrimSpace(command.AskID), command.Src)
	if err == nil && (command.Offset < 0 || command.Offset > len(data)) {
		err = fmt.Errorf("offset %d is outside %s, which holds %d bytes", command.Offset, command.Src, len(data))
	}
	if err != nil {
		return gatewaySend(conn, gateway.GatewayAskImageError{Event: "ask_image_error", RequestID: command.RequestID, Message: err.Error()})
	}
	chunk := data[command.Offset:min(len(data), command.Offset+gatewayImageChunkBytes)]
	return gatewaySend(conn, gateway.GatewayAskImageChunk{
		Event:      "ask_image_chunk",
		RequestID:  command.RequestID,
		Offset:     command.Offset,
		Size:       len(data),
		MimeType:   mimeType,
		DataBase64: base64.StdEncoding.EncodeToString(chunk),
	})
}

func (d *Daemon) gatewayAskImageBytes(profileID, askID, src string) (string, []byte, error) {
	stored, err := d.store.GetAsk(askID)
	if errors.Is(err, store.ErrAskGone) {
		return "", nil, fmt.Errorf("ask %s is gone", askID)
	}
	if err != nil {
		return "", nil, err
	}
	imagePath, ok := ask.ResolveImage(stored.BaseDir, src)
	if !ok {
		return "", nil, fmt.Errorf("%s is not a local image with one of the extensions an ask shows", src)
	}
	return d.askImage(askID, imagePath, profileID)
}
