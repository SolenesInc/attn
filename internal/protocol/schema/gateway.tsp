import "@typespec/json-schema";

using TypeSpec.JsonSchema;

// The phone's API. It only grows: scripts/check-gateway-compat refuses any
// change that is not a new message, a new optional field or a new enum member.
// Clients decode an enum member they do not know as unknown.
@jsonSchema
namespace Gateway;

const GATEWAY_API_VERSION = 2;

// The phone opens with hello; the home answers with welcome, then sessions.
model GatewayHello {
  cmd: "hello";
  api: int32;
  device_id: string;
}

// Since api 2: a device sends ping to learn that its home still answers
// through the relay; the home answers pong.
model GatewayPing {
  cmd: "ping";
}

model GatewayPong {
  event: "pong";
}

model GatewayWelcome {
  event: "welcome";
  // min(the device's api, GATEWAY_API_VERSION).
  api: int32;
  home_name: string;
  profile: GatewayProfile;
}

model GatewayProfile {
  id: string;
  name: string;
}

// The home ends the conversation after sending refused.
model GatewayRefused {
  event: "refused";
  message: string;
}

enum GatewaySessionState {
  working,
  waiting,
  needs_approval,
  idle,
  scheduled,
  launching,
  recoverable,
  state_unknown: "unknown",
}

model GatewaySession {
  id: string;
  title: string;
  agent: string;
  state: GatewaySessionState;
  // When the session started waiting on you; set while waiting or needs_approval.
  waiting_since?: utcDateTime;
  // The outpost the session runs on; absent on the home.
  host?: string;
  // True while the outpost the session runs on is unreachable.
  offline: boolean;
}

// Every session of the pairing's profile: after welcome, then on each change.
model GatewaySessions {
  event: "sessions";
  sessions: GatewaySession[];
}
