export function collaborate({ state, $, api, selectEvent, selectSource, renderNavigation, schedulePreview, status }) {
    let draft = null;
    let review = null;
    let scenarios = {};
    let flushing = null;
    let timer;
    let refreshTimer;
    let sourceSelection = null;
    let loadVersion = 0;
    let initialized = false;
    let refreshPending = false;
    const pending = new Map();
    const blocked = new Set();
    const op = (body) => api("operation", { author: $("editor-author").value || "maintainer", ...body });
    const showError = (error) => { $("collaboration-error").hidden = false; $("collaboration-error").textContent = error.message; };
    const hideError = () => { $("collaboration-error").hidden = true; };
    const payload = () => ({ ...(review ? { review: review.id } : draft ? { draft: draft.id } : {}), ...((review?.focus.scenario || $("saved-scenario").value) ? { scenario: review?.focus.scenario || $("saved-scenario").value } : {}) });
    const focused = () => ({ event: state.key, path: state.path, scenario: $("saved-scenario").value, values: { ...state.scenarios.get(state.key) }, base_commit: state.base?.commit || "" });
    function contextLabel() {
        $("draft-state").textContent = review ? `${review.id} · snapshot of revision ${review.draft_revision}` : draft ? `${draft.id} · revision ${draft.revision} · ${draft.author}` : "Checkout files";
        $("share-review").disabled = !draft || !!draft?.archived;
        $("new-draft").textContent = review ? "Fork review" : "New draft";
        $("source-edit").textContent = review ? "Read" : "Edit";
        $("apply-draft").hidden = !draft || !!review;
        $("sync-draft").hidden = !draft || !!review;
        $("archive-draft").hidden = !draft || !!review || draft.archived;
        $("restore-draft").hidden = !draft?.archived;
        $("save").textContent = draft ? "Apply draft" : "Save file ⌘S";
        const readonly = !initialized || !!review || !!draft?.archived;
        $("apply-draft").disabled = !draft || draft.archived || (!Object.keys(draft.files).length && !pending.size);
        $("return-draft").hidden = !review;
        $("source").disabled = !state.catalog.sources[state.path];
        $("source").readOnly = readonly;
        $("save").disabled ||= readonly;
        $("reset").disabled ||= readonly;
        $("reload").disabled ||= readonly;
        document.querySelectorAll("#inputs input,#inputs textarea,#inputs button,#presets button").forEach((input) => { input.disabled = readonly; });
        $("recipients").querySelectorAll("button").forEach((button) => { button.disabled = !!review; });
        $("tree").querySelectorAll("button").forEach((button) => { if (button.textContent.includes("/"))
            button.disabled = !!review; });
        $("saved-scenario").disabled = readonly;
        $("save-scenario").disabled = readonly;
        $("resolve-conflict").hidden = !blocked.size;
        if (review) {
            $("global-status").textContent = "Review snapshot";
            $("file-state").textContent = `Frozen source · ${state.catalog.sources[state.path]?.revision.slice(0, 8) || ""}`;
        }
        if (draft && !review) {
            $("file-state").textContent = pending.has(state.path) ? "Sharing changes…" : draft.files[state.path] ? "Shared draft · not applied to disk" : "Saved on disk";
            $("global-status").textContent = blocked.size ? "Shared edit conflict" : pending.size ? "Sharing changes…" : "Draft saved locally";
        }
    }
    function setLocation() {
        const params = new URLSearchParams();
        if (review)
            params.set("review", review.id);
        else if (draft)
            params.set("draft", draft.id);
        history.replaceState(null, "", `${location.pathname}${params.size ? `?${params}` : ""}#${state.key}`);
    }
    function setFocus(focus) {
        if (!focus?.event)
            return;
        if (focus.values)
            state.scenarios.set(focus.event, { ...focus.values });
        selectEvent(focus.event);
        if (focus.path && (state.catalog.sources[focus.path] || state.base?.sources[focus.path]))
            selectSource(focus.path);
        $("saved-scenario").value = focus.scenario || "";
    }
    async function setBase(commit) {
        if (!commit) {
            state.base = null;
            $("base-error").hidden = true;
            $("base-status").textContent = "Choose a base for comparison.";
            return;
        }
        state.base = await api("base", { ref: commit, mode: "tip" });
        $("base-ref").value = commit;
        $("base-mode").value = "tip";
        $("base-status").textContent = `Review base · ${commit.slice(0, 8)}`;
        $("base-error").hidden = !state.base.unavailable;
        $("base-error").textContent = state.base.unavailable || "";
    }
    async function lists() {
        const [drafts, reviews, saved] = await Promise.all([op({ op: "draft-list" }), op({ op: "review-list" }), op({ op: "scenarios" })]);
        const fill = (select, items, empty, selected) => {
            select.replaceChildren(new Option(empty, ""));
            for (const item of items)
                select.append(new Option(`${item.title || item.id}${item.archived ? " (archived)" : ""} · ${item.id}`, item.id));
            select.value = selected || "";
        };
        fill($("shared-draft"), drafts, review ? "Review snapshot" : "Checkout only", draft?.id);
        fill($("saved-review"), reviews, "Open a review…", review?.id);
        scenarios = review?.snapshot.scenarios || saved;
        const selected = review?.focus.scenario || $("saved-scenario").value;
        $("saved-scenario").replaceChildren(new Option("Custom inputs", ""));
        for (const scenario of Object.values(scenarios))
            $("saved-scenario").append(new Option(scenario.description || scenario.id, scenario.id));
        $("saved-scenario").value = selected;
    }
    function protectLocal() {
        if (!draft && !review && state.drafts.size)
            throw new Error("Save these checkout edits or use New draft to keep them before switching.");
    }
    async function loadDraft(id, follow = false) {
        const version = ++loadVersion;
        const [data, catalog] = await Promise.all([id ? op({ op: "draft-get", id }) : null, api("catalog")]);
        if (version !== loadVersion)
            return;
        draft = data;
        review = null;
        state.catalog = catalog;
        state.drafts.clear();
        sourceSelection = null;
        $("selection-context").textContent = "Feedback includes this scenario and its exact prompt revision.";
        for (const [path, file] of Object.entries(draft?.files || {}))
            state.drafts.set(path, file.text);
        if (follow && draft?.focus) {
            await setBase(draft.focus.base_commit);
            setFocus(draft.focus);
        }
        else {
            selectEvent(state.key);
            selectSource(state.path);
        }
        state.shared = payload();
        setLocation();
        await lists();
        renderNavigation();
        schedulePreview();
        feedbackList();
        contextLabel();
    }
    async function loadReview(id) {
        protectLocal();
        await flush();
        if (pending.size)
            throw new Error("Resolve local edits before opening a review.");
        const version = ++loadVersion;
        const [data, catalog] = await Promise.all([op({ op: "review-get", id }), op({ op: "catalog", review: id })]);
        if (version !== loadVersion)
            return;
        review = data;
        draft = null;
        state.catalog = catalog;
        state.drafts.clear();
        sourceSelection = null;
        $("selection-context").textContent = "Feedback includes this scenario and its exact prompt revision.";
        state.shared = payload();
        await setBase(review.focus.base_commit);
        setFocus(review.focus);
        state.reviewTarget = state.base ? "source" : "";
        setLocation();
        await lists();
        renderNavigation();
        schedulePreview();
        feedbackList();
        contextLabel();
    }
    async function flush() {
        clearTimeout(timer);
        if (flushing)
            return flushing;
        flushing = (async () => {
            for (const [path] of pending) {
                if (blocked.has(path))
                    continue;
                const edit = pending.get(path);
                try {
                    const updated = await op({ op: "draft-put", id: draft.id, path, text: edit.text, expect: edit.expect });
                    draft = updated;
                    const latest = pending.get(path);
                    if (latest === edit)
                        pending.delete(path);
                    else
                        latest.expect = draft.files[path]?.revision || state.catalog.sources[path].revision;
                }
                catch (error) {
                    blocked.add(path);
                    state.errors.set(path, error.message);
                    showError(error);
                }
            }
        })().finally(() => {
            flushing = null;
            status();
            if ([...pending.keys()].some((path) => !blocked.has(path)))
                timer = setTimeout(flush, 100);
        });
        return flushing;
    }
    function edited(path, text) {
        if (!draft || review)
            return;
        const previous = pending.get(path);
        pending.set(path, { text, expect: previous?.expect || draft.files[path]?.revision || state.catalog.sources[path].revision });
        clearTimeout(timer);
        timer = setTimeout(flush, 200);
        contextLabel();
    }
    async function refreshShared() {
        if (flushing) {
            await flushing;
        }
        const version = loadVersion;
        await lists();
        if (version !== loadVersion)
            return;
        if (review) {
            review = await op({ op: "review-get", id: review.id });
            feedbackList();
            return;
        }
        const freshCatalog = await api("catalog");
        if (version !== loadVersion)
            return;
        if (freshCatalog.validation)
            showError(new Error(freshCatalog.validation));
        const selectedPath = state.path;
        if (draft) {
            const updated = await op({ op: "draft-get", id: draft.id });
            if (version !== loadVersion)
                return;
            const moved = JSON.stringify(updated.focus) !== JSON.stringify(draft.focus);
            for (const path of new Set([...state.drafts.keys(), ...Object.keys(updated.files)])) {
                if (pending.has(path))
                    continue;
                if (updated.files[path])
                    state.drafts.set(path, updated.files[path].text);
                else
                    state.drafts.delete(path);
            }
            draft = updated;
            if (moved && $("follow-agent").checked) {
                await setBase(draft.focus.base_commit);
                setFocus(draft.focus);
            }
            if (draft.latest_review)
                $("review-notification").textContent = `Latest review: ${draft.latest_review}`;
        }
        const activeSource = document.activeElement === $("source");
        for (const [path, file] of Object.entries(freshCatalog.sources)) {
            if (!pending.has(path) && !state.drafts.has(path))
                state.catalog.sources[path] = file;
        }
        if (freshCatalog.revision !== state.catalog.revision && !pending.size) {
            state.catalog = freshCatalog;
            selectEvent(state.key);
        }
        if (!activeSource && !pending.has(selectedPath))
            selectSource(selectedPath);
        else if (!pending.has(selectedPath))
            $("source").value = state.drafts.get(selectedPath) ?? state.catalog.sources[selectedPath]?.text ?? "";
        renderNavigation();
        schedulePreview();
        contextLabel();
    }
    async function createDraft(fromReview = false) {
        await flush();
        if (blocked.size)
            throw new Error("Resolve the shared edit conflict first.");
        const title = prompt("Name this shared draft", "Prompt changes");
        if (!title)
            return;
        const local = new Map(state.drafts);
        const data = await op({ op: "draft-create", title, draft: fromReview ? undefined : draft?.id, review: fromReview ? review?.id : undefined, focus: focused() });
        await loadDraft(data.id);
        for (const [path, text] of local) {
            state.drafts.set(path, text);
            edited(path, text);
        }
        await flush();
        selectSource(state.path);
        contextLabel();
    }
    async function apply() {
        await flush();
        if (pending.size)
            throw new Error("Resolve shared edit conflicts before applying.");
        draft = await op({ op: "draft-apply", id: draft.id, revision: draft.revision });
        await loadDraft(draft.id);
    }
    async function reset() {
        if (!draft || review)
            return false;
        await flush();
        if (blocked.size)
            throw new Error("Resolve the shared edit conflict first.");
        const path = state.path;
        const expect = draft.files[path]?.revision || state.catalog.sources[path].revision;
        draft = await op({ op: "draft-reset", id: draft.id, path, expect });
        state.drafts.delete(path);
        selectSource(path);
        renderNavigation();
        schedulePreview();
        contextLabel();
        return true;
    }
    async function captureReview() {
        await flush();
        if (pending.size)
            throw new Error("Resolve local edits before creating a review.");
        if (review)
            return review;
        if (!draft)
            throw new Error("Start a shared draft before creating a review.");
        const data = await op({ op: "review-create", draft: draft.id, revision: draft.revision, title: draft.title, focus: focused() });
        await loadReview(data.id);
        return data;
    }
    function feedbackList() {
        $("feedback-list").replaceChildren();
        $("feedback-count").textContent = review ? `(${review.feedback.length})` : "";
        for (const item of review?.feedback || []) {
            const node = document.createElement("article");
            const author = document.createElement("strong");
            author.textContent = item.author;
            const text = document.createElement("p");
            text.textContent = item.message;
            node.append(author);
            if (item.selection) {
                const quote = document.createElement("blockquote");
                quote.textContent = item.selection;
                node.append(quote);
            }
            node.append(text);
            $("feedback-list").append(node);
        }
    }
    function rememberSelection() {
        const source = $("source");
        const fullText = $("review-full-text");
        if (document.activeElement === source && source.selectionEnd > source.selectionStart) {
            sourceSelection = { target: "source", path: state.path, selection: source.value.slice(source.selectionStart, source.selectionEnd) };
        }
        else if (document.activeElement === fullText && fullText.selectionEnd > fullText.selectionStart) {
            sourceSelection = $("review-version").value === "current"
                ? { target: state.reviewTarget, ...(state.reviewTarget === "source" ? { path: state.path } : {}), selection: fullText.value.slice(fullText.selectionStart, fullText.selectionEnd) }
                : null;
        }
        else {
            const selection = window.getSelection();
            if (selection && !selection.isCollapsed && $("output").contains(selection.anchorNode) && $("output").contains(selection.focusNode))
                sourceSelection = { target: "prompt", selection: selection.toString() };
        }
        $("selection-context").textContent = sourceSelection ? `Selected ${sourceSelection.target}: ${sourceSelection.selection.slice(0, 100)}` : "Feedback includes this scenario and its exact prompt revision.";
    }
    function action(id, fn) {
        $(id).addEventListener("click", async () => { hideError(); try {
            await fn();
        }
        catch (error) {
            showError(error);
        } });
    }
    action("new-draft", () => createDraft(!!review));
    action("apply-draft", apply);
    action("sync-draft", async () => { await flush(); draft = await op({ op: "draft-sync", id: draft.id, revision: draft.revision }); await loadDraft(draft.id); });
    action("restore-draft", async () => { draft = await op({ op: "draft-restore", id: draft.id, revision: draft.revision }); await loadDraft(draft.id); });
    action("archive-draft", async () => { await flush(); if (pending.size)
        throw new Error("Resolve local edits first."); await op({ op: "draft-archive", id: draft.id, revision: draft.revision }); await loadDraft(""); });
    action("return-draft", () => loadDraft(review.draft_id, true));
    action("share-review", captureReview);
    action("load-shared", async () => {
        if (flushing)
            await flushing;
        for (const path of blocked) {
            pending.delete(path);
            state.errors.delete(path);
        }
        blocked.clear();
        hideError();
        await refreshShared();
    });
    action("fork-local", async () => {
        const local = new Map(state.drafts);
        const data = await op({ op: "draft-create", title: `${draft.title} (local edits)`, draft: draft.id, focus: focused() });
        pending.clear();
        blocked.clear();
        state.errors.clear();
        await loadDraft(data.id);
        for (const [path, text] of local) {
            state.drafts.set(path, text);
            edited(path, text);
        }
        await flush();
        selectSource(state.path);
        hideError();
    });
    action("refresh-checkout", async () => {
        $("draft-state").textContent = "Regenerating catalog…";
        await op({ op: "refresh" });
        await refreshShared();
    });
    action("save-scenario", async () => {
        const id = prompt("Scenario id (letters, digits, hyphens)", $("saved-scenario").value || "my-scenario");
        if (!id)
            return;
        const existing = scenarios[$("saved-scenario").value];
        const inputs = existing?.recipient + "/" + existing?.event === state.key ? existing.inputs : undefined;
        const values = { ...state.scenarios.get(state.key) };
        for (const field of Object.keys(inputs || {}))
            delete values[field];
        await op({ op: "scenario-save", id, title: scenarios[id]?.description || id, event: state.key, values, inputs, expect: scenarios[id]?.revision || "", ...payload() });
        await lists();
        $("saved-scenario").value = id;
    });
    action("send-feedback", async () => {
        const message = $("feedback-message").value.trim();
        if (!message)
            throw new Error("Write feedback first.");
        const selected = sourceSelection;
        const captured = await captureReview();
        review = await op({ op: "review-comment", id: captured.id, text: message, ...selected });
        $("feedback-message").value = "";
        sourceSelection = null;
        feedbackList();
        rememberSelection();
        $("discussion").open = true;
        await lists();
    });
    action("copy-agent-context", async () => {
        const captured = await captureReview();
        const guidance = await op({ op: "authoring" });
        const command = `go run ./cmd/prompt-editor context --review ${captured.id} --json`;
        const feedback = `go run ./cmd/prompt-editor review get ${captured.id} --json`;
        await navigator.clipboard.writeText(`${captured.title}\n${location.href}\n\n${guidance.workflow}\nRead the complete review context, including every changed source and affected scenario:\n${command}\nExpand related guidance with --include EVENT_OR_SOURCE. Resolve relevant coverage gaps before editing.\nRead feedback:\n${feedback}\nContinue editing shared draft ${captured.draft_id}; rerun context --draft ${captured.draft_id} after edits and show the complete results in the browser.`);
        $("draft-state").textContent = "Agent context copied";
    });
    $("shared-draft").addEventListener("change", async () => { try {
        protectLocal();
        await flush();
        if (pending.size)
            throw new Error("Resolve local edits before switching drafts.");
        await loadDraft($("shared-draft").value, true);
    }
    catch (error) {
        showError(error);
    } });
    $("saved-review").addEventListener("change", async () => { if (!$("saved-review").value)
        return; try {
        await loadReview($("saved-review").value);
    }
    catch (error) {
        showError(error);
    } });
    $("saved-scenario").addEventListener("change", async () => {
        const id = $("saved-scenario").value;
        if (!id)
            return;
        try {
            const inspected = await op({ op: "inspect", scenario: id, ...payload() });
            state.scenarios.set(inspected.event, inspected.values);
            selectEvent(inspected.event);
            $("saved-scenario").value = id;
            await shareFocus();
        }
        catch (error) {
            showError(error);
        }
    });
    async function shareFocus() {
        if (!draft || review)
            return;
        await flush();
        if (pending.size)
            return;
        draft = await op({ op: "draft-focus", id: draft.id, focus: focused() });
        contextLabel();
    }
    $("inputs").addEventListener("change", () => { shareFocus().catch(showError); });
    for (const id of ["recipients", "tree", "uses", "all-sources", "presets"])
        $(id).addEventListener("click", (event) => {
            if (event.target.closest("button"))
                shareFocus().catch(showError);
        });
    $("source").addEventListener("select", rememberSelection);
    for (const id of ["output", "review-full-text"]) {
        $(id).addEventListener("mouseup", rememberSelection);
        $(id).addEventListener("keyup", rememberSelection);
    }
    const events = new EventSource("/api/events");
    function queueRefresh() {
        if (!initialized) { refreshPending = true; return; }
        clearTimeout(refreshTimer);
        refreshTimer = setTimeout(() => refreshShared().catch(showError), 90);
    }
    events.onmessage = queueRefresh;
    events.addEventListener("ready", queueRefresh);
    window.addEventListener("pagehide", () => events.close());
    return {
        payload, edited, contextLabel,
        pending: () => pending.size,
        active: () => !!draft || !!review,
        readonly: () => !!review || !!draft?.archived,
        reviewEvent: () => review?.focus.event,
        save: async () => {
            if (review) return true;
            if (!draft) return false;
            await apply();
            return true;
        },
        reset,
        reload: async () => {
            if (!draft && !review) return false;
            await refreshShared();
            return true;
        },
        async init() {
            await lists();
            const params = new URLSearchParams(location.search);
            if (params.get("review"))
                await loadReview(params.get("review"));
            else if (params.get("draft"))
                await loadDraft(params.get("draft"), true);
            if (state.catalog.validation)
                showError(new Error(state.catalog.validation));
            initialized = true;
            contextLabel();
            if (refreshPending) queueRefresh();
        },
    };
}
