import "@typespec/json-schema";

using TypeSpec.JsonSchema;

// The phone's API. It only grows: scripts/check-home-gateway-compat refuses any
// change that is not a new message, a new optional field or a new enum member.
// Clients decode an enum member they do not know as unknown.
@jsonSchema
namespace HomeGateway;

const HOME_GATEWAY_API_VERSION = 7;

// The phone opens with hello; the home answers with welcome, then sessions.
model HomeGatewayHello {
  cmd: "hello";
  api: int32;
  device_id: string;
}

// Since api 2: a device sends ping to learn that its home still answers
// through the relay; the home answers pong.
model HomeGatewayPing {
  cmd: "ping";
}

model HomeGatewayPong {
  event: "pong";
}

model HomeGatewayWelcome {
  event: "welcome";
  // min(the device's api, HOME_GATEWAY_API_VERSION).
  api: int32;
  home_name: string;
  profile: HomeGatewayProfile;
}

model HomeGatewayProfile {
  id: string;
  name: string;
}

// The home ends the conversation after sending refused.
model HomeGatewayRefused {
  event: "refused";
  message: string;
}

enum HomeGatewaySessionState {
  working,
  waiting,
  needs_approval,
  idle,
  scheduled,
  launching,
  recoverable,
  state_unknown: "unknown",
}

