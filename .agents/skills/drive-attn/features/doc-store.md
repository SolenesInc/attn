# Document store

Collections of documents with live queries, for agents and plugins. A
document is a JSON object addressed by namespace, collection and id; a
collection declares which fields can be filtered and sorted. Agents keep
shared state there, change it only if nobody changed it first, and watch a
query whose window updates as writes land. The garden is stored the same way,
but `attn doc` cannot read or change it: only `attn seed` reaches it.

## Actions and ways in

People and agents use `attn doc` from the shell (`cmd/attn/doc.go`); nothing in the app shows the store. Agents find the verb in `attn --help`. No bundled plugin uses it and the plugin API has no document methods.

- **Manage collections.** `attn doc define <namespace> <collection> [field:type ...]` declares queryable fields; redeclare to add or drop fields. `undefine` removes the collection and all documents; `collections [--json]` lists them.
- **Write and read.** `put <namespace> <collection> <id> <body|-> [--expect <rev|absent>]`, `get [--json]` and `delete [--expect <rev>]` write, read and delete. Reads and writes report revision and log position. `--expect` makes a write or delete conditional, refusing it when the document moved.
- **Query or watch.** `query`, `count` and `watch [--resume]` take `--where`, `--sort`, `--desc`, `--limit` and `--after`. A query answers at the position it reports; a watch prints its window again when a write changes it and `--resume` continues after a daemon restart.

## Driving it

Preconditions:

- The baseline instance, daemon only.
- A proof folder, `P=$(mktemp -d /tmp/drive-attn-doc-store.XXXX) && echo "export P=$P" >> $DRIVE_ENV`.
- An agent folder, `A=$(mktemp -d /tmp/drive-attn-doc-agent.XXXX) && echo "export A=$A" >> $DRIVE_ENV`, holding this fixture as `$A/.attn-mock-agent.json`:

```json
{"version": 1, "turns": [
  {"includes": "record", "actions": [
    {"type": "attn", "args": ["doc", "put", "drive/demo", "tasks", "t9", "{\"status\":\"pending\",\"attempts\":2}"]}]}
]}
```

`<terminal>` below is the agent's terminal id from `wsctl list`; `<n>` is a log position the command prints.

- **Declare.** Run `./attn doc define drive/demo tasks status:string attempts:number`. It prints `declared drive/demo/tasks with 2 queryable field(s)`. `./attn doc collections` lists `drive/demo`, `tasks` and `status:string attempts:number` under `NAMESPACE  COLLECTION  INDEXED FIELDS`, and `./attn doc collections --json` lists the same collection as JSON.
- **Write and read.** Run `./attn doc put drive/demo tasks t1 '{"status":"pending","attempts":1}'`. It prints `wrote drive/demo/tasks/t1 (rev 1, seq <n>)`. `./attn doc get drive/demo tasks t1` prints the body exactly as written, and `as of seq <n>` on stderr. `./attn doc get drive/demo tasks t1 --json` prints the document with its `id`, `rev` and timestamps.
- **Query and count.** `./attn doc query drive/demo tasks --where status=pending --sort attempts --desc` prints `t1` under `ID  REV  UPDATED  BODY`. `./attn doc count drive/demo tasks --where 'attempts>=1'` prints `1`.
- **Page and read as JSON.** Run `./attn doc put drive/demo tasks t3 '{"status":"pending","attempts":3}'`. `./attn doc query drive/demo tasks --sort attempts --limit 1` prints only `t1`, and the same with `--after t1` prints only `t3`. `./attn doc query drive/demo tasks --where status=pending --json` prints a JSON array whose `id` values are `t1` and `t3`. `./attn doc query drive/demo tasks --limit 1001` exits 1, naming the maximum, `1000`, and the `after` cursor to page with.
- **Conditional writes.** `./attn doc put drive/demo tasks t1 '{"status":"done","attempts":1}' --expect 1` prints `rev 2`. The same command again exits 1 naming `expected rev 1 but is at rev 2`, and `--expect absent` on `t1` exits 1 naming `already exists at rev 2`. `./attn doc get drive/demo tasks t1` still shows `"done"`.
- **Delete.** Run `./attn doc put drive/demo tasks t2 '{"status":"done","attempts":0}'`, then `./attn doc delete drive/demo tasks t2 --expect 1`: it prints `deleted drive/demo/tasks/t2 (seq <n>)`. The same delete without `--expect` prints `drive/demo/tasks/t2 did not exist`.
- **A watch resumes after a daemon restart.** Run `(./attn doc watch drive/demo tasks --where status=pending --json --resume & echo "export R=$!" >> $DRIVE_ENV; wait $!; echo $? > $P/resume.exit) > $P/resume.jsonl 2> $P/resume.err &`: `R` is the watch's PID, and `$P/resume.exit` gets its exit code. Re-run `jq -s length $P/resume.jsonl` until it prints `1`, with a deadline that fails the run: the first delivery holds `t3`. Run `./attn daemon stop`, which prints `stopped daemon (pid <n>)`, then `./attn daemon ensure`, which prints `"status": "started"`. Run `./attn doc put drive/demo tasks t5 '{"status":"pending","attempts":6}'`. Re-run `jq -c '{delivery, ids: [.documents[].id], changed}' $P/resume.jsonl | tail -1` until its `ids` hold `t3` and `t5` and its `changed` holds only `t5`: the resumed watch was sent only the body it did not hold.
- **Watch while an agent writes.** Run `(./attn doc watch drive/demo tasks --where status=pending & echo "export W=$!" >> $DRIVE_ENV; wait $!; echo $? > $P/watch.exit) > $P/watch.txt 2> $P/watch.err &`. `$P/watch.txt` starts with `--- delivery 1 (2 document(s)` and lists `t3` and `t5`. Run `wsctl add-session --cwd $A --agent claude --label doc-agent --id doc-agent` and `wsctl input --id <terminal> --text "record" --enter`. `./attn agent peek doc-agent` shows `wrote drive/demo/tasks/t9 (rev 1, seq <n>)`, and `$P/watch.txt` gains a second `--- delivery` block that also lists `t9`.
- **Removing the collection ends both watches.** Run `./attn doc undefine drive/demo tasks`. It prints `removed drive/demo/tasks and 4 document(s)`. Re-read `cat $P/watch.exit $P/resume.exit` until it prints `1` twice, with a deadline that fails the run, and `$P/watch.err` and `$P/resume.err` each end with a `doc watch:` line.
- **Refusals.** `./attn doc get drive/demo tasks t1` now exits 1, naming `attn doc define`. `./attn doc put core/garden seeds s-x '{}'` exits 1 with `use attn seed commands`, and so does `./attn doc query core/garden seeds`. `./attn doc query drive/demo tasks --resume` exits 1 with `--resume is only for watch`.
- **Proof.** In `$P`, keep each `./attn doc` command with its stdout, stderr and exit code, `watch.txt`, `watch.err`, `resume.jsonl`, `resume.err`, `watch.exit` and `resume.exit`, and `./attn agent peek doc-agent` after the prompt. Put the way in in each file's name, such as `doc-agent-put-peek.txt`.

