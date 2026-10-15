import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { useHarnesses } from '../../hooks/useHarnesses';
import { useSettings } from '../../contexts/SettingsContext';
import type { SessionAgent } from '../../types/sessionAgent';
import {
  agentLabel,
  type AgentAvailability,
  getAgentCapabilities,
  hasAnyAvailableAgents,
  isAgentAvailable,
  resolvePreferredAgent,
} from '../../utils/agentAvailability';
import { keyCombo } from '../../shortcuts/formatShortcut';

const DEFAULT_AGENT_AVAILABILITY: AgentAvailability = {
  shell: true,
  codex: true,
  claude: true,
  copilot: true,
};

const SESSION_AGENT_KEY = 'new_session_agent';
const SESSION_YOLO_KEYS = ['new_session_yolo_local', 'new_session_yolo'];
const AUTOMODE_DEFAULT_KEY = 'automode_enabled_default';
const AUTOMODE_CAPABILITY = 'auto_mode';
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
  if (preferred === TERMINAL_AGENT && availability[TERMINAL_AGENT]) {
    return TERMINAL_AGENT;
  }
  const nonTerminalAvailability = withoutTerminalAgent(availability);
  if (hasAnyAvailableAgents(nonTerminalAvailability)) {
    return resolvePreferredAgent(preferred, nonTerminalAvailability, fallback);
  }
  return availability[TERMINAL_AGENT]
    ? TERMINAL_AGENT
    : resolvePreferredAgent(preferred, availability, fallback);
}

