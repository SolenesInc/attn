import { describe, expect, it } from 'vitest';
import {
  LOCAL_SNAPSHOT_FORMAT,
  classifyAttachRestore,
  createAttachRequestContext,
  enqueuePendingAttachOutput,
  planAttachResultEffects,
  planAttachedRuntimeGeometry,
  planLivePtyOutput,
} from './attachPlanning';

describe('attachPlanning', () => {
  describe('server-authoritative Ghostty snapshot', () => {
    it('classifies a Ghostty snapshot as the authoritative restore payload', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasSnapshot).toBe(true);
      expect(plan.restoreCols).toBe(80);
      expect(plan.restoreRows).toBe(24);
    });

    it('restores at the snapshot grid even when it differs from the requested geometry', () => {
      const plan = classifyAttachRestore({
        cols: 100,
        rows: 40,
        snapshot: { cols: 100, rows: 40, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
      }, createAttachRequestContext({ cols: 58, rows: 46 }, 'same_app_remount'));

      expect(plan.hasSnapshot).toBe(true);
      expect(plan.restoreCols).toBe(100);
      expect(plan.restoreRows).toBe(40);
    });

    it('treats an empty Ghostty snapshot as no restore payload', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: '', format: LOCAL_SNAPSHOT_FORMAT },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasSnapshot).toBe(false);
    });

    it('declines a snapshot written in a different build format', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: 'deadbeef1234' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount'));

      expect(plan.hasSnapshot).toBe(false);
      expect(plan.restoreCols).toBe(80);
    });

    it('declines a snapshot that names no format', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount'));

      expect(plan.hasSnapshot).toBe(false);
    });

    it('leaves a foreign-format attach unreset, on the client watermark', () => {
      const restorePlan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: 'deadbeef1234' },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 7,
          snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: 'deadbeef1234' },
        },
        restorePlan,
        previousSeq: 6,
      });

      expect(effects.shouldReset).toBe(false);
      expect(effects.restoreAction).toEqual({ kind: 'none' });
      expect(effects.nextSeq).toBe(6);
    });

    it('plans a snapshot_restore reset and emits the vt dump as the restore action', () => {
      const restorePlan = classifyAttachRestore({
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 7,
          snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
        },
        restorePlan,
        previousSeq: 6,
      });

      expect(effects.shouldReset).toBe(true);
      expect(effects.resetReason).toBe('snapshot_restore');
      expect(effects.restoreAction).toEqual({
        kind: 'ghostty_snapshot',
        data: 'ZHVtcA==',
      });
      expect(effects.nextSeq).toBe(7);
    });

    it('reports the snapshot grid as the authoritative restore geometry', () => {
      const attachContext = createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount');
      const plan = planAttachedRuntimeGeometry({
        cols: 80,
        rows: 24,
      }, {
        cols: 80,
        rows: 24,
        snapshot: { cols: 80, rows: 24, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
      }, {
        attachPolicy: 'same_app_remount',
        attachContext,
      });

      expect(plan.hasSnapshot).toBe(true);
      expect(plan.restoreGeometryMatches).toBe(true);
    });
  });

  describe('snapshot-less reattach', () => {
    it('keeps client state and its own watermark on a snapshot-less restore reattach', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'relaunch_restore'));

      expect(plan.hasSnapshot).toBe(false);

      const effects = planAttachResultEffects({
        attachResult: { last_seq: 12 },
        restorePlan: plan,
        previousSeq: 11,
        queuedOutputs: [{ data: 'live-12', seq: 12 }],
      });

      expect(effects.shouldReset).toBe(false);
      expect(effects.resetReason).toBe(null);
      expect(effects.restoreAction.kind).toBe('none');
      expect(effects.queuedOutputsToEmit).toEqual([{ data: 'live-12', seq: 12 }]);
      expect(effects.nextSeq).toBe(12);
    });

    it('does not reset or drop queued output on a snapshot-less same-app remount', () => {
      const plan = classifyAttachRestore({
        cols: 80,
        rows: 24,
      }, createAttachRequestContext({ cols: 80, rows: 24 }, 'same_app_remount'));

      expect(plan.hasSnapshot).toBe(false);

      const effects = planAttachResultEffects({
        attachResult: { last_seq: 20 },
        restorePlan: plan,
        previousSeq: 15,
        queuedOutputs: [
          { data: 'queued-16', seq: 16 },
          { data: 'queued-20', seq: 20 },
        ],
      });

      expect(effects.shouldReset).toBe(false);
      expect(effects.resetReason).toBe(null);
      expect(effects.restoreAction.kind).toBe('none');
      expect(effects.queuedOutputsToEmit).toEqual([
        { data: 'queued-16', seq: 16 },
        { data: 'queued-20', seq: 20 },
      ]);
      expect(effects.nextSeq).toBe(20);
    });
  });

  describe('attached runtime geometry', () => {
    it('does not request PTY reconcile work for same-app remounts at matching geometry', () => {
      const plan = planAttachedRuntimeGeometry({
        cols: 58,
        rows: 46,
        shell: false,
      }, {
        cols: 58,
        rows: 46,
      }, {
        attachPolicy: 'same_app_remount',
      });

      expect(plan.resizeRequired).toBe(false);
      expect(plan.strategy).toBe('none');
    });

    it('preserves daemon geometry when same-app requested geometry was not measured', () => {
      const plan = planAttachedRuntimeGeometry({
        cols: 80,
        rows: 24,
        shell: false,
      }, {
        cols: 45,
        rows: 35,
      }, {
        attachPolicy: 'same_app_remount',
        requestedGeometryAuthoritative: false,
      });

      expect(plan.resizeRequired).toBe(false);
      expect(plan.strategy).toBe('preserve_attached');
    });

    it('reconciles an explicitly measured same-app remount geometry', () => {
      const plan = planAttachedRuntimeGeometry({
        cols: 58,
        rows: 46,
        shell: false,
      }, {
        cols: 80,
        rows: 24,
      }, {
        attachPolicy: 'same_app_remount',
        requestedGeometryAuthoritative: true,
      });

      expect(plan.resizeRequired).toBe(true);
      expect(plan.strategy).toBe('resize');
    });

    it('preserves daemon geometry during relaunch restore rather than resizing from bootstrap layout', () => {
      const attachContext = createAttachRequestContext({
        cols: 58,
        rows: 46,
        agent: 'claude',
      }, 'relaunch_restore');
      const plan = planAttachedRuntimeGeometry({
        cols: 58,
        rows: 46,
        shell: false,
      }, {
        cols: 37,
        rows: 46,
      }, {
        attachPolicy: 'relaunch_restore',
        attachContext,
      });

      expect(plan.resizeRequired).toBe(false);
      expect(plan.strategy).toBe('preserve_attached');
      expect(plan.ptyGeometryMatches).toBe(false);
      expect(plan.restoreGeometryMatches).toBe(false);
    });
  });

  describe('sequence dedup', () => {
    it('filters queued output already covered by the attach restore payload', () => {
      const restorePlan = classifyAttachRestore({
        cols: 58,
        rows: 46,
        snapshot: { cols: 58, rows: 46, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
      }, createAttachRequestContext({ cols: 58, rows: 46 }, 'relaunch_restore'));

      const effects = planAttachResultEffects({
        attachResult: {
          last_seq: 10,
          snapshot: { cols: 58, rows: 46, snapshot_b64: 'ZHVtcA==', format: LOCAL_SNAPSHOT_FORMAT },
        },
        restorePlan,
        queuedOutputs: [
          { data: 'old', seq: 9 },
          { data: 'equal', seq: 10 },
          { data: 'new', seq: 11 },
          { data: 'noseq' },
        ],
      });

      expect(effects.restoreAction.kind).toBe('ghostty_snapshot');
      expect(effects.queuedOutputsToEmit).toEqual([
        { data: 'new', seq: 11 },
        { data: 'noseq' },
      ]);
      expect(effects.nextSeq).toBe(11);
    });

    it('bounds queued attach output by dropping the oldest chunk first', () => {
      const queued = enqueuePendingAttachOutput(
        [
          { data: 'one', seq: 1 },
          { data: 'two', seq: 2 },
        ],
        { data: 'three', seq: 3 },
        2,
      );

      expect(queued).toEqual([
        { data: 'two', seq: 2 },
        { data: 'three', seq: 3 },
      ]);
    });

    it('drops live PTY output whose sequence does not advance past the last applied chunk', () => {
      expect(planLivePtyOutput({ incomingSeq: 10, lastSeq: 10 })).toEqual({
        shouldDropAsStale: true,
        nextSeq: 10,
      });
      expect(planLivePtyOutput({ incomingSeq: 9, lastSeq: 10 })).toEqual({
        shouldDropAsStale: true,
        nextSeq: 10,
      });
      expect(planLivePtyOutput({ incomingSeq: 11, lastSeq: 10 })).toEqual({
        shouldDropAsStale: false,
        nextSeq: 11,
      });
    });

    it('keeps seq-less live PTY output because it cannot be proven stale', () => {
      expect(planLivePtyOutput({ lastSeq: 10 })).toEqual({
        shouldDropAsStale: false,
        nextSeq: 10,
      });
    });
  });
});
