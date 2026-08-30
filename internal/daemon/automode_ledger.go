package daemon

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/automode"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
)

// Without it, a record the store's row cap has since trimmed is re-imported and trimmed forever.
const settingAutoModeDenialCursor = "automode_denial_ledger_cursor"

func autoModeDenialLedgerPath() string {
	if override := strings.TrimSpace(os.Getenv(automode.DenialLedgerEnvVar)); override != "" {
		return override
	}
	return automode.DenialLedgerPath(config.DataDir())
}

type autoModeLedgerReconcile struct {
	Imported  int
	Dropped   int
	Malformed int
}

var autoModeLedgerMu sync.Mutex

func (d *Daemon) reconcileAutoModeDenialLedger() autoModeLedgerReconcile {
	if d.store == nil {
		return autoModeLedgerReconcile{}
	}
	// The cursor is read, compared and written across the whole pass: two concurrent readers would import the same records.
	autoModeLedgerMu.Lock()
	defer autoModeLedgerMu.Unlock()

	path := autoModeDenialLedgerPath()
	reading, err := automode.ReadDenialLedger(path)
	if err != nil {
		d.logf("automode: reading denial ledger %s: %v", path, err)
		return autoModeLedgerReconcile{}
	}
	if len(reading.Records) == 0 && reading.Dropped == 0 && reading.Malformed == 0 {
		return autoModeLedgerReconcile{}
	}

	cursor := parseAutoModeDenialCursor(d.store.GetSetting(settingAutoModeDenialCursor))
	stored, err := d.store.ListAutoModeDenials(store.AutoModeDenialRows)
	if err != nil {
		d.logf("automode: listing denials to reconcile the ledger: %v", err)
		return autoModeLedgerReconcile{}
	}
	known := make(map[string]struct{}, len(stored))
	for _, denial := range stored {
		known[autoModeDenialKey(denial.SessionID, denial.Signature, denial.CreatedAt)] = struct{}{}
	}

	out := autoModeLedgerReconcile{Dropped: reading.Dropped, Malformed: reading.Malformed}
	newest := cursor
	for _, record := range reading.Records {
		if record.At.After(newest) {
			newest = record.At
		}
		if !record.At.After(cursor) {
			continue
		}
		if _, ok := known[autoModeDenialKey(record.SessionID, record.Action, record.At)]; ok {
			continue
		}
		if _, _, err := d.store.RecordAutoModeDenial(store.AutoModeDenial{
			SessionID: record.SessionID,
			Tool:      record.Tool,
			Signature: record.Action,
			Reason:    record.Reason,
			Rule:      record.Rule,
		}, record.At); err != nil {
			d.logf("automode: importing a denial from the ledger: %v", err)
			// Leave the cursor where it was so this record is tried again.
			return out
		}
		out.Imported++
		d.logf("automode: recovered a denial the relay never delivered session=%s rule=%s action=%q at=%s",
			record.SessionID, record.Rule, record.Action, record.At.Format(time.RFC3339))
	}
	if newest.After(cursor) {
		d.store.SetSetting(settingAutoModeDenialCursor, newest.UTC().Format(time.RFC3339Nano))
	}
	if out.Dropped > 0 {
		d.logf("automode: the denial ledger has dropped %d records to rotation", out.Dropped)
	}
	if out.Malformed > 0 {
		d.logf("automode: the denial ledger holds %d unreadable lines", out.Malformed)
	}
	return out
}

// Dedup key across both arrival paths: the relay stores the session's own timestamp verbatim.
func autoModeDenialKey(sessionID, signature string, at time.Time) string {
	return fmt.Sprintf("%s|%s|%s", sessionID, at.UTC().Format(time.RFC3339Nano), signature)
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func parseAutoModeDenialCursor(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func autoModeLedgerNote(reconcile autoModeLedgerReconcile) string {
	parts := []string{}
	if reconcile.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d older %s dropped when the local ledger rotated",
			reconcile.Dropped, plural(reconcile.Dropped, "denial was", "denials were")))
	}
	if reconcile.Malformed > 0 {
		parts = append(parts, fmt.Sprintf("%d ledger %s not be read",
			reconcile.Malformed, plural(reconcile.Malformed, "line could", "lines could")))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}
