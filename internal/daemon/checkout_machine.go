package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/stepclock"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) resolveCallerHost(source protocol.SessionID, requested *string) (string, error) {
	place := d.here()
	if requested != nil {
		if host := strings.TrimSpace(*requested); host != "home" {
			place = d.machineFor(host)
		}
	} else if session := d.store.Get(source); session != nil {
		place = d.machineOnWire(session.Host)
	}
	if place.IsLocal() {
		return place.ID(), nil
	}
	row, err := d.resolveOutpost(place.ID())
	if err != nil {
		return "", err
	}
	if err := startsTerminals(row); err != nil {
		return "", err
	}
	if _, _, err := d.outpostConn(row.ID); err != nil {
		return "", err
	}
	return row.ID, nil
}

// startsTerminals refuses a machine that dials this home: it starts no terminals until it serves its own.
func startsTerminals(row store.Machine) error {
	if row.Reverse() {
		return protocol.Refuse(protocol.RefusalWrongState, "%s dials this home, so it starts no terminals yet; they come once %s serves its own terminals", row.Name, row.Name)
	}
	return nil
}

func (d *Daemon) wireHost(id string) *string {
	return d.store.WireHost(id)
}

func canonicalCheckoutPath(path string) (string, error) {
	return filepath.Abs(localMachine().canonicalize(path).path)
}

func (d *Daemon) inspectCheckout(ctx context.Context, msg protocol.CheckoutInspectMessage) (protocol.CheckoutInspection, error) {
	result := protocol.CheckoutInspection{Refs: []protocol.CheckoutRef{}, Directories: []protocol.CheckoutDirectory{}}
	var err error
	result.Cwd, err = canonicalCheckoutPath(msg.Cwd)
	if err != nil {
		return result, err
	}
	info, err := os.Stat(result.Cwd)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Exists, result.IsDirectory = true, info.IsDir()
	if !result.IsDirectory {
		return result, nil
	}
	err = d.gitExecution().Run(ctx, delegationGitTask, func(ctx context.Context, client *git.Client) error {
		root, rootErr := client.GetRepoRoot(ctx, result.Cwd)
		if rootErr != nil {
			return nil
		}
		result.RepoRoot = localMachine().canonicalize(root).path
		result.MainRepo = localMachine().canonicalize(client.ResolveMainRepoPath(ctx, root)).path
		if relative, err := filepath.Rel(result.RepoRoot, result.Cwd); err == nil && relative != "." {
			result.RepositorySubdir = relative
		}
		branch, _ := client.Output(ctx, git.OpMetadata, result.Cwd, "symbolic-ref", "--short", "HEAD")
		result.Branch = strings.TrimSpace(string(branch))
		for _, ref := range msg.Refs {
			commit, _ := client.Output(ctx, git.OpMetadata, result.MainRepo, "rev-parse", "--verify", ref+"^{commit}")
			result.Refs = append(result.Refs, protocol.CheckoutRef{Ref: ref, Commit: strings.TrimSpace(string(commit))})
		}
		for _, directory := range msg.Directories {
			root, _ := client.GetRepoRoot(ctx, directory)
			result.Directories = append(result.Directories, protocol.CheckoutDirectory{Path: directory, RepoRoot: localMachine().canonicalize(root).path})
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	path := strings.TrimSpace(protocol.Deref(msg.WorktreePath))
	if path == "" && result.MainRepo != "" && protocol.Deref(msg.WorktreeBranch) != "" {
		path = git.GenerateWorktreePath(result.MainRepo, protocol.Deref(msg.WorktreeBranch))
	}
	if path != "" {
		result.WorktreePath, err = canonicalCheckoutPath(path)
	}
	return result, err
}

// Outpost checkout_inspect round trips took 35-89ms in the bridge receipts.
const remoteCheckoutInspectTripwire = 10 * time.Second

const remoteCheckoutInspectTimer stepclock.Name = "remote-checkout-inspect"

func (d *Daemon) inspectRemoteCheckout(ctx context.Context, host, cwd string) (protocol.CheckoutInspection, error) {
	expired := fmt.Errorf("%s did not answer checkout_inspect for %s within %s", host, cwd, remoteCheckoutInspectTripwire)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	deadline := d.clock.AfterFunc(remoteCheckoutInspectTimer, remoteCheckoutInspectTripwire, func() { cancel(expired) })
	defer deadline.Stop()
	inspection, err := Call[protocol.CheckoutInspection](ctx, d.router(), protocol.CheckoutInspectMessage{Cmd: protocol.CmdCheckoutInspect, Host: host, Cwd: cwd})
	if err != nil && errors.Is(context.Cause(ctx), expired) {
		return inspection, expired
	}
	return inspection, err
}

func (d *Daemon) delegationCheckout(host, cwd string, checkout *protocol.DelegateCheckout) (protocol.CheckoutInspection, error) {
	request := protocol.CheckoutInspectMessage{Cmd: protocol.CmdCheckoutInspect, Host: host, Cwd: cwd}
	if checkout != nil {
		request.WorktreeBranch, request.WorktreePath = &checkout.Branch, checkout.Path
		request.Refs = []string{"refs/heads/" + checkout.Branch}
		if base := protocol.Deref(checkout.From); base != "" {
			request.Refs = append(request.Refs, base)
		}
	}
	inspection, err := Call[protocol.CheckoutInspection](context.Background(), d.router(), request)
	if err != nil {
		return inspection, fmt.Errorf("delegation directory: %w", err)
	}
	if !inspection.Exists {
		return inspection, fmt.Errorf("delegation directory %s does not exist", inspection.Cwd)
	}
	if !inspection.IsDirectory {
		return inspection, fmt.Errorf("delegation directory is not a directory: %s", inspection.Cwd)
	}
	return inspection, nil
}

func checkoutCommit(inspection protocol.CheckoutInspection, ref string) string {
	for _, resolved := range inspection.Refs {
		if resolved.Ref == ref {
			return resolved.Commit
		}
	}
	return ""
}

func (d *Daemon) delegationExecution(session protocol.SessionID, host, agent string, inspection protocol.CheckoutInspection) garden.Execution {
	return garden.Execution{SessionID: session, Machine: protocol.Ptr(host), Agent: agent, Cwd: inspection.Cwd, Branch: inspection.Branch,
		RepositoryRoot: inspection.MainRepo, RepositorySubdir: inspection.RepositorySubdir, CapturedAt: formatGardenTime(d.gardenTime())}
}
