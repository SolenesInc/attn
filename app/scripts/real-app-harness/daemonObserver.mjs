import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import WebSocket from 'ws';
import { assertProductionRunAllowed, defaultWSURLForInstance, harnessClientHello, dataDirForInstance } from './harnessInstance.mjs';

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function sendClientHello(ws, trustedApp = false) {
  ws.send(
    JSON.stringify({
      ...harnessClientHello(trustedApp ? 'tauri-app' : 'harness-observer'),
      ...(trustedApp ? { browser_host_token: fs.readFileSync(path.join(dataDirForInstance(), 'browser-host-token'), 'utf8').trim() } : {}),
    }),
  );
}

export class DaemonObserver {
  constructor({
    wsUrl = defaultWSURLForInstance(),
    connectTimeoutMs = 45_000,
    trustedApp = false,
  } = {}) {
    assertProductionRunAllowed({ wsUrl });
    this.wsUrl = wsUrl;
    this.trustedApp = trustedApp;
    this.connectTimeoutMs = connectTimeoutMs;
    this.ws = null;
    this.sessionsById = new Map();
    this.endpointsById = new Map();
    this.settings = new Map();
    this.connected = false;
    this.initialStateReceived = false;
    this.profileId = null;
    this.profile = null;
    this.desktops = [];
    this.nextProfileRequest = 0;
    this.pendingProfileActions = new Map();
  }

  getSetting(key) {
    return this.settings.get(key) ?? '';
  }

  async connect() {
    if (this.connected && this.ws) {
      return;
    }

    const startedAt = Date.now();
    while (Date.now() - startedAt < this.connectTimeoutMs) {
      try {
        this.initialStateReceived = false;
        await this.#connectOnce();
        await this.waitFor(
          () => this.initialStateReceived,
          'daemon initial_state',
          Math.min(5_000, this.connectTimeoutMs),
        );
        return;
      } catch (error) {
        await delay(250);
        this.lastConnectError = error;
      }
    }

    throw new Error(
      `Timed out connecting to daemon websocket at ${this.wsUrl}: ${this.lastConnectError instanceof Error ? this.lastConnectError.message : this.lastConnectError}`
    );
  }

  async close() {
    const ws = this.ws;
    this.ws = null;
    this.connected = false;
    this.initialStateReceived = false;
    if (!ws) {
      return;
    }
    await new Promise((resolve) => {
      ws.once('close', resolve);
      ws.close();
      setTimeout(resolve, 500);
    });
  }

