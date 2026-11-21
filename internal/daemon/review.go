package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/command"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/review"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
)

var reviewTask = gitTask{Kind: gitTaskReview, Lane: gitInteractive}

func reviewOff() string {
	return fmt.Sprintf("attn review is off: code review is experimental. Turn it on with `attn settings set %s true`.", setting.CodeReviewEnabled.Key)
}

func (d *Daemon) resolveReview(ctx context.Context, document, content string) (protocol.ReviewBundle, error) {
	return gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (protocol.ReviewBundle, error) {
		return review.Resolve(ctx, client, document, content), nil
	})
}

func (d *Daemon) reviewCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"review start":  command.Typed(d.runReviewStart),
		"review check":  command.Typed(d.runReviewCheck),
		"review answer": command.Typed(d.runReviewAnswer),
	}
}

func reviewOffRefusal() error {
	return command.Refuse(command.CodeReviewOff, "%s", reviewOff())
}

func (d *Daemon) runReviewStart(ctx context.Context, c command.Caller, a command.ReviewStartArgs) (any, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return nil, reviewOffRefusal()
	}
	repository, err := d.callerRepository(ctx, c, a.Repository)
	if err != nil {
		return nil, err
	}
	document, _, err := d.callerPath(ctx, c, callerDestination("document", a.Document))
	if err != nil {
		return nil, err
	}
	document = filepath.Clean(document)
	result, err := gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (protocol.ReviewStartResult, error) {
		return review.Start(ctx, client, document, repository, a.Base)
	})
	if err != nil {
		return nil, fmt.Errorf("review start: %w", err)
	}
	return command.Then{Result: result, After: func() { d.warmCodeNavigation(document) }}, nil
}

func (d *Daemon) runReviewCheck(ctx context.Context, c command.Caller, a command.ReviewCheckArgs) (any, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return nil, reviewOffRefusal()
	}
	document, found, err := d.callerPath(ctx, c, callerFile("document", a.Document))
	if err != nil {
		return nil, err
	}
	document = filepath.Clean(document)
	content, err := readMarkdownFile(document)
	if err != nil {
		return found.explain(nil, fmt.Errorf("review check: %w", err))
	}
	bundle, err := d.resolveReview(ctx, document, content)
	if err != nil {
		return nil, fmt.Errorf("review check: %w", err)
	}
	return protocol.ReviewCheckResult{Problems: bundle.Problems}, nil
}

func (d *Daemon) reviewDocumentHere(document string) (string, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return "", errors.New(reviewOff())
	}
	content, err := readMarkdownFile(filepath.Clean(document))
	if err != nil {
		return "", err
	}
	if !review.IsReview(content) {
		return "", fmt.Errorf("%s is not a review document: its frontmatter has no `attn: review`", document)
	}
	return content, nil
}

func (d *Daemon) readReviewFile(ctx context.Context, msg protocol.ReviewFileMessage) (protocol.ReviewFileResultMessage, error) {
	content, err := d.reviewDocumentHere(msg.Document)
	if err != nil {
		return protocol.ReviewFileResultMessage{}, err
	}
	if filepath.IsAbs(msg.Path) && d.codeNavOutside.allowed(filepath.Clean(msg.Document), filepath.Clean(msg.Path)) {
		return review.OutsideFile(filepath.Clean(msg.Path))
	}
	return gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (protocol.ReviewFileResultMessage, error) {
		return review.File(ctx, client, content, msg.Path)
	})
}

func (d *Daemon) searchReview(ctx context.Context, msg protocol.ReviewSearchMessage) (protocol.ReviewSearchResultMessage, error) {
	content, err := d.reviewDocumentHere(msg.Document)
	if err != nil {
		return protocol.ReviewSearchResultMessage{}, err
	}
	return gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (protocol.ReviewSearchResultMessage, error) {
		return review.Search(ctx, client, content, msg.Query)
	})
}

func (d *Daemon) handleReviewFileWS(client *wsClient, msg *protocol.ReviewFileMessage) {
	d.life.Go("handleReviewFileWS", func() {
		result, err := Call[protocol.ReviewFileResultMessage](d.life.Context(), d.router(), *msg)
		if err != nil {
			result = protocol.ReviewFileResultMessage{Event: protocol.EventReviewFileResult, Error: protocol.Ptr(d.tileDocumentError(protocol.Deref(msg.Host), err).Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}

func (d *Daemon) handleReviewSearchWS(client *wsClient, msg *protocol.ReviewSearchMessage) {
	d.life.Go("handleReviewSearchWS", func() {
		result, err := Call[protocol.ReviewSearchResultMessage](d.life.Context(), d.router(), *msg)
		if err != nil {
			result = protocol.ReviewSearchResultMessage{Event: protocol.EventReviewSearchResult, Error: protocol.Ptr(d.tileDocumentError(protocol.Deref(msg.Host), err).Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}

func (d *Daemon) markReviewSent(ctx context.Context, msg protocol.ReviewSentMessage) (struct{}, error) {
	content, err := d.reviewDocumentHere(msg.Document)
	if err != nil {
		return struct{}{}, err
	}
	document := filepath.Clean(msg.Document)
	return gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (struct{}, error) {
		return struct{}{}, review.MarkSent(ctx, client, document, content)
	})
}

func (d *Daemon) runReviewAnswer(ctx context.Context, c command.Caller, a command.ReviewAnswerArgs) (any, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return nil, reviewOffRefusal()
	}
	if a.Document == "" {
		return nil, command.Usage("{document} <document> is required")
	}
	text := strings.TrimSpace(a.Text)
	if text == "" {
		return nil, command.Usage("the answer is empty. Pass it with {text} \"<answer>\"")
	}
	document, found, err := d.callerPath(ctx, c, callerFile("document", a.Document))
	if err != nil {
		return nil, err
	}
	key := store.HostPath{Path: filepath.Clean(document)}
	if origin, remote := OriginOf(ctx); remote {
		key.Host = origin.Host
	}
	generation, err := d.store.AnswerMarkdownQuestion(key, a.Question, text, time.Now())
	if errors.Is(err, store.ErrNoQuestion) {
		return found.explain(nil, command.Refuse(command.CodeNoQuestion, "%s has no open question %s. The user may have removed it or sent the review", key.Path, a.Question))
	}
	if err != nil {
		return nil, fmt.Errorf("review answer: %w", err)
	}
	d.annotationsChanged(key, generation)
	return protocol.ReviewAnswerResult{Question: a.Question, Document: key.Path}, nil
}

func (d *Daemon) editReviewFile(_ context.Context, msg protocol.ReviewEditMessage) (protocol.ReviewEditResultMessage, error) {
	content, err := d.reviewDocumentHere(msg.Document)
	if err != nil {
		return protocol.ReviewEditResultMessage{}, err
	}
	return review.Edit(content, msg, d.fsStore)
}

func (d *Daemon) handleReviewEditWS(client *wsClient, msg *protocol.ReviewEditMessage) {
	d.life.Go("handleReviewEditWS", func() {
		result, err := Call[protocol.ReviewEditResultMessage](d.life.Context(), d.router(), *msg)
		if err != nil {
			result = protocol.ReviewEditResultMessage{Event: protocol.EventReviewEditResult, Error: protocol.Ptr(d.tileDocumentError(protocol.Deref(msg.Host), err).Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}
