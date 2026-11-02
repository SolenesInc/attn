import "@typespec/json-schema";

using TypeSpec.JsonSchema;

// The phone's API. It only grows: scripts/check-gateway-compat refuses any
// change that is not a new message, a new optional field or a new enum member.
// Clients decode an enum member they do not know as unknown.
@jsonSchema
namespace Gateway;

const GATEWAY_API_VERSION = 5;

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
  // Since api 4: the session's main terminal, for terminal_attach; absent while it has none.
  terminal_id?: string;
}

// Every session of the pairing's profile: after welcome, then on each change.
model GatewaySessions {
  event: "sessions";
  sessions: GatewaySession[];
}

// Since api 3: the profile's asks, one ask per frame, because an ask's markdown
// alone may fill a frame. After welcome and on each change the home sends an
// ask event for each ask that is new or changed, then asks with every ask's id:
// the open asks oldest first, then the asks answered in the last day.
model GatewayAsks {
  event: "asks";
  ids: string[];
}

model GatewayAskEvent {
  event: "ask";
  ask: GatewayAsk;
}

enum GatewayAskQuestionKind {
  one,
  any,
  text,
  kind_unknown: "unknown",
}

model GatewayAskChoice {
  label: string;
  description?: string;
}

// recommended names a choice label; on a text question it is a suggested answer.
model GatewayAskQuestion {
  key: string;
  kind: GatewayAskQuestionKind;
  prompt: string;
  context?: string;
  choices: GatewayAskChoice[];
  recommended?: string;
}

// An ask reads as its parts in order: markdown, or one question. Images in the
// markdown load through ask_image with their src as written.
model GatewayAskPart {
  markdown?: string;
  question?: GatewayAskQuestion;
}

// picked holds choice labels; other is your own choice on a one or any
// question; text answers a text question.
model GatewayAskAnswer {
  key: string;
  picked: string[];
  other?: string;
  text?: string;
}

model GatewayAskReply {
  answers: GatewayAskAnswer[];
  note?: string;
  answered_at: utcDateTime;
}

// too_large_bytes is set, and parts left empty, when the ask does not fit one
// frame of max_frame_bytes; answer it on the computer.
model GatewayAsk {
  id: string;
  session_id: string;
  subject: string;
  parts: GatewayAskPart[];
  created_at: utcDateTime;
  seed_slug?: string;
  seed_title?: string;
  crew_member?: string;
  chief?: boolean;
  reply?: GatewayAskReply;
  too_large_bytes?: int32;
  max_frame_bytes?: int32;
}

// Answers an ask through the same path as the desktop. The home answers with
// ask_answer_result; error is absent on success.
model GatewayAskAnswerCommand {
  cmd: "ask_answer";
  request_id: string;
  ask_id: string;
  answers: GatewayAskAnswer[];
  note?: string;
}

model GatewayAskAnswerResult {
  event: "ask_answer_result";
  request_id: string;
  ask_id: string;
  error?: string;
}

// Reads one chunk of an ask's image, from offset. The home answers with
// ask_image_chunk, or ask_image_error naming the image and the reason. Every
// chunk but the last holds the same number of bytes, so after the first chunk
// a phone can ask for the rest at once; the chunks it has asked for and not
// received are its credit. Since api 5, src has each HTML character reference
// decoded once, as the ask's markdown means it, and the home reads exactly that
// image.
model GatewayAskImage {
  cmd: "ask_image";
  request_id: string;
  ask_id: string;
  src: string;
  offset: int32;
}

// size is the whole image's size in bytes; data_base64 holds the chunk.
model GatewayAskImageChunk {
  event: "ask_image_chunk";
  request_id: string;
  offset: int32;
  size: int32;
  mime_type: string;
  data_base64: string;
}

model GatewayAskImageError {
  event: "ask_image_error";
  request_id: string;
  message: string;
}

// Since api 4: a device opens a session's terminal with terminal_attach. The
// home answers terminal_screen, then the terminal's output as binary frames:
// 0x01 | id_len u8 | terminal_id | seq u32 BE | bytes. The home sends output
// only while the device's credit allows; the device grants credit with the
// attach and with terminal_credit as it renders. Credit counts terminal bytes:
// a screen's decoded ANSI and each frame's output bytes. A screen waits until
// the credit covers the largest gateway message (65,514 bytes).
model TerminalAttach {
  cmd: "terminal_attach";
  terminal_id: string;
  // The size the device asks for; it applies while the device owns the size.
  cols: int32;
  rows: int32;
  // Bytes of screen and output the home may send before the next terminal_credit.
  credit: int32;
}

model TerminalCredit {
  cmd: "terminal_credit";
  terminal_id: string;
  bytes: int32;
}

model TerminalDetach {
  cmd: "terminal_detach";
  terminal_id: string;
}

// Keys the user pressed; makes the device the size owner.
model TerminalInput {
  cmd: "terminal_input";
  terminal_id: string;
  data: string;
}

model TerminalResize {
  cmd: "terminal_resize";
  terminal_id: string;
  cols: int32;
  rows: int32;
}

// Makes the device the size owner: the PTY takes the device's size, and every
// other client shows it scaled until one of them claims it back.
model TerminalFocus {
  cmd: "terminal_focus";
  terminal_id: string;
}

// The home pastes text into the session's agent and presses Enter, when the
// agent is at a turn boundary.
model SubmitPrompt {
  cmd: "submit_prompt";
  request_id: string;
  session_id: string;
  text: string;
}

model SubmitResult {
  event: "submit_result";
  request_id: string;
  delivered: boolean;
  // Why the text was not delivered.
  message?: string;
}

// The visible screen as ANSI, base64-encoded. The device resets its terminal
// to cols x rows and writes it; output frames that follow continue from it.
model TerminalScreen {
  event: "terminal_screen";
  terminal_id: string;
  cols: int32;
  rows: int32;
  ansi: string;
  // The seq of the last output the screen shows; every frame after it has a larger seq.
  seq: int32;
}

model TerminalResized {
  event: "terminal_resized";
  terminal_id: string;
  cols: int32;
  rows: int32;
}

// The home could not attach, or the terminal refused input; the device shows message.
model TerminalError {
  event: "terminal_error";
  terminal_id: string;
  message: string;
}

// The terminal's program exited; no more output follows.
model TerminalEnded {
  event: "terminal_ended";
  terminal_id: string;
}