model HomeGatewaySession {
  id: string;
  title: string;
  agent: string;
  state: HomeGatewaySessionState;
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
model HomeGatewaySessions {
  event: "sessions";
  sessions: HomeGatewaySession[];
}

// Since api 3: the profile's asks, one ask per frame, because an ask's markdown
// alone may fill a frame. After welcome and on each change the home sends an
// ask event for each ask that is new or changed, then asks with every ask's id:
// the open asks oldest first, then the asks answered in the last day.
model HomeGatewayAsks {
  event: "asks";
  ids: string[];
}

model HomeGatewayAskEvent {
  event: "ask";
  ask: HomeGatewayAsk;
}

enum HomeGatewayAskQuestionKind {
  one,
  any,
  text,
  kind_unknown: "unknown",
}

model HomeGatewayAskChoice {
  label: string;
  description?: string;
}

// recommended names a choice label; on a text question it is a suggested answer.
model HomeGatewayAskQuestion {
  key: string;
  kind: HomeGatewayAskQuestionKind;
  prompt: string;
  context?: string;
  choices: HomeGatewayAskChoice[];
  recommended?: string;
}

// An ask reads as its parts in order: markdown, or one question. Images in the
// markdown load through ask_image with their src as written.
model HomeGatewayAskPart {
  markdown?: string;
  question?: HomeGatewayAskQuestion;
}

// picked holds choice labels; other is your own choice on a one or any
// question; text answers a text question.
model HomeGatewayAskAnswer {
  key: string;
  picked: string[];
  other?: string;
  text?: string;
}

model HomeGatewayAskReply {
  answers: HomeGatewayAskAnswer[];
  note?: string;
  answered_at: utcDateTime;
}

// too_large_bytes is set, and parts left empty, when the ask does not fit one
// frame of max_frame_bytes; answer it on the computer.
model HomeGatewayAsk {
  id: string;
  session_id: string;
  subject: string;
  parts: HomeGatewayAskPart[];
  created_at: utcDateTime;
  seed_slug?: string;
  seed_title?: string;
  crew_member?: string;
  chief?: boolean;
  reply?: HomeGatewayAskReply;
  too_large_bytes?: int32;
  max_frame_bytes?: int32;
}

// Answers an ask through the same path as the desktop. The home answers with
// ask_answer_result; error is absent on success.
model HomeGatewayAskAnswerCommand {
  cmd: "ask_answer";
  request_id: string;
  ask_id: string;
  answers: HomeGatewayAskAnswer[];
  note?: string;
}

model HomeGatewayAskAnswerResult {
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
model HomeGatewayAskImage {
  cmd: "ask_image";
  request_id: string;
  ask_id: string;
  src: string;
  offset: int32;
}

// size is the whole image's size in bytes; data_base64 holds the chunk.
model HomeGatewayAskImageChunk {
  event: "ask_image_chunk";
  request_id: string;
  offset: int32;
  size: int32;
  mime_type: string;
  data_base64: string;
}

model HomeGatewayAskImageError {
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
// the credit covers the largest Home Gateway message (65,514 bytes).
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

// Since api 6: the profile's open seeds, planted, growing and parked, and the
// seeds closed in the last day, without their bodies. After welcome and on
// each change the home sends garden_seeds frames holding each seed that is new
// or changed, as many as fit a frame, then garden with every listed seed's id,
// newest first.
model HomeGatewayGardenSeeds {
  event: "garden_seeds";
  seeds: HomeGatewaySeed[];
}

model HomeGatewayGarden {
  event: "garden";
  ids: string[];
}

enum HomeGatewaySeedStatus {
  planted,
  growing,
  parked,
  harvested,
  withered,
  status_unknown: "unknown",
}

model HomeGatewayCheckTally {
  confirmed: int32;
  total: int32;
}

// How far a plot has drained: done counts harvested children, withered ones
// count apart.
model HomeGatewayPlotProgress {
  total: int32;
  done: int32;
  withered: int32;
  growing: int32;
  parked: int32;
  ready: int32;
  blocked: int32;
}

model HomeGatewaySeed {
  id: string;
  slug: string;
  title: string;
  status: HomeGatewaySeedStatus;
  // Who the seed's claim names; claimed says whether the claim holds now.
  tender?: string;
  claimed: boolean;
  // Nothing open blocks it, nobody holds it, and it is not a plot.
  ready: boolean;
  // The plot this seed is part of.
  plot_id?: string;
  // The open seeds this one waits for.
  waits_for: string[];
  // Present when the seed is a plot.
  progress?: HomeGatewayPlotProgress;
  // The seed's own and inherited checks; on a plot, every check below it.
  checks?: HomeGatewayCheckTally;
  state_changed_at: utcDateTime;
  // Why it was harvested or withered.
  reason?: string;
}

// Reads one seed of the pairing's profile. The home answers seed_detail, or
// seed_error naming the reason.
model HomeGatewaySeedGet {
  cmd: "seed_get";
  request_id: string;
  seed_id: string;
}

enum HomeGatewayCheckState {
  open,
  reported,
  confirmed,
  rejected,
  check_unknown: "unknown",
}

model HomeGatewaySeedCheck {
  id: string;
  text: string;
  state: HomeGatewayCheckState;
  // The plot that defines an inherited check.
  from?: string;
}

model HomeGatewaySeedLogEntry {
  id: string;
  // note, handoff, attach, detach, check, or a kind added later.
  kind: string;
  body: string;
  author: string;
  created_at: utcDateTime;
}

// body is the seed's markdown. children lists every seed part of it, closed
// ones included, newest first. log holds the newest entries, newest first, as
// many as fit one frame; log_total counts them all. body_too_large_bytes is
// set, and body left empty, when the body alone does not fit a frame of
// max_frame_bytes; read it on the computer.
model HomeGatewaySeedDetail {
  event: "seed_detail";
  request_id: string;
  seed: HomeGatewaySeed;
  body: string;
  checks: HomeGatewaySeedCheck[];
  children: HomeGatewaySeed[];
  log: HomeGatewaySeedLogEntry[];
  log_total: int32;
  body_too_large_bytes?: int32;
  max_frame_bytes?: int32;
}

// Since api 7: something in a seed's detail changed, such as its body, its log,
// its checks or its links, including changes that leave its garden_seeds copy
// unchanged. A phone showing that seed reads it again with seed_get.
model HomeGatewaySeedChanged {
  event: "seed_changed";
  seed_id: string;
}

model HomeGatewaySeedError {
  event: "seed_error";
  request_id: string;
  message: string;
}

// Adds a note to a seed's log as the user, through the desktop's note path.
// The home answers seed_result.
model HomeGatewaySeedNoteCommand {
  cmd: "seed_note";
  request_id: string;
  seed_id: string;
  body: string;
}

enum HomeGatewaySeedVerb {
  park,
  harvest,
  wither,
  replant,
  verb_unknown: "unknown",
}

// Moves a seed as the user, as the desktop's board does: harvest and wither
// keep text as the seed's reason, park logs it with the move, and replant logs
// it as a note first. Harvest needs text. The home answers seed_result.
model HomeGatewaySeedMove {
  cmd: "seed_move";
  request_id: string;
  seed_id: string;
  verb: HomeGatewaySeedVerb;
  text: string;
}

// error is absent on success; the seed's change reaches the device as garden
// frames.
model HomeGatewaySeedResult {
  event: "seed_result";
  request_id: string;
  seed_id: string;
  error?: string;
}
