package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

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

func (d *Daemon) handleReviewStart(ctx context.Context, conn net.Conn, msg *protocol.ReviewStartMessage) {
	if !d.on(setting.CodeReviewEnabled) {
		d.sendError(conn, reviewOff())
		return
	}
	document := filepath.Clean(msg.Document)
	result, err := gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (protocol.ReviewStartResult, error) {
		return review.Start(ctx, client, document, msg.Repository, protocol.Deref(msg.Base))
	})
	if err != nil {
		d.sendError(conn, fmt.Sprintf("review start: %v", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, ReviewStartResult: &result})
	d.warmCodeNavigation(document)
}

func (d *Daemon) handleReviewCheck(ctx context.Context, conn net.Conn, msg *protocol.ReviewCheckMessage) {
	if !d.on(setting.CodeReviewEnabled) {
		d.sendError(conn, reviewOff())
		return
	}
	document := filepath.Clean(msg.Document)
	content, err := readMarkdownFile(document)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("review check: %v", err))
		return
	}
	bundle, err := d.resolveReview(ctx, document, content)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("review check: %v", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, ReviewCheckResult: &protocol.ReviewCheckResult{Problems: bundle.Problems}})
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

func (d *Daemon) handleReviewAnswer(ctx context.Context, conn net.Conn, msg *protocol.ReviewAnswerMessage) {
	if !d.on(setting.CodeReviewEnabled) {
		d.sendError(conn, reviewOff())
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		d.sendError(conn, "review answer: the answer is empty. Pass it with -m \"<answer>\"")
		return
	}
	if !filepath.IsAbs(msg.Document) {
		d.sendError(conn, fmt.Sprintf("review answer: the document path must be absolute: %s", msg.Document))
		return
	}
	key := store.HostPath{Path: filepath.Clean(msg.Document)}
	if origin, remote := OriginOf(ctx); remote {
		key.Host = origin.Host
	}
	generation, err := d.store.AnswerMarkdownQuestion(key, msg.AnnotationID, text, time.Now())
	if errors.Is(err, store.ErrNoQuestion) {
		d.sendError(conn, fmt.Sprintf("review answer: %s has no open question %s. The user may have removed it or sent the review", key.Path, msg.AnnotationID))
		return
	}
	if err != nil {
		d.sendError(conn, fmt.Sprintf("review answer: %v", err))
		return
	}
	d.annotationsChanged(key, generation)
	d.sendOK(conn)
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