## Contexts

- **Durability across restarts**: documents and their declarations survive daemon restarts. `attn doc watch --resume` waits for the daemon to come back and receives only what changed while it was away; without `--resume` a watch exits non-zero when the daemon goes.
- **Linux**: `attn doc` works the same where the daemon and CLI run on Linux.
- **Outposts**: every `attn doc` call on an outpost is answered by the home, so an agent there reads and writes the home's documents.
- **Profiles**: documents belong to no profile. Every profile and agent on the daemon reads the same namespaces. The garden's namespace, `core/garden`, is refused to `attn doc` for every verb, and its seeds are reached only through `attn seed`, which keeps them in their profile.
- **Performance and idle cost**: a declared field is indexed, so filters and sorts on it read the index. `--limit` defaults to 100, and a limit above 1,000 is refused, naming the maximum and telling the caller to page with `--after`. A burst of writes reaches a slow watcher in at most two deliveries, and a writer never waits on a watcher that stopped reading.
- **Compatibility across versions**: the store's migrations carry documents written by earlier versions forward, including their revisions and timestamps.

## Tests

- Wire: `internal/daemon/documents_wire_test.go`, `internal/daemon/documents_live_wire_test.go`, `internal/daemon/documents_rules_wire_test.go`.
- Stack: `cmd/attn/doc_stack_test.go`.
- Kernel: `internal/docstore/*_test.go`, `internal/store/documents*_test.go`.

## Gotchas

- Quote `--where` expressions with `<` or `>`: unquoted, `--where attempts>=1` makes the shell write to a file named `=1`.
- A `--where` value is read as JSON when it parses as JSON: `--where status=5` sends the number 5, and `--where 'status="5"'` sends the string.
- Reading or writing an undeclared collection is refused; declare it first. Redeclaring without a field that a live query uses ends that watch.
- `watch` runs until interrupted. Run it in the background with its PID kept from `$!`, and end it by removing the collection or by that PID, never by name.
- A watch prints the whole window on every delivery; `--json` adds `changed`, the bodies that delivery actually sent.
- `get` and `query` print the position on stderr; a script that reads stdout gets only the body. Every `attn doc` call also prints the `[attn instance=...]` banner on stderr, so keep a watch's stderr out of the file that holds its deliveries.
