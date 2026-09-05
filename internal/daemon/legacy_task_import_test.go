package daemon

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/store"
)

func legacyRow(kind, subject, meta string) store.LegacyTaskRecord {
	at := time.Now().UTC()
	return store.LegacyTaskRecord{
		ID:            kind + ":" + subject,
		Kind:          kind,
		Subject:       subject,
		State:         "queued",
		Attempts:      1,
		NextAttemptAt: at,
		MetaJSON:      meta,
		CreatedAt:     at,
		UpdatedAt:     at,
	}
}

func translateAndWrite(t *testing.T, d *Daemon, rows ...store.LegacyTaskRecord) {
	t.Helper()
	for _, rec := range rows {
		if err := d.store.UpsertJob(d.legacyTaskToJob(rec)); err != nil {
			t.Fatalf("write translated job for %s: %v", rec.ID, err)
		}
	}
}

func TestImportCarriesEachKindsInputsOntoItsPayload(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	translateAndWrite(t, d,
		legacyRow(reconcileKind, "t-3", `{"reconcile_inputs":"{\"TicketID\":\"t-3\",\"Title\":\"ship it\"}"}`),
	)

	d.startJobQueue()
	runner := d.jobQueueRef()
	t.Cleanup(runner.Stop)

	reconcile, err := runner.GetByKey(reconcileKind, "t-3")
	if err != nil || reconcile == nil {
		t.Fatalf("imported reconcile job: %v (%+v)", err, reconcile)
	}
	in, err := reconcileInputsFromJob(reconcile)
	if err != nil {
		t.Fatalf("imported reconcile inputs: %v", err)
	}
	if in.TicketID != "t-3" || in.Title != "ship it" {
		t.Fatalf("imported reconcile lost its inputs: %+v", in)
	}
}

func TestImportKeepsARowWithUnreadableMeta(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	translateAndWrite(t, d, legacyRow(reconcileKind, "t-broken", `{not json`))

	job, ok, err := d.store.GetJob("reconcile:t-broken")
	if err != nil {
		t.Fatalf("get imported job: %v", err)
	}
	if !ok {
		t.Fatal("a row with unreadable meta was dropped instead of imported")
	}
	if job.Payload != "" {
		t.Fatalf("unreadable meta produced a payload: %q", job.Payload)
	}
}
