package appbuild

import (
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/apps"
)

type ScaffoldOptions struct {
	Dir         string
	Name        string
	Description string
	StoreDir    string
	Log         func(string)
}

func Scaffold(opts ScaffoldOptions) (Manifest, error) {
	dir, err := filepath.Abs(strings.TrimSpace(opts.Dir))
	if err != nil {
		return Manifest{}, fmt.Errorf("resolving %s: %w", opts.Dir, err)
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	if err := apps.ValidateName(name); err != nil {
		if strings.TrimSpace(opts.Name) == "" {
			return Manifest{}, fmt.Errorf("%w (the name came from the directory %s; pass --name to choose another)", err, dir)
		}
		return Manifest{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestName)); err == nil {
		return Manifest{}, fmt.Errorf("%s already holds %s, so it is already an app; `attn app apply %s` builds it", dir, ManifestName, dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		return Manifest{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	description := strings.TrimSpace(opts.Description)
	if description == "" {
		description = "An attn app."
	}
	if err := os.MkdirAll(filepath.Join(dir, "src", "views"), 0o755); err != nil {
		return Manifest{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	files := map[string]string{
		ManifestName:             scaffoldManifest(name, description),
		"src/index.ts":           scaffoldEntrypoint(),
		"src/views/Sessions.tsx": scaffoldView(),
		"tsconfig.json":          scaffoldTSConfig(),
		".gitignore":             "node_modules/\n",
		"AGENTS.md":              scaffoldAgentsMD(name),
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return Manifest{}, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	claude := filepath.Join(dir, "CLAUDE.md")
	_ = os.Remove(claude)
	if err := os.Symlink("AGENTS.md", claude); err != nil {
		return Manifest{}, fmt.Errorf("linking CLAUDE.md to AGENTS.md: %w", err)
	}

	manifest, err := LoadManifest(dir)
	if err != nil {
		return Manifest{}, fmt.Errorf("the scaffold attn wrote does not parse, which is a bug in attn: %w", err)
	}
	if err := WriteGenerated(dir, manifest); err != nil {
		return Manifest{}, err
	}
	if opts.StoreDir != "" {
		if _, err := ResolveToolchain(opts.StoreDir, opts.Log); err != nil {
			logf(opts.Log, "the SDK's types are not linked yet (%v); `attn app apply %s` installs them, and your editor will show unresolved imports until then", err, dir)
		} else if _, err := EnsureSDK(opts.StoreDir, dir, opts.Log); err != nil {
			logf(opts.Log, "the SDK's types are not linked yet (%v); `attn app apply %s` installs them, and your editor will show unresolved imports until then", err, dir)
		}
	}
	return manifest, nil
}

func scaffoldManifest(name, description string) string {
	return fmt.Sprintf(`# %s — an attn app.
#
# This file is the app's contract. It declares what wakes the app and what state
# the app owns; `+"`attn app apply`"+` derives src/generated.ts from it and the
# TypeScript compiler checks your handlers against that. Nothing here is read at
# runtime from this file — it is frozen into the version when you apply.

name = %q
description = %q

# The app contract version attn speaks. Apply refuses a manifest that asks for a
# newer one than the attn running it.
attn_app_api = %d

entrypoint = "src/index.ts"

# An app whose collections are derived from facts must be able to rebuild them
# from attn's current state: a version move changes what the app derives, and a
# gap means facts it can never receive. Declaring this makes `+"`reconcile`"+` a required
# export in src/generated.ts, and a subscribed version without it is refused a
# version move.
reconcile = true

# Every event pattern listed here becomes a required handler in src/generated.ts.
# A pattern is an exact fact name (session.state.changed) or a family (session.*).
# `+"`attn bus status`"+` lists the consumers; the fact names are attn's domain
# vocabulary — session.*, ticket.*, delegation.*, document.*.
[[subscribe]]
events = ["session.state.changed"]

# The app's own documents, under %s. Fields listed here are the ones you can
# filter and sort on; everything else in a document body is stored and read back
# untouched.
[[collections]]
name = "seen"
fields = ["state"]

# A view is a React component attn mounts as a tile in a workspace. The title is
# what the dock picker and the tile header show. The optional params table makes
# the dock ask for one line of text before placing the tile, and that string is
# what makes two tiles of one view show different things — it is opaque to attn.
[[views]]
name = "sessions"
kind = "tile"
title = "Sessions"
entrypoint = "src/views/Sessions.tsx"
params = { label = "Filter by session id", placeholder = "leave empty for all" }

# A command is how a view acts. Each one becomes a required handler under
# `+"`commands`"+` in src/generated.ts, and it runs where every other handler runs —
# same process, same document access, same log.
[[commands]]
name = "forget"
description = "Drop one session from the list."
`, name, name, description, APIVersion, apps.Namespace(name))
}

func scaffoldEntrypoint() string {
	return fmt.Sprintf(`import type { Ctx, Handlers } from "./generated"
import type { AppEvent, ReconcileReason } from %q

// One handler per subscription and one per command in %s. The `+"`satisfies Handlers`"+` below is
// what keeps the two in step: declare either one in the manifest without a
// handler here — or give a handler the wrong shape — and `+"`attn app apply`"+` fails
// with the file and line, before anything is installed.

async function onSessionState(event: AppEvent, ctx: Ctx): Promise<void> {
  // A fact says something changed; it is not the new state. Read what you need.
  await ctx.collections.seen.put(event.subject, {
    state: String(event.name),
    seq: event.seq,
  })
}

// A command is what a view calls. The payload is whatever the view passed —
// typed unknown, because attn never looks inside it — and what this returns
// travels back to the view as the command's result.
async function forget(payload: unknown, ctx: Ctx): Promise<{ forgotten: boolean }> {
  const id = (payload as { id?: unknown } | null)?.id
  if (typeof id !== "string" || id === "") {
    // Throwing is how a command refuses. The view is told, in these words.
    throw new Error("forget needs an id: { id: \"<session>\" }")
  }
  return { forgotten: await ctx.collections.seen.delete(id) }
}

// Reconcile rebuilds what the subscriptions derive, from current state rather
// than from facts. attn calls it when this app moves to a different version or
// resumes below the oldest surviving fact, and no fact or command runs until it
// succeeds. It must converge: run it twice, or after an interrupted attempt, and
// the collection ends up the same. Deleting what current truth no longer has is
// half the job — a rebuild that only upserts leaves rows nothing will remove.
async function reconcile(_reason: ReconcileReason, ctx: Ctx): Promise<void> {
  const current = await ctx.current.snapshot()
  const live = new Set(current.sessions.map((session) => session.id))
  for (const row of await ctx.collections.seen.query({})) {
    if (!live.has(row.id)) {
      await ctx.collections.seen.delete(row.id)
    }
  }
  for (const session of current.sessions) {
    await ctx.collections.seen.put(session.id, {
      state: String(session.state),
      seq: current.asOfSeq,
    })
  }
}

// Handlers are grouped by kind, and attn knows which kind it is running: a
// command and a subscription can share a name without either becoming ambiguous.
export default {
  subscriptions: { "session.state.changed": onSessionState },
  commands: { forget },
  reconcile,
} satisfies Handlers
`, SDKModule, ManifestName)
}

func scaffoldView() string {
	return fmt.Sprintf(`import {
  Button,
  EmptyState,
  List,
  ListRow,
  TextInput,
  useCommand,
  useQuery,
  useState,
  type ReactElement,
  type ViewProps,
} from %q

// A view is a React component attn mounts as a tile. It is a function of where
// it sits: workspaceId, sessionId and tileId are ambient, and params is the line
// the user typed when docking this tile.
//
// There is no react to import and no styling to write. The SDK re-exports the
// hooks and the components, and a tile inherits attn's own tokens, so a view
// that uses them looks like the rest of the app.

interface Seen {
  state: string
  seq: number
}

export default function Sessions({ params }: ViewProps): ReactElement {
  // A live query. It stays current on its own: attn re-runs it when a document
  // this window would hold changes, and re-renders only what moved.
  const { docs, live, error } = useQuery<Seen>("seen", {
    sort: { field: "updated_at", desc: true },
    limit: 50,
  })
  const [filter, setFilter] = useState(params)
  const forget = useCommand("forget")

  if (error) {
    return <EmptyState title="This query stopped" hint={error.message} />
  }

  const shown = filter === "" ? docs : docs.filter((doc) => doc.id.includes(filter))
  if (shown.length === 0) {
    // Loading is a state, not a spinner. Nothing in a view may animate forever:
    // attn is open all day beside GPU terminals, and a repaint loop is a battery
    // bug no test catches.
    return (
      <EmptyState
        title={live ? "No sessions yet" : "Connecting…"}
        hint={live ? "This fills in as sessions change state." : ""}
      />
    )
  }

  return (
    <>
      <TextInput
        value={filter}
        onChange={setFilter}
        placeholder="Filter by session id"
        ariaLabel="Filter by session id"
      />
      <List>
        {shown.map((doc) => (
          <ListRow
            key={doc.id}
            title={doc.id}
            meta={`+"`${doc.body.state} · seq ${doc.body.seq}`"+`}
            actions={
              <Button
                variant="danger"
                disabled={forget.pending}
                onClick={() => forget({ id: doc.id })}
              >
                Forget
              </Button>
            }
          />
        ))}
      </List>
      {/* A command that failed says so where the click happened. The message is
          the daemon's or the handler's own — it names the app and the command. */}
      {forget.error && <EmptyState title="That did not work" hint={forget.error} />}
    </>
  )
}
`, SDKModule)
}

func scaffoldTSConfig() string {
	return `{
  "compilerOptions": {
    "strict": true,
    "target": "es2022",
    "module": "esnext",
    "moduleResolution": "bundler",
    "skipLibCheck": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "jsxImportSource": "` + SDKModule + `"
  },
  "include": ["src"]
}
`
}

func scaffoldAgentsMD(name string) string {
	return prompts.RenderText("authoring-agent", "app-guidance", prompts.Values{"app_name": name, "sdk_module": SDKModule})
}