  async waitForMessage(predicate, description, timeoutMs = 10_000) {
    const ws = this.ws;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      throw new Error(`Cannot wait for ${description}: daemon websocket is not connected`);
    }
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        clearTimeout(timeout);
        ws.off('message', onMessage);
        ws.off('close', onClose);
      };
      const onMessage = (raw) => {
        let data;
        try {
          data = JSON.parse(raw.toString());
        } catch {
          return;
        }
        const value = predicate(data);
        if (!value) return;
        cleanup();
        resolve(value);
      };
      const onClose = () => {
        cleanup();
        reject(new Error(`Daemon websocket closed while waiting for ${description}`));
      };
      const timeout = setTimeout(() => {
        cleanup();
        reject(new Error(`Timed out after ${timeoutMs}ms waiting for ${description}`));
      }, timeoutMs);
      ws.on('message', onMessage);
      ws.once('close', onClose);
    });
  }

  async waitForDisconnect(description = 'daemon websocket to close', timeoutMs = 10_000) {
    const ws = this.ws;
    if (!ws || ws.readyState === WebSocket.CLOSED) return true;
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        ws.off('close', onClose);
        reject(new Error(`Timed out after ${timeoutMs}ms waiting for ${description}`));
      }, timeoutMs);
      const onClose = () => {
        clearTimeout(timeout);
        resolve(true);
      };
      ws.once('close', onClose);
    });
  }

  async requestResult(message, resultEvent, timeoutMs = 10_000) {
    const request_id = crypto.randomUUID();
    const result = this.waitForMessage(
      (data) => (data.event === resultEvent && data.request_id === request_id ? data : null),
      `${resultEvent} for ${message.cmd}`,
      timeoutMs,
    );
    this.send({ ...message, request_id });
    const data = await result;
    if (data.success === false) {
      throw new Error(`${message.cmd} failed: ${data.error || 'no error given'}`);
    }
    return data;
  }

  send(message) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error('Daemon websocket is not connected');
    }
    this.ws.send(JSON.stringify(message));
  }

  profileCommand(cmd, fields) {
    const requestId = `harness-${cmd}-${(this.nextProfileRequest += 1)}`;
    const settled = new Promise((resolve, reject) => {
      this.pendingProfileActions.set(requestId, { resolve, reject });
    });
    this.send({ cmd, request_id: requestId, ...fields });
    return settled;
  }

  desktop(desktopId) {
    return this.desktops.find((entry) => entry.id === desktopId) ?? null;
  }

  desktopOf(sessionId) {
    return this.desktops.find((desktop) => desktop.panes.some((pane) => pane.session_id === sessionId)) ?? null;
  }

  currentDesktopId() {
    return this.profile?.current_desktop_id ?? null;
  }

  // Unnamed, so the daemon removes it once it is empty and not current.
  async createDesktop() {
    const result = await this.profileCommand('desktop_create', { profile_id: this.profileId });
    const created = result.desktops?.[0];
    if (!created) throw new Error(`desktop_create returned no desktop: ${JSON.stringify(result)}`);
    return this.waitFor(() => this.desktop(created.id), `arrangement with desktop ${created.id}`);
  }

  setCurrentDesktop(desktopId) {
    return this.profileCommand('desktop_set_current', { profile_id: this.profileId, desktop_id: desktopId });
  }

  describeArrangement() {
    return JSON.stringify(
      {
        profileId: this.profileId,
        currentDesktopId: this.currentDesktopId(),
        desktops: this.desktops.map((desktop) => ({
          id: desktop.id,
          name: desktop.name,
          slot: desktop.shortcut_slot ?? null,
          activePaneId: desktop.active_pane_id,
          panes: desktop.panes.map((pane) => `${pane.pane_id}:${pane.session_id}`),
        })),
      },
      null,
      2,
    );
  }

  addEndpoint(name, sshTarget) {
    this.send({ cmd: 'add_endpoint', name, ssh_target: sshTarget });
  }

  updateEndpoint(endpointId, updates) {
    this.send({ cmd: 'update_endpoint', endpoint_id: endpointId, ...updates });
  }

  removeEndpoint(endpointId) {
    this.send({ cmd: 'remove_endpoint', endpoint_id: endpointId });
  }

  unregisterSession(sessionId) {
    this.send({ cmd: 'unregister', id: sessionId });
  }

  async unregisterMatchingSessions(predicate, timeoutMs = 15_000) {
    const matches = [...this.sessionsById.values()].filter(predicate);
    if (matches.length === 0) {
      return [];
    }

    for (const session of matches) {
      this.unregisterSession(session.id);
    }

    const targetIds = new Set(matches.map((session) => session.id));
    await this.waitFor(() => {
      const remaining = [...this.sessionsById.values()].filter((session) => targetIds.has(session.id));
      return remaining.length === 0 ? true : null;
    }, `daemon unregister for sessions ${[...targetIds].join(', ')}`, timeoutMs);

    return matches;
  }


  getSession(sessionId) {
    return this.sessionsById.get(sessionId) || null;
  }

  // The PTY runtime a session's pane places; a session no pane places runs under its own id.
  terminalOf(sessionId) {
    for (const desktop of this.desktops) {
      const pane = desktop.panes.find((entry) => entry.session_id === sessionId && entry.runtime_id);
      if (pane) return pane.runtime_id;
    }
    return sessionId;
  }

  getEndpoint(endpointId) {
    return this.endpointsById.get(endpointId) || null;
  }

  findEndpointByName(name) {
    for (const endpoint of this.endpointsById.values()) {
      if (endpoint.name === name) {
        return endpoint;
      }
    }
    return null;
  }

  async waitForEndpoint({ id, name, sshTarget, status, timeoutMs = 20_000 } = {}) {
    return this.waitFor(
      () => {
        for (const endpoint of this.endpointsById.values()) {
          if (id && endpoint.id !== id) continue;
          if (name && endpoint.name !== name) continue;
          if (sshTarget && endpoint.ssh_target !== sshTarget) continue;
          if (status && endpoint.status !== status) continue;
          return endpoint;
        }
        return null;
      },
      `endpoint id=${id || '*'} name=${name || '*'} sshTarget=${sshTarget || '*'} status=${status || '*'}`,
      timeoutMs
    );
  }

  async waitForSession({ id, label, directory, timeoutMs = 20_000 } = {}) {
    return this.waitFor(
      () => {
        for (const session of this.sessionsById.values()) {
          if (id && session.id !== id) {
            continue;
          }
          if (label && session.label !== label) {
            continue;
          }
          if (directory && session.directory !== directory) {
            continue;
          }
          return session;
        }
        return null;
      },
      `session id=${id || '*'} label=${label || '*'} directory=${directory || '*'}`,
      timeoutMs
    );
  }

  async waitForDesktopOf(sessionId, predicate, description, timeoutMs = 20_000) {
    return this.waitFor(() => {
      const desktop = this.desktopOf(sessionId);
      return desktop && predicate(desktop) ? desktop : null;
    }, description || `desktop holding session ${sessionId}`, timeoutMs);
  }

  async waitForUtilityPane(sessionId, timeoutMs = 20_000, excludePaneIds = new Set()) {
    const isNewSessionPane = (pane) => !excludePaneIds.has(pane.pane_id) && Boolean(pane.session_id);
    const desktop = await this.waitForDesktopOf(
      sessionId,
      (entry) => entry.panes.some(isNewSessionPane),
      `utility pane beside session ${sessionId}`,
      timeoutMs
    );
    return desktop.panes.find(isNewSessionPane) || null;
  }

  async attachOnce(runtimeId, timeoutMs = 5_000) {
    return attachOnce(this.wsUrl, runtimeId, timeoutMs);
  }

  async waitForAttachable(runtimeId, timeoutMs = 12_000) {
    const startedAt = Date.now();
    let lastError = null;
    while (Date.now() - startedAt < timeoutMs) {
      try {
        return await this.attachOnce(runtimeId, Math.min(5_000, timeoutMs));
      } catch (error) {
        lastError = error;
      }
      await delay(250);
    }
    throw new Error(
      `Timed out waiting for ${runtimeId} to become attachable: ${lastError instanceof Error ? lastError.message : String(lastError || 'unknown error')}`
    );
  }

  async waitFor(predicate, description, timeoutMs = 10_000) {
    const startedAt = Date.now();
    let lastSnapshot = this.describeState();
    while (Date.now() - startedAt < timeoutMs) {
      const value = predicate();
      if (value) {
        return value;
      }
      await delay(100);
      lastSnapshot = this.describeState();
    }
    throw new Error(`Timed out waiting for ${description}. Current daemon state:\n${lastSnapshot}`);
  }

  describeState() {
    const sessions = [...this.sessionsById.values()].map((session) => ({
      id: session.id,
      label: session.label,
      directory: session.directory,
      state: session.state,
      agent: session.agent,
    }));
    const endpoints = [...this.endpointsById.values()].map((endpoint) => ({
      id: endpoint.id,
      name: endpoint.name,
      sshTarget: endpoint.ssh_target,
      status: endpoint.status,
      sessionCount: endpoint.session_count,
    }));
    return JSON.stringify({ sessions, endpoints, arrangement: JSON.parse(this.describeArrangement()) }, null, 2);
  }

  #connectOnce() {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(this.wsUrl, this.trustedApp ? { headers: { Origin: 'tauri://localhost' } } : undefined);
      let settled = false;

      const fail = (error) => {
        if (settled) {
          return;
        }
        settled = true;
        try {
          ws.close();
        } catch {
          // Ignore close failures during connect.
        }
        reject(error);
      };

      const succeed = () => {
        if (settled) {
          return;
        }
        settled = true;
        this.ws = ws;
        this.connected = true;
        resolve();
      };

      const timeout = setTimeout(() => {
        fail(new Error(`connect timeout after ${this.connectTimeoutMs}ms`));
      }, Math.min(5_000, this.connectTimeoutMs));

      ws.once('open', () => {
        clearTimeout(timeout);
        sendClientHello(ws, this.trustedApp);
        succeed();
      });

      ws.once('error', (error) => {
        clearTimeout(timeout);
        fail(error);
      });

      ws.on('close', () => {
        if (this.ws === ws) {
          this.connected = false;
          this.ws = null;
        }
      });

      ws.on('message', (raw) => {
        try {
          const data = JSON.parse(raw.toString());
          this.#handleMessage(data);
        } catch (error) {
          console.warn('[RealAppHarness] Failed to parse daemon message:', error);
        }
      });
    });
  }

  #applySettings(settings) {
    if (!settings || typeof settings !== 'object') {
      return;
    }
    this.settings = new Map(Object.entries(settings).map(([key, value]) => [key, String(value ?? '')]));
  }

  #handleMessage(data) {
    switch (data.event) {
      case 'initial_state':
        this.initialStateReceived = true;
        this.sessionsById.clear();
        for (const session of data.sessions || []) {
          this.sessionsById.set(session.id, session);
        }
        this.endpointsById.clear();
        for (const endpoint of data.endpoints || []) {
          this.endpointsById.set(endpoint.id, endpoint);
        }
        this.#applySettings(data.settings);
        this.profileId = data.selected_profile_id ?? null;
        this.profile = (data.profiles || []).find((profile) => profile.id === this.profileId) ?? null;
        this.desktops = data.desktops || [];
        break;
      case 'profile_arrangement_changed':
        if (data.profile?.id === this.profileId) {
          this.profile = data.profile;
          this.desktops = data.desktops || [];
        }
        break;
      case 'profile_action_result': {
        const waiter = this.pendingProfileActions.get(data.request_id);
        if (waiter) {
          this.pendingProfileActions.delete(data.request_id);
          if (data.success) waiter.resolve(data);
          else waiter.reject(new Error(`${data.action} failed: ${data.error_code ?? ''} ${data.error ?? ''}`));
        }
        break;
      }
      case 'settings_updated':
        this.#applySettings(data.settings);
        break;
      case 'sessions_updated':
        this.sessionsById.clear();
        for (const session of data.sessions || []) {
          this.sessionsById.set(session.id, session);
        }
        break;
      case 'endpoint_status_changed':
        if (data.endpoint?.id) {
          this.endpointsById.set(data.endpoint.id, data.endpoint);
        }
        break;
      case 'endpoints_updated':
        this.endpointsById.clear();
        for (const endpoint of data.endpoints || []) {
          this.endpointsById.set(endpoint.id, endpoint);
        }
        break;
      case 'session_registered':
      case 'session_state_changed':
        if (data.session?.id) {
          this.sessionsById.set(data.session.id, data.session);
        }
        break;
      case 'session_unregistered':
        if (data.session?.id) {
          this.sessionsById.delete(data.session.id);
        }
        break;
      default:
        break;
    }
  }

}

export async function attachOnce(wsUrl, runtimeId, timeoutMs = 5_000) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    const timeout = setTimeout(() => {
      try {
        ws.close();
      } catch {
        // Ignore close errors during timeout handling.
      }
      reject(new Error(`attach_session timeout for ${runtimeId}`));
    }, timeoutMs);

    ws.once('open', () => {
      sendClientHello(ws);
      ws.send(JSON.stringify({ cmd: 'attach_session', id: runtimeId }));
    });

    ws.on('message', (raw) => {
      const data = JSON.parse(raw.toString());
      if (data.event !== 'attach_result' || data.id !== runtimeId) {
        return;
      }
      clearTimeout(timeout);
      ws.close();
      if (!data.success) {
        reject(new Error(data.error || `attach_session failed for ${runtimeId}`));
        return;
      }
      resolve(data);
    });

    ws.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });
}
