import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { useHarnesses } from '../../hooks/useHarnesses';
import { useSettings } from '../../contexts/SettingsContext';
import type { SessionAgent } from '../../types/sessionAgent';
import {
  agentLabel,
  type AgentAvailability,
  checkingForHarnessLabel,
  getAgentCapabilities,
  harnessAvailability,
  hasAnySelectableAgents,
  isAgentSelectable,
  resolvePreferredAgent,
} from '../../utils/agentAvailability';
import { keyCombo } from '../../shortcuts/formatShortcut';
import { modifierGlyphs } from '../../shortcuts/platform';
import { HarnessCheckingMark } from '../HarnessCheckingMark';

const DEFAULT_AGENT_AVAILABILITY: AgentAvailability = {
  shell: 'available',
  codex: 'available',
  claude: 'available',
};

const SESSION_AGENT_KEY = 'new_session_agent';
const SESSION_YOLO_KEYS = ['new_session_yolo_local', 'new_session_yolo'];
const TERMINAL_AGENT: SessionAgent = 'shell';

const normalizeAgent = (value?: string): SessionAgent | null => {
  if (!value) return null;
  const lower = value.toLowerCase().trim();
  return lower || null;
};

function parseBooleanSetting(value?: string): boolean | null {
  if (typeof value !== 'string') {
    return null;
  }
  const normalized = value.trim().toLowerCase();
  if (normalized === 'true') {
    return true;
  }
  if (normalized === 'false') {
    return false;
  }
  return null;
}

function pickerAgentLabel(agent: SessionAgent): string {
  return agent === TERMINAL_AGENT ? 'Terminal' : agentLabel(agent);
}

function withoutTerminalAgent(availability: AgentAvailability): AgentAvailability {
  const { [TERMINAL_AGENT]: _terminal, ...rest } = availability;
  return rest;
}

export function resolvePickerAgent(
  preferred: SessionAgent | undefined,
  availability: AgentAvailability,
  fallback: SessionAgent = 'codex',
): SessionAgent {
  if (preferred === TERMINAL_AGENT && isAgentSelectable(availability, TERMINAL_AGENT)) {
    return TERMINAL_AGENT;
  }
  const nonTerminalAvailability = withoutTerminalAgent(availability);
  if (hasAnySelectableAgents(nonTerminalAvailability)) {
    return resolvePreferredAgent(preferred, nonTerminalAvailability, fallback);
  }
  return isAgentSelectable(availability, TERMINAL_AGENT)
    ? TERMINAL_AGENT
    : resolvePreferredAgent(preferred, availability, fallback);
}

