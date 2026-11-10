package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"

	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/review"
	"github.com/victorarias/attn/internal/setting"
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
