package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/codenav"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
)

type codeNavSetupKey struct {
	host       string
	repository string
	language   string
}

// codeNavSetups are the sessions setting up a language server, so every review
// of the repository links to the one running, and the setups still launching,
// so a second request does not start a second agent.
type codeNavSetups struct {
	mu        sync.Mutex
	sessions  map[codeNavSetupKey]protocol.SessionID
	launching map[codeNavSetupKey]bool
}

func (s *codeNavSetups) begin(key codeNavSetupKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.launching[key] {
		return false
	}
	if s.launching == nil {
		s.launching = map[codeNavSetupKey]bool{}
	}
	s.launching[key] = true
	return true
}

func (s *codeNavSetups) end(key codeNavSetupKey, session protocol.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.launching, key)
	if session == "" {
		return
	}
	if s.sessions == nil {
		s.sessions = map[codeNavSetupKey]protocol.SessionID{}
	}
	s.sessions[key] = session
}

// withSetups is the review as the app sees it: each language that has a setup
// running names its session.
func (s *codeNavSetups) withSetups(host string, review *protocol.ReviewBundle) *protocol.ReviewBundle {
	if review == nil || len(review.Navigation) == 0 {
		return review
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var navigation []protocol.ReviewNavigation
	for i, entry := range review.Navigation {
		session, ok := s.sessions[codeNavSetupKey{host: host, repository: review.Repository, language: entry.Language}]
		if !ok {
			continue
		}
		if navigation == nil {
			navigation = append([]protocol.ReviewNavigation(nil), review.Navigation...)
		}
		navigation[i].SetupSessionID = protocol.Ptr(session)
	}
	if navigation == nil {
		return review
	}
	shown := *review
	shown.Navigation = navigation
	return &shown
}

func (d *Daemon) handleCodeNavSetupWS(client *wsClient, msg *protocol.CodeNavSetupMessage) {
	d.life.Go("handleCodeNavSetupWS", func() {
		result := protocol.CodeNavSetupResultMessage{Event: protocol.EventCodeNavSetupResult, RequestID: msg.RequestID}
		if session, err := d.startCodeNavSetup(d.life.Context(), msg); err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Success, result.SessionID = true, protocol.Ptr(session)
		}
		d.sendToClient(client, &result)
	})
}

// startCodeNavSetup starts an agent on the review's machine that sets up the
// language's server: the user's delegation fallback when they configured one,
// else the harness they picked for new sessions. It answers once the agent
// runs, so a launch that fails reaches the reader with its reason.
func (d *Daemon) startCodeNavSetup(ctx context.Context, msg *protocol.CodeNavSetupMessage) (protocol.SessionID, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return "", errors.New(codeNavOff())
	}
	if msg.State != codenav.StateMissing && msg.State != codenav.StateFailed {
		return "", fmt.Errorf("setup is for a language with no working server; %s is %s", msg.Language, msg.State)
	}
	if !filepath.IsAbs(msg.Repository) {
		return "", fmt.Errorf("repository %q is not an absolute path", msg.Repository)
	}
	desktop, err := d.store.GetDesktop(msg.DesktopID)
	if err != nil {
		return "", fmt.Errorf("desktop %s: %w", msg.DesktopID, err)
	}
	host := protocol.Deref(msg.Host)
	key := codeNavSetupKey{host: host, repository: msg.Repository, language: msg.Language}
	if !d.codeNavSetups.begin(key) {
		return "", fmt.Errorf("a setup of %s in %s is already starting", msg.Language, msg.Repository)
	}
	session, err := d.launchCodeNavSetup(ctx, msg, desktop.ProfileID)
	d.codeNavSetups.end(key, session)
	if err != nil {
		return "", err
	}
	d.reviewsOfRepositoryChanged(host, msg.Repository)
	return session, nil
}

func (d *Daemon) launchCodeNavSetup(ctx context.Context, msg *protocol.CodeNavSetupMessage, profileID string) (protocol.SessionID, error) {
	host := protocol.Deref(msg.Host)
	brief := prompts.RenderText("code-navigation", "setup", prompts.Values{
		"language":   msg.Language,
		"repository": msg.Repository,
		"document":   msg.Document,
		"file":       msg.File,
		"failed":     strconv.FormatBool(msg.State == codenav.StateFailed),
		"reason":     protocol.Deref(msg.Detail),
	})
	delegate := &protocol.DelegateMessage{
		Cmd:        protocol.CmdDelegate,
		RequestID:  protocol.Deref(msg.RequestID),
		ProfileID:  protocol.Ptr(profileID),
		Assignment: protocol.DelegateAssignment{Kind: protocol.DelegateAssignmentKindNew, Brief: protocol.Ptr(brief)},
		Cwd:        msg.Repository,
	}
	if d.delegationFallbackReady() {
		delegate.Fallback = protocol.Ptr(true)
	} else {
		delegate.Agent = protocol.Ptr(d.newSessionAgent())
	}
	if host != "" {
		delegate.Host = protocol.Ptr(host)
	}
	inspection, err := d.delegationCheckout(host, msg.Repository, nil)
	if err != nil {
		return "", err
	}
	if inspection.Branch != "" {
		delegate.Checkout = &protocol.DelegateCheckout{Kind: protocol.DelegateCheckoutKindReuse, Branch: inspection.Branch}
	} else {
		delegate.Cwd = "~"
	}
	operation, err := d.startDelegationForeground(ctx, delegate)
	if err == nil {
		operation, err = d.awaitDelegationSettled(operation.OperationID, nil)
	}
	if err != nil {
		return "", err
	}
	if operation.State == protocol.DelegationOperationStateFailed {
		if operation.Failure != nil {
			return "", errors.New(operation.Failure.Message)
		}
		return "", errors.New(operation.Progress)
	}
	return operation.SessionID, nil
}

func validateCodeNavSetupOffer(value string) error {
	switch strings.TrimSpace(value) {
	case "", "off":
		return nil
	}
	return fmt.Errorf("the setup offer is off, or empty to offer setup: %s", value)
}

// delegationFallbackReady says whether the user chose a harness for work no
// delegation role fits; without one a delegation runs the default agent.
func (d *Daemon) delegationFallbackReady() bool {
	cfg, err := d.store.GetDelegationPreferences()
	return err == nil && delegationprefs.Active(prompts.ExpandDelegationPreferences(cfg)).Fallback != nil
}

// newSessionAgent is the harness the user picked for new sessions.
func (d *Daemon) newSessionAgent() string {
	if agent := strings.TrimSpace(d.setting(setting.NewSessionAgent)); agent != "" {
		return agent
	}
	return string(protocol.SessionAgentClaude)
}

// reviewsOfRepositoryChanged sends the shown reviews of a repository again.
func (d *Daemon) reviewsOfRepositoryChanged(host, repository string) {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	for document, held := range delivery.documents {
		if document.host == host && held.serial > 0 && held.review != nil && held.review.Repository == repository {
			held.serial++
		}
	}
	delivery.mu.Unlock()
	d.nudgeDesktopTileContent()
}