export function useSessionControls({
  isOpen,
  initialAgent,
  agentAvailability,
  resetAgentOnOpen = false,
}: {
  resetAgentOnOpen?: boolean;
  isOpen: boolean;
  initialAgent?: SessionAgent;
  agentAvailability?: AgentAvailability;
}) {
  const { settings, setSetting } = useSettings();
  const [agent, setAgent] = useState<SessionAgent>('claude');
  const [yoloMode, setYoloMode] = useState(false);
  const previousInitialAgent = useRef<SessionAgent | undefined>(undefined);
  const agentCapabilities = useMemo(() => getAgentCapabilities(settings), [settings]);
  const effectiveAgentAvailability = useMemo<AgentAvailability>(
    () => ({ ...(agentAvailability || DEFAULT_AGENT_AVAILABILITY), [TERMINAL_AGENT]: 'available' }),
    [agentAvailability],
  );
  const hasAvailableAgents = hasAnySelectableAgents(effectiveAgentAvailability);
  const savedYoloMode = useMemo(() => {
    for (const key of SESSION_YOLO_KEYS) {
      const parsed = parseBooleanSetting(settings[key]);
      if (parsed != null) {
        return parsed;
      }
    }
    return false;
  }, [settings]);

  const daemonApi = useOptionalDaemonApi();
  const {
    harnesses,
    hasCatalog,
    loading: harnessesLoading,
    error: harnessesError,
    retry: retryHarnesses,
  } = useHarnesses(isOpen);
  const orderedAgentList = useMemo(() => {
    const reported = Object.keys(effectiveAgentAvailability) as SessionAgent[];
    if (!daemonApi) return [...new Set([TERMINAL_AGENT, ...reported])];
    const discovered = harnesses.map((harness) => harness.id);
    const interactive =
      hasCatalog || harnessesError
        ? reported
            .filter((candidate) => !discovered.includes(candidate))
            .sort((a, b) => a.localeCompare(b))
        : [];
    return [...new Set([TERMINAL_AGENT, ...discovered, ...interactive])];
  }, [effectiveAgentAvailability, harnesses, hasCatalog, harnessesError, daemonApi]);
  const agentShortcutByName = useMemo(() => {
    const shortcuts = new Map<SessionAgent, number>();
    if (daemonApi && !hasCatalog) return shortcuts;
    orderedAgentList
      .filter((candidate) => candidate !== TERMINAL_AGENT)
      .forEach((candidate, index) => {
        shortcuts.set(candidate, index + 1);
      });
    return shortcuts;
  }, [orderedAgentList, daemonApi, hasCatalog]);

  const savedAgent = normalizeAgent(settings[SESSION_AGENT_KEY]);
  const yoloSupported = Boolean(agentCapabilities[agent]?.yolo);

  useEffect(() => {
    if (!savedAgent || initialAgent) return;
    const resolvedSavedAgent = resolvePickerAgent(
      savedAgent,
      effectiveAgentAvailability,
      'codex',
    );
    setAgent((prev) => (prev === resolvedSavedAgent ? prev : resolvedSavedAgent));
  }, [effectiveAgentAvailability, savedAgent, initialAgent]);

  useEffect(() => {
    const resolvedAgent = resolvePickerAgent(agent, effectiveAgentAvailability, 'codex');
    if (resolvedAgent !== agent) {
      setAgent(resolvedAgent);
    }
  }, [agent, effectiveAgentAvailability]);

  useEffect(() => {
    setYoloMode((prev) => (prev === savedYoloMode ? prev : savedYoloMode));
  }, [savedYoloMode]);

  useEffect(() => {
    if (!yoloSupported) {
      setYoloMode(false);
    }
  }, [yoloSupported]);

  const handleAgentChange = useCallback(
    (nextAgent: SessionAgent) => {
      if (!isAgentSelectable(effectiveAgentAvailability, nextAgent)) {
        return;
      }
      setAgent(nextAgent);
      if (nextAgent === TERMINAL_AGENT) {
        return;
      }
      setSetting(SESSION_AGENT_KEY, nextAgent);
    },
    [effectiveAgentAvailability, setSetting],
  );

  const toggleYoloMode = useCallback(() => {
    if (!yoloSupported) {
      return;
    }
    const nextYoloMode = !yoloMode;
    setSetting(SESSION_YOLO_KEYS[0], String(nextYoloMode));
    setYoloMode(nextYoloMode);
  }, [setSetting, yoloMode, yoloSupported]);

  useEffect(() => {
    if (!isOpen) return;
    if (initialAgent || resetAgentOnOpen || previousInitialAgent.current) {
      setAgent(
        resolvePickerAgent(initialAgent || savedAgent || 'claude', effectiveAgentAvailability),
      );
    }
    previousInitialAgent.current = initialAgent;
  }, [isOpen, initialAgent, resetAgentOnOpen]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleControlKey = (e: React.KeyboardEvent) => {
    if (!e.altKey || e.metaKey || e.ctrlKey) return false;
    const digit = /^Digit([1-9])$/.exec(e.code);
    if (e.code === 'KeyT') handleAgentChange(TERMINAL_AGENT);
    else if (e.code === 'KeyQ') toggleYoloMode();
    else if (digit) {
      const nextAgent = [...agentShortcutByName].find(
        ([, number]) => number === Number(digit[1]),
      )?.[0];
      if (nextAgent) handleAgentChange(nextAgent);
    } else return false;
    e.preventDefault();
    return true;
  };
  return {
    agent,
    labelForAgent: (candidate: SessionAgent) => settings[`${candidate}_display_name`]?.trim() || pickerAgentLabel(candidate),
    yoloMode,
    yoloSupported,
    effectiveAgentAvailability,
    hasAvailableAgents,
    harnessesLoading,
    harnessesError,
    retryHarnesses,
    orderedAgentList,
    agentShortcutByName,
    handleAgentChange,
    toggleYoloMode,
    handleControlKey,
  };
}

export type MissingHarnesses = { machine: string; harnesses: ReadonlySet<SessionAgent> };

export function SessionControls({
  controls,
  compact = false,
  missing,
  targetIsOutpost = false,
}: {
  controls: ReturnType<typeof useSessionControls>;
  compact?: boolean;
  missing?: MissingHarnesses;
  targetIsOutpost?: boolean;
}) {
  const {
    agent,
    labelForAgent,
    yoloMode,
    yoloSupported,
    effectiveAgentAvailability,
    hasAvailableAgents,
    harnessesLoading,
    harnessesError,
    retryHarnesses,
    orderedAgentList,
    agentShortcutByName,
    handleAgentChange,
    toggleYoloMode,
  } = controls;
  const noAgentsMessage = 'No supported agent CLI found in PATH.';
  const toggles = (
    <>
      {(!compact || agent !== TERMINAL_AGENT) && (
        <div className="picker-chief-bar">
          <div className="picker-agent-label">YOLO</div>
          <div className="picker-chief-controls">
            <div className="agent-toggle">
              <button
                type="button"
                className={`agent-option ${yoloMode ? 'active' : ''}`}
                data-testid="location-picker-yolo-toggle"
                role="switch"
                aria-checked={yoloMode}
                disabled={!yoloSupported}
                onMouseDown={(e) => e.preventDefault()}
                onClick={toggleYoloMode}
                title={
                  yoloSupported
                    ? 'Run the agent without asking for permission'
                    : `${agentLabel(agent)} does not support yolo mode`
                }
              >
                <span className="agent-option-name">
                  {compact ? 'YOLO' : yoloMode ? 'On' : 'Off'}
                </span>
                {yoloSupported && <kbd className="agent-shortcut">{keyCombo('alt', 'Q')}</kbd>}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
  return (
    <>
      {' '}
      <div className="picker-agent-bar">
        <div className="picker-agent-label">SESSION AGENT</div>
        <div className="picker-agent-controls">
          {harnessesLoading && <span role="status">Loading harnesses…</span>}
          {harnessesError && (
            <span role="alert">
              {harnessesError}
              <button type="button" onClick={retryHarnesses}>
                Retry
              </button>
            </span>
          )}
          {compact && (
            <span className="launcher-key-legend" aria-hidden="true">
              {modifierGlyphs().alt}
            </span>
          )}
          <div className="agent-toggle" role="radiogroup" aria-label="Session agent">
            {orderedAgentList.map((candidate) => {
              const availability = harnessAvailability(effectiveAgentAvailability, candidate);
              const available = availability !== 'unavailable';
              const shortcutNumber = agentShortcutByName.get(candidate);
              const key =
                candidate === TERMINAL_AGENT
                  ? 'T'
                  : shortcutNumber && shortcutNumber <= 9
                    ? String(shortcutNumber)
                    : null;
              const shortcut = key && (compact ? key : keyCombo('alt', key));
              const label = labelForAgent(candidate);
              const absent = missing?.harnesses.has(candidate) && missing.machine;
              const checking = availability === 'checking' && !targetIsOutpost && !absent;
              return (
                <button
                  key={candidate}
                  type="button"
                  className={`agent-option ${agent === candidate ? 'active' : ''}${absent ? ' agent-option--missing' : ''}`}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => handleAgentChange(candidate)}
                  role="radio"
                  aria-checked={agent === candidate}
                  disabled={!available}
                  title={
                    !available
                      ? `${label} is not available on this machine`
                      : absent
                        ? `${label} is not installed on ${absent}`
                        : checking
                          ? checkingForHarnessLabel(label)
                          : undefined
                  }
                >
                  <span className="agent-option-name">{label}</span>
                  {available && shortcut && <kbd className="agent-shortcut">{shortcut}</kbd>}
                  {checking && <HarnessCheckingMark />}
                </button>
              );
            })}
          </div>
        </div>
      </div>
      {compact ? <div className="launcher-toggle-group">{toggles}</div> : toggles}
      {!hasAvailableAgents && <div className="picker-agent-warning">{noAgentsMessage}</div>}
    </>
  );
}