export function useSessionControls({
  isOpen,
  initialAgent,
  agentAvailability,
  chiefExists = false,
  resetAgentOnOpen = false,
}: {
  resetAgentOnOpen?: boolean;
  isOpen: boolean;
  initialAgent?: SessionAgent;
  agentAvailability?: AgentAvailability;
  chiefExists?: boolean;
}) {
  const { settings, setSetting } = useSettings();
  const [agent, setAgent] = useState<SessionAgent>('claude');
  const [yoloMode, setYoloMode] = useState(false);
  const [chiefOfStaff, setChiefOfStaff] = useState(false);
  const [autoMode, setAutoMode] = useState(true);
  const autoModeTouchedRef = useRef(false);
  const previousInitialAgent = useRef<SessionAgent | undefined>(undefined);
  const agentCapabilities = useMemo(() => getAgentCapabilities(settings), [settings]);
  const effectiveAgentAvailability = useMemo<AgentAvailability>(
    () => ({ ...(agentAvailability || DEFAULT_AGENT_AVAILABILITY), [TERMINAL_AGENT]: true }),
    [agentAvailability],
  );
  const hasAvailableAgents = hasAnyAvailableAgents(effectiveAgentAvailability);
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
  const autoModeSupported = Boolean(agentCapabilities[agent]?.[AUTOMODE_CAPABILITY]);
  const autoModeDefault = parseBooleanSetting(settings[AUTOMODE_DEFAULT_KEY]) ?? true;
  const chiefToggleEligible = !chiefExists && (agent === 'claude' || agent === 'codex');

  useEffect(() => {
    if (!savedAgent || initialAgent) return;
    const resolvedSavedAgent = resolvePickerAgent(savedAgent, effectiveAgentAvailability, 'codex');
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

  useEffect(() => {
    if (!chiefToggleEligible) {
      setChiefOfStaff(false);
    }
  }, [chiefToggleEligible]);

  useEffect(() => {
    if (autoModeTouchedRef.current) return;
    setAutoMode((prev) => (prev === autoModeDefault ? prev : autoModeDefault));
  }, [autoModeDefault]);

  const handleAgentChange = useCallback(
    (nextAgent: SessionAgent) => {
      if (!isAgentAvailable(effectiveAgentAvailability, nextAgent)) {
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

  const toggleAutoMode = () => {
    if (!autoModeSupported) return;
    autoModeTouchedRef.current = true;
    setAutoMode((value) => !value);
  };
  const handleControlKey = (e: React.KeyboardEvent) => {
    if (!e.altKey || e.metaKey || e.ctrlKey) return false;
    const digit = /^Digit([1-9])$/.exec(e.code);
    if (e.code === 'KeyT') handleAgentChange(TERMINAL_AGENT);
    else if (e.code === 'KeyQ') toggleYoloMode();
    else if (e.code === 'KeyA') toggleAutoMode();
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
    yoloMode,
    chiefOfStaff,
    autoMode,
    yoloSupported,
    autoModeSupported,
    chiefToggleEligible,
    effectiveAgentAvailability,
    hasAvailableAgents,
    harnessesLoading,
    harnessesError,
    retryHarnesses,
    orderedAgentList,
    agentShortcutByName,
    handleAgentChange,
    toggleYoloMode,
    toggleAutoMode,
    setChiefOfStaff,
    handleControlKey,
  };
}

export function SessionControls({
  controls,
  compact = false,
}: {
  controls: ReturnType<typeof useSessionControls>;
  compact?: boolean;
}) {
  const {
    agent,
    yoloMode,
    chiefOfStaff,
    autoMode,
    yoloSupported,
    autoModeSupported,
    chiefToggleEligible,
    effectiveAgentAvailability,
    hasAvailableAgents,
    harnessesLoading,
    harnessesError,
    retryHarnesses,
    orderedAgentList,
    agentShortcutByName,
    handleAgentChange,
    toggleYoloMode,
    toggleAutoMode,
    setChiefOfStaff,
  } = controls;
  const noAgentsMessage = 'No supported agent CLI found in PATH.';
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
          <div className="agent-toggle" role="radiogroup" aria-label="Session agent">
            {orderedAgentList.map((candidate) => {
              const available = isAgentAvailable(effectiveAgentAvailability, candidate);
              const shortcutNumber = agentShortcutByName.get(candidate);
              const shortcut =
                candidate === TERMINAL_AGENT
                  ? keyCombo('alt', 'T')
                  : shortcutNumber && shortcutNumber <= 9
                    ? keyCombo('alt', String(shortcutNumber))
                    : null;
              const label = pickerAgentLabel(candidate);
              return (
                <button
                  key={candidate}
                  type="button"
                  className={`agent-option ${agent === candidate ? 'active' : ''}`}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => handleAgentChange(candidate)}
                  role="radio"
                  aria-checked={agent === candidate}
                  disabled={!available}
                  title={!available ? `${label} is not available on this machine` : undefined}
                >
                  <span className="agent-option-name">{label}</span>
                  {available && shortcut && <kbd className="agent-shortcut">{shortcut}</kbd>}
                </button>
              );
            })}
          </div>
        </div>
      </div>
      {autoModeSupported && (
        <div className="picker-chief-bar">
          <div className="picker-agent-label">AUTO MODE</div>
          <div className="picker-chief-controls">
            <div className="agent-toggle">
              <button
                type="button"
                className={`agent-option ${autoMode ? 'active' : ''}`}
                data-testid="location-picker-automode-toggle"
                role="switch"
                aria-checked={autoMode}
                onMouseDown={(e) => e.preventDefault()}
                onClick={toggleAutoMode}
                title="Judge calls that reach outside this session's directory against what the conversation asked for, instead of running everything"
              >
                <span className="agent-option-name">
                  {compact ? 'auto mode' : autoMode ? 'On' : 'Off'}
                </span>
                {compact && <kbd className="agent-shortcut">{keyCombo('alt', 'A')}</kbd>}
              </button>
            </div>
          </div>
        </div>
      )}
      {chiefToggleEligible && (
        <div className="picker-chief-bar">
          <div className="picker-agent-label">CHIEF OF STAFF</div>
          <div className="picker-chief-controls">
            <div className="agent-toggle">
              <button
                type="button"
                className={`agent-option ${chiefOfStaff ? 'active' : ''}`}
                data-testid="location-picker-chief-toggle"
                role="switch"
                aria-checked={chiefOfStaff}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => setChiefOfStaff((prev) => !prev)}
                title="Launch this session as the chief of staff — it runs the notebook and delegates work to other sessions"
              >
                <span className="agent-option-name">
                  {compact ? 'chief' : chiefOfStaff ? 'On' : 'Off'}
                </span>
              </button>
            </div>
          </div>
        </div>
      )}
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
      {!hasAvailableAgents && <div className="picker-agent-warning">{noAgentsMessage}</div>}
    </>
  );
}
