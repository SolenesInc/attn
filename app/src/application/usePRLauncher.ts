import { useCallback, useMemo, useRef, useState } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { DaemonPR } from '../hooks/useDaemonSocket';
import { useOpenPR, type OpenPRProgress } from '../hooks/useOpenPR';
import { formatShortcut } from '../shortcuts/formatShortcut';
import { normalizeSessionAgent } from '../types/sessionAgent';
import {
  getAgentAvailability,
  hasAnyAvailableAgents,
  resolvePreferredAgent,
} from '../utils/agentAvailability';
import { AppContentProps, OpenPRLauncherJob } from './appSupport';
import { useWorkspaceCreation } from './useWorkspaceCreation';

interface Options {
  settings: AppContentProps['settings'];
  createWorkspaceSession: ReturnType<typeof useWorkspaceCreation>['createWorkspaceSession'];
  selectCreatedSession: (id: string) => boolean;
}
export function usePRLauncher({ settings, createWorkspaceSession, selectCreatedSession }: Options) {
  const { sendRefreshPRs, sendFetchPRDetails, sendEnsureRepo, sendCreateWorktreeFromBranch } =
    useDaemonApi();
  const agentAvailability = useMemo(() => getAgentAvailability(settings), [settings]);
  const hasAvailableAgents = hasAnyAvailableAgents(agentAvailability);
  const [openPRLauncherJob, setOpenPRLauncherJob] = useState<OpenPRLauncherJob | null>(null);
  const openPRLauncherIdRef = useRef(0);
  const openPR = useOpenPR({
    settings,
    sendFetchPRDetails,
    sendEnsureRepo,
    sendCreateWorktreeFromBranch,
    createSession: createWorkspaceSession,
  });

  const handleOpenPR = useCallback(
    async (pr: DaemonPR) => {
      console.log(`[App] Open PR requested: ${pr.repo}#${pr.number} - ${pr.title}`);

      if (!hasAvailableAgents) {
        alert('No supported agent CLI found in PATH.');
        return;
      }
      const configuredDefaultAgent = normalizeSessionAgent(settings.new_session_agent, 'claude');
      const defaultAgent = resolvePreferredAgent(
        configuredDefaultAgent,
        agentAvailability,
        'codex',
      );
      const launcherId = openPRLauncherIdRef.current + 1;
      openPRLauncherIdRef.current = launcherId;
      const isActiveLauncher = () => openPRLauncherIdRef.current === launcherId;
      const updateLauncherProgress = (progress: OpenPRProgress) => {
        setOpenPRLauncherJob((current) =>
          current?.id === launcherId ? { ...current, progress } : current,
        );
      };

      setOpenPRLauncherJob({
        id: launcherId,
        pr,
        progress: { step: pr.head_branch ? 'ensuring_repo' : 'fetching_pr_details' },
      });
      const result = await openPR(pr, defaultAgent, { onProgress: updateLauncherProgress }).finally(
        () => {
          if (isActiveLauncher()) {
            setOpenPRLauncherJob(null);
          }
        },
      );
      if (!isActiveLauncher()) {
        return;
      }
      if (result.success) {
        selectCreatedSession(result.sessionId);
        console.log(`[App] Worktree created at ${result.worktreePath}`);
        return;
      }

      const errorMsg = result.error.message || '';
      switch (result.error.kind) {
        case 'missing_projects_directory':
          alert(
            'Please configure your Projects Directory in Settings first.\n\nThis tells the app where to find your local git repositories.',
          );
          break;
        case 'missing_head_branch':
          alert(
            `PR branch information not available.\n\nTry refreshing PRs (${formatShortcut('session.refreshPRs')}) to fetch branch details.`,
          );
          break;
        case 'fetch_pr_details_failed':
          alert(
            `Failed to fetch PR details.\n\n${errorMsg || `Try refreshing PRs (${formatShortcut('session.refreshPRs')}) and try again.`}`,
          );
          break;
        case 'ensure_repo_failed':
        case 'create_worktree_failed':
        case 'create_session_failed':
        case 'unknown': {
          if (errorMsg.includes('clone failed')) {
            alert(
              `Failed to clone repository ${pr.repo}.\n\nError: ${errorMsg}\n\nCheck your network connection and GitHub access.`,
            );
          } else if (errorMsg.includes('already exists')) {
            alert(`A worktree for this branch may already exist.\n\nError: ${errorMsg}`);
          } else {
            alert(`Failed to open PR: ${errorMsg || 'Unknown error'}`);
          }
          break;
        }
      }
    },
    [
      agentAvailability,
      hasAvailableAgents,
      openPR,
      selectCreatedSession,
      settings.new_session_agent,
    ],
  );

  const [isRefreshingPRs, setIsRefreshingPRs] = useState(false);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  const handleRefreshPRs = useCallback(async () => {
    setIsRefreshingPRs(true);
    setRefreshError(null);
    try {
      const result = await sendRefreshPRs();
      if (!result.success) {
        setRefreshError(result.error || 'Refresh failed');
      }
    } catch (err) {
      setRefreshError(err instanceof Error ? err.message : 'Refresh failed');
    } finally {
      setIsRefreshingPRs(false);
    }
  }, [sendRefreshPRs]);

  return { openPRLauncherJob, handleOpenPR, isRefreshingPRs, refreshError, handleRefreshPRs };
}
