// To parse this data:
//
//   import { Convert, GatewayAsk, GatewayAskAnswer, GatewayAskAnswerCommand, GatewayAskAnswerResult, GatewayAskChoice, GatewayAskEvent, GatewayAskImage, GatewayAskImageChunk, GatewayAskImageError, GatewayAskPart, GatewayAskQuestion, GatewayAskQuestionKind, GatewayAskReply, GatewayAsks, GatewayCheckState, GatewayCheckTally, GatewayGarden, GatewayGardenSeeds, GatewayHello, GatewayPing, GatewayPlotProgress, GatewayPong, GatewayProfile, GatewayRefused, GatewaySeed, GatewaySeedChanged, GatewaySeedCheck, GatewaySeedDetail, GatewaySeedError, GatewaySeedGet, GatewaySeedLogEntry, GatewaySeedMove, GatewaySeedNoteCommand, GatewaySeedResult, GatewaySeedStatus, GatewaySeedVerb, GatewaySession, GatewaySessionState, GatewaySessions, GatewayWelcome, SubmitPrompt, SubmitResult, TerminalAttach, TerminalCredit, TerminalDetach, TerminalEnded, TerminalError, TerminalFocus, TerminalInput, TerminalResize, TerminalResized, TerminalScreen } from "./gatewayGenerated";
//
//   const gatewayAsk = Convert.toGatewayAsk(json);
//   const gatewayAskAnswer = Convert.toGatewayAskAnswer(json);
//   const gatewayAskAnswerCommand = Convert.toGatewayAskAnswerCommand(json);
//   const gatewayAskAnswerResult = Convert.toGatewayAskAnswerResult(json);
//   const gatewayAskChoice = Convert.toGatewayAskChoice(json);
//   const gatewayAskEvent = Convert.toGatewayAskEvent(json);
//   const gatewayAskImage = Convert.toGatewayAskImage(json);
//   const gatewayAskImageChunk = Convert.toGatewayAskImageChunk(json);
//   const gatewayAskImageError = Convert.toGatewayAskImageError(json);
//   const gatewayAskPart = Convert.toGatewayAskPart(json);
//   const gatewayAskQuestion = Convert.toGatewayAskQuestion(json);
//   const gatewayAskQuestionKind = Convert.toGatewayAskQuestionKind(json);
//   const gatewayAskReply = Convert.toGatewayAskReply(json);
//   const gatewayAsks = Convert.toGatewayAsks(json);
//   const gatewayCheckState = Convert.toGatewayCheckState(json);
//   const gatewayCheckTally = Convert.toGatewayCheckTally(json);
//   const gatewayGarden = Convert.toGatewayGarden(json);
//   const gatewayGardenSeeds = Convert.toGatewayGardenSeeds(json);
//   const gatewayHello = Convert.toGatewayHello(json);
//   const gatewayPing = Convert.toGatewayPing(json);
//   const gatewayPlotProgress = Convert.toGatewayPlotProgress(json);
//   const gatewayPong = Convert.toGatewayPong(json);
//   const gatewayProfile = Convert.toGatewayProfile(json);
//   const gatewayRefused = Convert.toGatewayRefused(json);
//   const gatewaySeed = Convert.toGatewaySeed(json);
//   const gatewaySeedChanged = Convert.toGatewaySeedChanged(json);
//   const gatewaySeedCheck = Convert.toGatewaySeedCheck(json);
//   const gatewaySeedDetail = Convert.toGatewaySeedDetail(json);
//   const gatewaySeedError = Convert.toGatewaySeedError(json);
//   const gatewaySeedGet = Convert.toGatewaySeedGet(json);
//   const gatewaySeedLogEntry = Convert.toGatewaySeedLogEntry(json);
//   const gatewaySeedMove = Convert.toGatewaySeedMove(json);
//   const gatewaySeedNoteCommand = Convert.toGatewaySeedNoteCommand(json);
//   const gatewaySeedResult = Convert.toGatewaySeedResult(json);
//   const gatewaySeedStatus = Convert.toGatewaySeedStatus(json);
//   const gatewaySeedVerb = Convert.toGatewaySeedVerb(json);
//   const gatewaySession = Convert.toGatewaySession(json);
//   const gatewaySessionState = Convert.toGatewaySessionState(json);
//   const gatewaySessions = Convert.toGatewaySessions(json);
//   const gatewayWelcome = Convert.toGatewayWelcome(json);
//   const submitPrompt = Convert.toSubmitPrompt(json);
//   const submitResult = Convert.toSubmitResult(json);
//   const terminalAttach = Convert.toTerminalAttach(json);
//   const terminalCredit = Convert.toTerminalCredit(json);
//   const terminalDetach = Convert.toTerminalDetach(json);
//   const terminalEnded = Convert.toTerminalEnded(json);
//   const terminalError = Convert.toTerminalError(json);
//   const terminalFocus = Convert.toTerminalFocus(json);
//   const terminalInput = Convert.toTerminalInput(json);
//   const terminalResize = Convert.toTerminalResize(json);
//   const terminalResized = Convert.toTerminalResized(json);
//   const terminalScreen = Convert.toTerminalScreen(json);
//
// These functions will throw an error if the JSON doesn't
// match the expected interface, even if the JSON is valid.

export interface GatewayAsk {
    chief?:           boolean;
    created_at:       Date;
    crew_member?:     string;
    id:               string;
    max_frame_bytes?: number;
    parts:            PartElement[];
    reply?:           Reply;
    seed_slug?:       string;
    seed_title?:      string;
    session_id:       string;
    subject:          string;
    too_large_bytes?: number;
    [property: string]: any;
}

export interface PartElement {
    markdown?: string;
    question?: Question;
    [property: string]: any;
}

export interface Question {
    choices:      ChoiceElement[];
    context?:     string;
    key:          string;
    kind:         GatewayAskQuestionKind;
    prompt:       string;
    recommended?: string;
    [property: string]: any;
}

export interface ChoiceElement {
    description?: string;
    label:        string;
    [property: string]: any;
}

export enum GatewayAskQuestionKind {
    Any = "any",
    One = "one",
    Text = "text",
    Unknown = "unknown",
}

export interface Reply {
    answered_at: Date;
    answers:     AnswerElement[];
    note?:       string;
    [property: string]: any;
}

export interface AnswerElement {
    key:    string;
    other?: string;
    picked: string[];
    text?:  string;
    [property: string]: any;
}

export interface GatewayAskAnswer {
    key:    string;
    other?: string;
    picked: string[];
    text?:  string;
    [property: string]: any;
}

export interface GatewayAskAnswerCommand {
    answers:    AnswerElement[];
    ask_id:     string;
    cmd:        GatewayAskAnswerCommandCmd;
    note?:      string;
    request_id: string;
    [property: string]: any;
}

export enum GatewayAskAnswerCommandCmd {
    AskAnswer = "ask_answer",
}

export interface GatewayAskAnswerResult {
    ask_id:     string;
    error?:     string;
    event:      GatewayAskAnswerResultEvent;
    request_id: string;
    [property: string]: any;
}

export enum GatewayAskAnswerResultEvent {
    AskAnswerResult = "ask_answer_result",
}

export interface GatewayAskChoice {
    description?: string;
    label:        string;
    [property: string]: any;
}

export interface GatewayAskEvent {
    ask:   Ask;
    event: GatewayAskEventEvent;
    [property: string]: any;
}

export interface Ask {
    chief?:           boolean;
    created_at:       Date;
    crew_member?:     string;
    id:               string;
    max_frame_bytes?: number;
    parts:            PartElement[];
    reply?:           Reply;
    seed_slug?:       string;
    seed_title?:      string;
    session_id:       string;
    subject:          string;
    too_large_bytes?: number;
    [property: string]: any;
}

export enum GatewayAskEventEvent {
    Ask = "ask",
}

export interface GatewayAskImage {
    ask_id:     string;
    cmd:        GatewayAskImageCmd;
    offset:     number;
    request_id: string;
    src:        string;
    [property: string]: any;
}

export enum GatewayAskImageCmd {
    AskImage = "ask_image",
}

export interface GatewayAskImageChunk {
    data_base64: string;
    event:       GatewayAskImageChunkEvent;
    mime_type:   string;
    offset:      number;
    request_id:  string;
    size:        number;
    [property: string]: any;
}

export enum GatewayAskImageChunkEvent {
    AskImageChunk = "ask_image_chunk",
}

export interface GatewayAskImageError {
    event:      GatewayAskImageErrorEvent;
    message:    string;
    request_id: string;
    [property: string]: any;
}

export enum GatewayAskImageErrorEvent {
    AskImageError = "ask_image_error",
}

export interface GatewayAskPart {
    markdown?: string;
    question?: Question;
    [property: string]: any;
}

export interface GatewayAskQuestion {
    choices:      ChoiceElement[];
    context?:     string;
    key:          string;
    kind:         GatewayAskQuestionKind;
    prompt:       string;
    recommended?: string;
    [property: string]: any;
}

export interface GatewayAskReply {
    answered_at: Date;
    answers:     AnswerElement[];
    note?:       string;
    [property: string]: any;
}

export interface GatewayAsks {
    event: GatewayAsksEvent;
    ids:   string[];
    [property: string]: any;
}

export enum GatewayAsksEvent {
    Asks = "asks",
}

export interface GatewayCheckTally {
    confirmed: number;
    total:     number;
    [property: string]: any;
}

export interface GatewayGarden {
    event: GatewayGardenEvent;
    ids:   string[];
    [property: string]: any;
}

export enum GatewayGardenEvent {
    Garden = "garden",
}

export interface GatewayGardenSeeds {
    event: GatewayGardenSeedsEvent;
    seeds: Seed[];
    [property: string]: any;
}

export enum GatewayGardenSeedsEvent {
    GardenSeeds = "garden_seeds",
}

export interface Seed {
    checks?:          Checks;
    claimed:          boolean;
    id:               string;
    plot_id?:         string;
    progress?:        Progress;
    ready:            boolean;
    reason?:          string;
    slug:             string;
    state_changed_at: Date;
    status:           GatewaySeedStatus;
    tender?:          string;
    title:            string;
    waits_for:        string[];
    [property: string]: any;
}

export interface Checks {
    confirmed: number;
    total:     number;
    [property: string]: any;
}

export interface Progress {
    blocked:  number;
    done:     number;
    growing:  number;
    parked:   number;
    ready:    number;
    total:    number;
    withered: number;
    [property: string]: any;
}

export enum GatewaySeedStatus {
    Growing = "growing",
    Harvested = "harvested",
    Parked = "parked",
    Planted = "planted",
    Unknown = "unknown",
    Withered = "withered",
}

export interface GatewayHello {
    api:       number;
    cmd:       GatewayHelloCmd;
    device_id: string;
    [property: string]: any;
}

export enum GatewayHelloCmd {
    Hello = "hello",
}

export interface GatewayPing {
    cmd: GatewayPingCmd;
    [property: string]: any;
}

export enum GatewayPingCmd {
    Ping = "ping",
}

export interface GatewayPlotProgress {
    blocked:  number;
    done:     number;
    growing:  number;
    parked:   number;
    ready:    number;
    total:    number;
    withered: number;
    [property: string]: any;
}

export interface GatewayPong {
    event: GatewayPongEvent;
    [property: string]: any;
}

export enum GatewayPongEvent {
    Pong = "pong",
}

export interface GatewayProfile {
    id:   string;
    name: string;
    [property: string]: any;
}

export interface GatewayRefused {
    event:   GatewayRefusedEvent;
    message: string;
    [property: string]: any;
}

export enum GatewayRefusedEvent {
    Refused = "refused",
}

export interface GatewaySeed {
    checks?:          Checks;
    claimed:          boolean;
    id:               string;
    plot_id?:         string;
    progress?:        Progress;
    ready:            boolean;
    reason?:          string;
    slug:             string;
    state_changed_at: Date;
    status:           GatewaySeedStatus;
    tender?:          string;
    title:            string;
    waits_for:        string[];
    [property: string]: any;
}

export interface GatewaySeedChanged {
    event:   GatewaySeedChangedEvent;
    seed_id: string;
    [property: string]: any;
}

export enum GatewaySeedChangedEvent {
    SeedChanged = "seed_changed",
}

export interface GatewaySeedCheck {
    from?: string;
    id:    string;
    state: GatewayCheckState;
    text:  string;
    [property: string]: any;
}

export enum GatewayCheckState {
    Confirmed = "confirmed",
    Open = "open",
    Rejected = "rejected",
    Reported = "reported",
    Unknown = "unknown",
}

export interface GatewaySeedDetail {
    body:                  string;
    body_too_large_bytes?: number;
    checks:                CheckElement[];
    children:              Seed[];
    event:                 GatewaySeedDetailEvent;
    log:                   LogElement[];
    log_total:             number;
    max_frame_bytes?:      number;
    request_id:            string;
    seed:                  Seed;
    [property: string]: any;
}

export interface CheckElement {
    from?: string;
    id:    string;
    state: GatewayCheckState;
    text:  string;
    [property: string]: any;
}

export enum GatewaySeedDetailEvent {
    SeedDetail = "seed_detail",
}

export interface LogElement {
    author:     string;
    body:       string;
    created_at: Date;
    id:         string;
    kind:       string;
    [property: string]: any;
}

export interface GatewaySeedError {
    event:      GatewaySeedErrorEvent;
    message:    string;
    request_id: string;
    [property: string]: any;
}

export enum GatewaySeedErrorEvent {
    SeedError = "seed_error",
}

export interface GatewaySeedGet {
    cmd:        GatewaySeedGetCmd;
    request_id: string;
    seed_id:    string;
    [property: string]: any;
}

export enum GatewaySeedGetCmd {
    SeedGet = "seed_get",
}

export interface GatewaySeedLogEntry {
    author:     string;
    body:       string;
    created_at: Date;
    id:         string;
    kind:       string;
    [property: string]: any;
}

export interface GatewaySeedMove {
    cmd:        GatewaySeedMoveCmd;
    request_id: string;
    seed_id:    string;
    text:       string;
    verb:       GatewaySeedVerb;
    [property: string]: any;
}

export enum GatewaySeedMoveCmd {
    SeedMove = "seed_move",
}

export enum GatewaySeedVerb {
    Harvest = "harvest",
    Park = "park",
    Replant = "replant",
    Unknown = "unknown",
    Wither = "wither",
}

export interface GatewaySeedNoteCommand {
    body:       string;
    cmd:        GatewaySeedNoteCommandCmd;
    request_id: string;
    seed_id:    string;
    [property: string]: any;
}

export enum GatewaySeedNoteCommandCmd {
    SeedNote = "seed_note",
}

export interface GatewaySeedResult {
    error?:     string;
    event:      GatewaySeedResultEvent;
    request_id: string;
    seed_id:    string;
    [property: string]: any;
}

export enum GatewaySeedResultEvent {
    SeedResult = "seed_result",
}

export interface GatewaySession {
    agent:          string;
    host?:          string;
    id:             string;
    offline:        boolean;
    state:          GatewaySessionState;
    terminal_id?:   string;
    title:          string;
    waiting_since?: Date;
    [property: string]: any;
}

export enum GatewaySessionState {
    Idle = "idle",
    Launching = "launching",
    NeedsApproval = "needs_approval",
    Recoverable = "recoverable",
    Scheduled = "scheduled",
    Unknown = "unknown",
    Waiting = "waiting",
    Working = "working",
}

export interface GatewaySessions {
    event:    GatewaySessionsEvent;
    sessions: SessionElement[];
    [property: string]: any;
}

export enum GatewaySessionsEvent {
    Sessions = "sessions",
}

export interface SessionElement {
    agent:          string;
    host?:          string;
    id:             string;
    offline:        boolean;
    state:          GatewaySessionState;
    terminal_id?:   string;
    title:          string;
    waiting_since?: Date;
    [property: string]: any;
}

export interface GatewayWelcome {
    api:       number;
    event:     GatewayWelcomeEvent;
    home_name: string;
    profile:   Profile;
    [property: string]: any;
}

export enum GatewayWelcomeEvent {
    Welcome = "welcome",
}

export interface Profile {
    id:   string;
    name: string;
    [property: string]: any;
}

export interface SubmitPrompt {
    cmd:        SubmitPromptCmd;
    request_id: string;
    session_id: string;
    text:       string;
    [property: string]: any;
}

export enum SubmitPromptCmd {
    SubmitPrompt = "submit_prompt",
}

export interface SubmitResult {
    delivered:  boolean;
    event:      SubmitResultEvent;
    message?:   string;
    request_id: string;
    [property: string]: any;
}

export enum SubmitResultEvent {
    SubmitResult = "submit_result",
}

export interface TerminalAttach {
    cmd:         TerminalAttachCmd;
    cols:        number;
    credit:      number;
    rows:        number;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalAttachCmd {
    TerminalAttach = "terminal_attach",
}

export interface TerminalCredit {
    bytes:       number;
    cmd:         TerminalCreditCmd;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalCreditCmd {
    TerminalCredit = "terminal_credit",
}

export interface TerminalDetach {
    cmd:         TerminalDetachCmd;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalDetachCmd {
    TerminalDetach = "terminal_detach",
}

export interface TerminalEnded {
    event:       TerminalEndedEvent;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalEndedEvent {
    TerminalEnded = "terminal_ended",
}

export interface TerminalError {
    event:       TerminalErrorEvent;
    message:     string;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalErrorEvent {
    TerminalError = "terminal_error",
}

export interface TerminalFocus {
    cmd:         TerminalFocusCmd;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalFocusCmd {
    TerminalFocus = "terminal_focus",
}

export interface TerminalInput {
    cmd:         TerminalInputCmd;
    data:        string;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalInputCmd {
    TerminalInput = "terminal_input",
}

export interface TerminalResize {
    cmd:         TerminalResizeCmd;
    cols:        number;
    rows:        number;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalResizeCmd {
    TerminalResize = "terminal_resize",
}

export interface TerminalResized {
    cols:        number;
    event:       TerminalResizedEvent;
    rows:        number;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalResizedEvent {
    TerminalResized = "terminal_resized",
}

export interface TerminalScreen {
    ansi:        string;
    cols:        number;
    event:       TerminalScreenEvent;
    rows:        number;
    seq:         number;
    terminal_id: string;
    [property: string]: any;
}

export enum TerminalScreenEvent {
    TerminalScreen = "terminal_screen",
}

// Converts JSON strings to/from your types
// and asserts the results of JSON.parse at runtime
export class Convert {
    public static toGatewayAsk(json: string): GatewayAsk {
        return cast(JSON.parse(json), r("GatewayAsk"));
    }

    public static gatewayAskToJson(value: GatewayAsk): string {
        return JSON.stringify(uncast(value, r("GatewayAsk")), null, 2);
    }

    public static toGatewayAskAnswer(json: string): GatewayAskAnswer {
        return cast(JSON.parse(json), r("GatewayAskAnswer"));
    }

    public static gatewayAskAnswerToJson(value: GatewayAskAnswer): string {
        return JSON.stringify(uncast(value, r("GatewayAskAnswer")), null, 2);
    }

    public static toGatewayAskAnswerCommand(json: string): GatewayAskAnswerCommand {
        return cast(JSON.parse(json), r("GatewayAskAnswerCommand"));
    }

    public static gatewayAskAnswerCommandToJson(value: GatewayAskAnswerCommand): string {
        return JSON.stringify(uncast(value, r("GatewayAskAnswerCommand")), null, 2);
    }

    public static toGatewayAskAnswerResult(json: string): GatewayAskAnswerResult {
        return cast(JSON.parse(json), r("GatewayAskAnswerResult"));
    }

    public static gatewayAskAnswerResultToJson(value: GatewayAskAnswerResult): string {
        return JSON.stringify(uncast(value, r("GatewayAskAnswerResult")), null, 2);
    }

    public static toGatewayAskChoice(json: string): GatewayAskChoice {
        return cast(JSON.parse(json), r("GatewayAskChoice"));
    }

    public static gatewayAskChoiceToJson(value: GatewayAskChoice): string {
        return JSON.stringify(uncast(value, r("GatewayAskChoice")), null, 2);
    }

    public static toGatewayAskEvent(json: string): GatewayAskEvent {
        return cast(JSON.parse(json), r("GatewayAskEvent"));
    }

    public static gatewayAskEventToJson(value: GatewayAskEvent): string {
        return JSON.stringify(uncast(value, r("GatewayAskEvent")), null, 2);
    }

    public static toGatewayAskImage(json: string): GatewayAskImage {
        return cast(JSON.parse(json), r("GatewayAskImage"));
    }

    public static gatewayAskImageToJson(value: GatewayAskImage): string {
        return JSON.stringify(uncast(value, r("GatewayAskImage")), null, 2);
    }

    public static toGatewayAskImageChunk(json: string): GatewayAskImageChunk {
        return cast(JSON.parse(json), r("GatewayAskImageChunk"));
    }

    public static gatewayAskImageChunkToJson(value: GatewayAskImageChunk): string {
        return JSON.stringify(uncast(value, r("GatewayAskImageChunk")), null, 2);
    }

    public static toGatewayAskImageError(json: string): GatewayAskImageError {
        return cast(JSON.parse(json), r("GatewayAskImageError"));
    }

    public static gatewayAskImageErrorToJson(value: GatewayAskImageError): string {
        return JSON.stringify(uncast(value, r("GatewayAskImageError")), null, 2);
    }

    public static toGatewayAskPart(json: string): GatewayAskPart {
        return cast(JSON.parse(json), r("GatewayAskPart"));
    }

    public static gatewayAskPartToJson(value: GatewayAskPart): string {
        return JSON.stringify(uncast(value, r("GatewayAskPart")), null, 2);
    }

    public static toGatewayAskQuestion(json: string): GatewayAskQuestion {
        return cast(JSON.parse(json), r("GatewayAskQuestion"));
    }

    public static gatewayAskQuestionToJson(value: GatewayAskQuestion): string {
        return JSON.stringify(uncast(value, r("GatewayAskQuestion")), null, 2);
    }

    public static toGatewayAskQuestionKind(json: string): GatewayAskQuestionKind {
        return cast(JSON.parse(json), r("GatewayAskQuestionKind"));
    }

    public static gatewayAskQuestionKindToJson(value: GatewayAskQuestionKind): string {
        return JSON.stringify(uncast(value, r("GatewayAskQuestionKind")), null, 2);
    }

    public static toGatewayAskReply(json: string): GatewayAskReply {
        return cast(JSON.parse(json), r("GatewayAskReply"));
    }

    public static gatewayAskReplyToJson(value: GatewayAskReply): string {
        return JSON.stringify(uncast(value, r("GatewayAskReply")), null, 2);
    }

    public static toGatewayAsks(json: string): GatewayAsks {
        return cast(JSON.parse(json), r("GatewayAsks"));
    }

    public static gatewayAsksToJson(value: GatewayAsks): string {
        return JSON.stringify(uncast(value, r("GatewayAsks")), null, 2);
    }

    public static toGatewayCheckState(json: string): GatewayCheckState {
        return cast(JSON.parse(json), r("GatewayCheckState"));
    }

    public static gatewayCheckStateToJson(value: GatewayCheckState): string {
        return JSON.stringify(uncast(value, r("GatewayCheckState")), null, 2);
    }

    public static toGatewayCheckTally(json: string): GatewayCheckTally {
        return cast(JSON.parse(json), r("GatewayCheckTally"));
    }

    public static gatewayCheckTallyToJson(value: GatewayCheckTally): string {
        return JSON.stringify(uncast(value, r("GatewayCheckTally")), null, 2);
    }

    public static toGatewayGarden(json: string): GatewayGarden {
        return cast(JSON.parse(json), r("GatewayGarden"));
    }

    public static gatewayGardenToJson(value: GatewayGarden): string {
        return JSON.stringify(uncast(value, r("GatewayGarden")), null, 2);
    }

    public static toGatewayGardenSeeds(json: string): GatewayGardenSeeds {
        return cast(JSON.parse(json), r("GatewayGardenSeeds"));
    }

    public static gatewayGardenSeedsToJson(value: GatewayGardenSeeds): string {
        return JSON.stringify(uncast(value, r("GatewayGardenSeeds")), null, 2);
    }

    public static toGatewayHello(json: string): GatewayHello {
        return cast(JSON.parse(json), r("GatewayHello"));
    }

    public static gatewayHelloToJson(value: GatewayHello): string {
        return JSON.stringify(uncast(value, r("GatewayHello")), null, 2);
    }

    public static toGatewayPing(json: string): GatewayPing {
        return cast(JSON.parse(json), r("GatewayPing"));
    }

    public static gatewayPingToJson(value: GatewayPing): string {
        return JSON.stringify(uncast(value, r("GatewayPing")), null, 2);
    }

    public static toGatewayPlotProgress(json: string): GatewayPlotProgress {
        return cast(JSON.parse(json), r("GatewayPlotProgress"));
    }

    public static gatewayPlotProgressToJson(value: GatewayPlotProgress): string {
        return JSON.stringify(uncast(value, r("GatewayPlotProgress")), null, 2);
    }

    public static toGatewayPong(json: string): GatewayPong {
        return cast(JSON.parse(json), r("GatewayPong"));
    }

    public static gatewayPongToJson(value: GatewayPong): string {
        return JSON.stringify(uncast(value, r("GatewayPong")), null, 2);
    }

    public static toGatewayProfile(json: string): GatewayProfile {
        return cast(JSON.parse(json), r("GatewayProfile"));
    }

    public static gatewayProfileToJson(value: GatewayProfile): string {
        return JSON.stringify(uncast(value, r("GatewayProfile")), null, 2);
    }

    public static toGatewayRefused(json: string): GatewayRefused {
        return cast(JSON.parse(json), r("GatewayRefused"));
    }

    public static gatewayRefusedToJson(value: GatewayRefused): string {
        return JSON.stringify(uncast(value, r("GatewayRefused")), null, 2);
    }

    public static toGatewaySeed(json: string): GatewaySeed {
        return cast(JSON.parse(json), r("GatewaySeed"));
    }

    public static gatewaySeedToJson(value: GatewaySeed): string {
        return JSON.stringify(uncast(value, r("GatewaySeed")), null, 2);
    }

    public static toGatewaySeedChanged(json: string): GatewaySeedChanged {
        return cast(JSON.parse(json), r("GatewaySeedChanged"));
    }

    public static gatewaySeedChangedToJson(value: GatewaySeedChanged): string {
        return JSON.stringify(uncast(value, r("GatewaySeedChanged")), null, 2);
    }

    public static toGatewaySeedCheck(json: string): GatewaySeedCheck {
        return cast(JSON.parse(json), r("GatewaySeedCheck"));
    }

    public static gatewaySeedCheckToJson(value: GatewaySeedCheck): string {
        return JSON.stringify(uncast(value, r("GatewaySeedCheck")), null, 2);
    }

    public static toGatewaySeedDetail(json: string): GatewaySeedDetail {
        return cast(JSON.parse(json), r("GatewaySeedDetail"));
    }

    public static gatewaySeedDetailToJson(value: GatewaySeedDetail): string {
        return JSON.stringify(uncast(value, r("GatewaySeedDetail")), null, 2);
    }

    public static toGatewaySeedError(json: string): GatewaySeedError {
        return cast(JSON.parse(json), r("GatewaySeedError"));
    }

    public static gatewaySeedErrorToJson(value: GatewaySeedError): string {
        return JSON.stringify(uncast(value, r("GatewaySeedError")), null, 2);
    }

    public static toGatewaySeedGet(json: string): GatewaySeedGet {
        return cast(JSON.parse(json), r("GatewaySeedGet"));
    }

    public static gatewaySeedGetToJson(value: GatewaySeedGet): string {
        return JSON.stringify(uncast(value, r("GatewaySeedGet")), null, 2);
    }

    public static toGatewaySeedLogEntry(json: string): GatewaySeedLogEntry {
        return cast(JSON.parse(json), r("GatewaySeedLogEntry"));
    }

    public static gatewaySeedLogEntryToJson(value: GatewaySeedLogEntry): string {
        return JSON.stringify(uncast(value, r("GatewaySeedLogEntry")), null, 2);
    }

    public static toGatewaySeedMove(json: string): GatewaySeedMove {
        return cast(JSON.parse(json), r("GatewaySeedMove"));
    }

    public static gatewaySeedMoveToJson(value: GatewaySeedMove): string {
        return JSON.stringify(uncast(value, r("GatewaySeedMove")), null, 2);
    }

    public static toGatewaySeedNoteCommand(json: string): GatewaySeedNoteCommand {
        return cast(JSON.parse(json), r("GatewaySeedNoteCommand"));
    }

    public static gatewaySeedNoteCommandToJson(value: GatewaySeedNoteCommand): string {
        return JSON.stringify(uncast(value, r("GatewaySeedNoteCommand")), null, 2);
    }

    public static toGatewaySeedResult(json: string): GatewaySeedResult {
        return cast(JSON.parse(json), r("GatewaySeedResult"));
    }

    public static gatewaySeedResultToJson(value: GatewaySeedResult): string {
        return JSON.stringify(uncast(value, r("GatewaySeedResult")), null, 2);
    }

    public static toGatewaySeedStatus(json: string): GatewaySeedStatus {
        return cast(JSON.parse(json), r("GatewaySeedStatus"));
    }

    public static gatewaySeedStatusToJson(value: GatewaySeedStatus): string {
        return JSON.stringify(uncast(value, r("GatewaySeedStatus")), null, 2);
    }

    public static toGatewaySeedVerb(json: string): GatewaySeedVerb {
        return cast(JSON.parse(json), r("GatewaySeedVerb"));
    }

    public static gatewaySeedVerbToJson(value: GatewaySeedVerb): string {
        return JSON.stringify(uncast(value, r("GatewaySeedVerb")), null, 2);
    }

    public static toGatewaySession(json: string): GatewaySession {
        return cast(JSON.parse(json), r("GatewaySession"));
    }

    public static gatewaySessionToJson(value: GatewaySession): string {
        return JSON.stringify(uncast(value, r("GatewaySession")), null, 2);
    }

    public static toGatewaySessionState(json: string): GatewaySessionState {
        return cast(JSON.parse(json), r("GatewaySessionState"));
    }

    public static gatewaySessionStateToJson(value: GatewaySessionState): string {
        return JSON.stringify(uncast(value, r("GatewaySessionState")), null, 2);
    }

    public static toGatewaySessions(json: string): GatewaySessions {
        return cast(JSON.parse(json), r("GatewaySessions"));
    }

    public static gatewaySessionsToJson(value: GatewaySessions): string {
        return JSON.stringify(uncast(value, r("GatewaySessions")), null, 2);
    }

    public static toGatewayWelcome(json: string): GatewayWelcome {
        return cast(JSON.parse(json), r("GatewayWelcome"));
    }

    public static gatewayWelcomeToJson(value: GatewayWelcome): string {
        return JSON.stringify(uncast(value, r("GatewayWelcome")), null, 2);
    }

    public static toSubmitPrompt(json: string): SubmitPrompt {
        return cast(JSON.parse(json), r("SubmitPrompt"));
    }

    public static submitPromptToJson(value: SubmitPrompt): string {
        return JSON.stringify(uncast(value, r("SubmitPrompt")), null, 2);
    }

    public static toSubmitResult(json: string): SubmitResult {
        return cast(JSON.parse(json), r("SubmitResult"));
    }

    public static submitResultToJson(value: SubmitResult): string {
        return JSON.stringify(uncast(value, r("SubmitResult")), null, 2);
    }

    public static toTerminalAttach(json: string): TerminalAttach {
        return cast(JSON.parse(json), r("TerminalAttach"));
    }

    public static terminalAttachToJson(value: TerminalAttach): string {
        return JSON.stringify(uncast(value, r("TerminalAttach")), null, 2);
    }

    public static toTerminalCredit(json: string): TerminalCredit {
        return cast(JSON.parse(json), r("TerminalCredit"));
    }

    public static terminalCreditToJson(value: TerminalCredit): string {
        return JSON.stringify(uncast(value, r("TerminalCredit")), null, 2);
    }

    public static toTerminalDetach(json: string): TerminalDetach {
        return cast(JSON.parse(json), r("TerminalDetach"));
    }

    public static terminalDetachToJson(value: TerminalDetach): string {
        return JSON.stringify(uncast(value, r("TerminalDetach")), null, 2);
    }

    public static toTerminalEnded(json: string): TerminalEnded {
        return cast(JSON.parse(json), r("TerminalEnded"));
    }

    public static terminalEndedToJson(value: TerminalEnded): string {
        return JSON.stringify(uncast(value, r("TerminalEnded")), null, 2);
    }

    public static toTerminalError(json: string): TerminalError {
        return cast(JSON.parse(json), r("TerminalError"));
    }

    public static terminalErrorToJson(value: TerminalError): string {
        return JSON.stringify(uncast(value, r("TerminalError")), null, 2);
    }

    public static toTerminalFocus(json: string): TerminalFocus {
        return cast(JSON.parse(json), r("TerminalFocus"));
    }

    public static terminalFocusToJson(value: TerminalFocus): string {
        return JSON.stringify(uncast(value, r("TerminalFocus")), null, 2);
    }

    public static toTerminalInput(json: string): TerminalInput {
        return cast(JSON.parse(json), r("TerminalInput"));
    }

    public static terminalInputToJson(value: TerminalInput): string {
        return JSON.stringify(uncast(value, r("TerminalInput")), null, 2);
    }

    public static toTerminalResize(json: string): TerminalResize {
        return cast(JSON.parse(json), r("TerminalResize"));
    }

    public static terminalResizeToJson(value: TerminalResize): string {
        return JSON.stringify(uncast(value, r("TerminalResize")), null, 2);
    }

    public static toTerminalResized(json: string): TerminalResized {
        return cast(JSON.parse(json), r("TerminalResized"));
    }

    public static terminalResizedToJson(value: TerminalResized): string {
        return JSON.stringify(uncast(value, r("TerminalResized")), null, 2);
    }

    public static toTerminalScreen(json: string): TerminalScreen {
        return cast(JSON.parse(json), r("TerminalScreen"));
    }

    public static terminalScreenToJson(value: TerminalScreen): string {
        return JSON.stringify(uncast(value, r("TerminalScreen")), null, 2);
    }
}

function invalidValue(typ: any, val: any, key: any, parent: any = ''): never {
    const prettyTyp = prettyTypeName(typ);
    const parentText = parent ? ` on ${parent}` : '';
    const keyText = key ? ` for key "${key}"` : '';
    throw Error(`Invalid value${keyText}${parentText}. Expected ${prettyTyp} but got ${JSON.stringify(val)}`);
}

function prettyTypeName(typ: any): string {
    if (Array.isArray(typ)) {
        if (typ.length === 2 && typ[0] === undefined) {
            return `an optional ${prettyTypeName(typ[1])}`;
        } else {
            return `one of [${typ.map(a => { return prettyTypeName(a); }).join(", ")}]`;
        }
    } else if (typeof typ === "object" && typ.literal !== undefined) {
        return typ.literal;
    } else {
        return typeof typ;
    }
}

function jsonToJSProps(typ: any): any {
    if (typ.jsonToJS === undefined) {
        const map: any = {};
        typ.props.forEach((p: any) => map[p.json] = { key: p.js, typ: p.typ });
        typ.jsonToJS = map;
    }
    return typ.jsonToJS;
}

function jsToJSONProps(typ: any): any {
    if (typ.jsToJSON === undefined) {
        const map: any = {};
        typ.props.forEach((p: any) => map[p.js] = { key: p.json, typ: p.typ });
        typ.jsToJSON = map;
    }
    return typ.jsToJSON;
}

function transform(val: any, typ: any, getProps: any, key: any = '', parent: any = ''): any {
    function transformPrimitive(typ: string, val: any): any {
        if (typeof typ === typeof val) return val;
        return invalidValue(typ, val, key, parent);
    }

    function transformUnion(typs: any[], val: any): any {
        // val must validate against one typ in typs
        const l = typs.length;
        for (let i = 0; i < l; i++) {
            const typ = typs[i];
            try {
                return transform(val, typ, getProps);
            } catch (_) {}
        }
        return invalidValue(typs, val, key, parent);
    }

    function transformEnum(cases: string[], val: any): any {
        if (cases.indexOf(val) !== -1) return val;
        return invalidValue(cases.map(a => { return l(a); }), val, key, parent);
    }

    function transformArray(typ: any, val: any): any {
        // val must be an array with no invalid elements
        if (!Array.isArray(val)) return invalidValue(l("array"), val, key, parent);
        return val.map(el => transform(el, typ, getProps));
    }

    function transformDate(val: any): any {
        if (val === null) {
            return null;
        }
        const d = new Date(val);
        if (isNaN(d.valueOf())) {
            return invalidValue(l("Date"), val, key, parent);
        }
        return d;
    }

    function transformObject(props: { [k: string]: any }, additional: any, val: any): any {
        if (val === null || typeof val !== "object" || Array.isArray(val)) {
            return invalidValue(l(ref || "object"), val, key, parent);
        }
        const result: any = {};
        Object.getOwnPropertyNames(props).forEach(key => {
            const prop = props[key];
            const v = Object.prototype.hasOwnProperty.call(val, key) ? val[key] : undefined;
            result[prop.key] = transform(v, prop.typ, getProps, key, ref);
        });
        Object.getOwnPropertyNames(val).forEach(key => {
            if (!Object.prototype.hasOwnProperty.call(props, key)) {
                result[key] = transform(val[key], additional, getProps, key, ref);
            }
        });
        return result;
    }

    if (typ === "any") return val;
    if (typ === null) {
        if (val === null) return val;
        return invalidValue(typ, val, key, parent);
    }
    if (typ === false) return invalidValue(typ, val, key, parent);
    let ref: any = undefined;
    while (typeof typ === "object" && typ.ref !== undefined) {
        ref = typ.ref;
        typ = typeMap[typ.ref];
    }
    if (Array.isArray(typ)) return transformEnum(typ, val);
    if (typeof typ === "object") {
        return typ.hasOwnProperty("unionMembers") ? transformUnion(typ.unionMembers, val)
            : typ.hasOwnProperty("arrayItems")    ? transformArray(typ.arrayItems, val)
            : typ.hasOwnProperty("props")         ? transformObject(getProps(typ), typ.additional, val)
            : invalidValue(typ, val, key, parent);
    }
    // Numbers can be parsed by Date but shouldn't be.
    if (typ === Date && typeof val !== "number") return transformDate(val);
    return transformPrimitive(typ, val);
}

function cast<T>(val: any, typ: any): T {
    return transform(val, typ, jsonToJSProps);
}

function uncast<T>(val: T, typ: any): any {
    return transform(val, typ, jsToJSONProps);
}

function l(typ: any) {
    return { literal: typ };
}

function a(typ: any) {
    return { arrayItems: typ };
}

function u(...typs: any[]) {
    return { unionMembers: typs };
}

function o(props: any[], additional: any) {
    return { props, additional };
}

function r(name: string) {
    return { ref: name };
}

const typeMap: any = {
    "GatewayAsk": o([
        { json: "chief", js: "chief", typ: u(undefined, true) },
        { json: "created_at", js: "created_at", typ: Date },
        { json: "crew_member", js: "crew_member", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "max_frame_bytes", js: "max_frame_bytes", typ: u(undefined, 0) },
        { json: "parts", js: "parts", typ: a(r("PartElement")) },
        { json: "reply", js: "reply", typ: u(undefined, r("Reply")) },
        { json: "seed_slug", js: "seed_slug", typ: u(undefined, "") },
        { json: "seed_title", js: "seed_title", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "subject", js: "subject", typ: "" },
        { json: "too_large_bytes", js: "too_large_bytes", typ: u(undefined, 0) },
    ], "any"),
    "PartElement": o([
        { json: "markdown", js: "markdown", typ: u(undefined, "") },
        { json: "question", js: "question", typ: u(undefined, r("Question")) },
    ], "any"),
    "Question": o([
        { json: "choices", js: "choices", typ: a(r("ChoiceElement")) },
        { json: "context", js: "context", typ: u(undefined, "") },
        { json: "key", js: "key", typ: "" },
        { json: "kind", js: "kind", typ: r("GatewayAskQuestionKind") },
        { json: "prompt", js: "prompt", typ: "" },
        { json: "recommended", js: "recommended", typ: u(undefined, "") },
    ], "any"),
    "ChoiceElement": o([
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "label", js: "label", typ: "" },
    ], "any"),
    "Reply": o([
        { json: "answered_at", js: "answered_at", typ: Date },
        { json: "answers", js: "answers", typ: a(r("AnswerElement")) },
        { json: "note", js: "note", typ: u(undefined, "") },
    ], "any"),
    "AnswerElement": o([
        { json: "key", js: "key", typ: "" },
        { json: "other", js: "other", typ: u(undefined, "") },
        { json: "picked", js: "picked", typ: a("") },
        { json: "text", js: "text", typ: u(undefined, "") },
    ], "any"),
    "GatewayAskAnswer": o([
        { json: "key", js: "key", typ: "" },
        { json: "other", js: "other", typ: u(undefined, "") },
        { json: "picked", js: "picked", typ: a("") },
        { json: "text", js: "text", typ: u(undefined, "") },
    ], "any"),
    "GatewayAskAnswerCommand": o([
        { json: "answers", js: "answers", typ: a(r("AnswerElement")) },
        { json: "ask_id", js: "ask_id", typ: "" },
        { json: "cmd", js: "cmd", typ: r("GatewayAskAnswerCommandCmd") },
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "GatewayAskAnswerResult": o([
        { json: "ask_id", js: "ask_id", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GatewayAskAnswerResultEvent") },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "GatewayAskChoice": o([
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "label", js: "label", typ: "" },
    ], "any"),
    "GatewayAskEvent": o([
        { json: "ask", js: "ask", typ: r("Ask") },
        { json: "event", js: "event", typ: r("GatewayAskEventEvent") },
    ], "any"),
    "Ask": o([
        { json: "chief", js: "chief", typ: u(undefined, true) },
        { json: "created_at", js: "created_at", typ: Date },
        { json: "crew_member", js: "crew_member", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "max_frame_bytes", js: "max_frame_bytes", typ: u(undefined, 0) },
        { json: "parts", js: "parts", typ: a(r("PartElement")) },
        { json: "reply", js: "reply", typ: u(undefined, r("Reply")) },
        { json: "seed_slug", js: "seed_slug", typ: u(undefined, "") },
        { json: "seed_title", js: "seed_title", typ: u(undefined, "") },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "subject", js: "subject", typ: "" },
        { json: "too_large_bytes", js: "too_large_bytes", typ: u(undefined, 0) },
    ], "any"),
    "GatewayAskImage": o([
        { json: "ask_id", js: "ask_id", typ: "" },
        { json: "cmd", js: "cmd", typ: r("GatewayAskImageCmd") },
        { json: "offset", js: "offset", typ: 0 },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "src", js: "src", typ: "" },
    ], "any"),
    "GatewayAskImageChunk": o([
        { json: "data_base64", js: "data_base64", typ: "" },
        { json: "event", js: "event", typ: r("GatewayAskImageChunkEvent") },
        { json: "mime_type", js: "mime_type", typ: "" },
        { json: "offset", js: "offset", typ: 0 },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "size", js: "size", typ: 0 },
    ], "any"),
    "GatewayAskImageError": o([
        { json: "event", js: "event", typ: r("GatewayAskImageErrorEvent") },
        { json: "message", js: "message", typ: "" },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "GatewayAskPart": o([
        { json: "markdown", js: "markdown", typ: u(undefined, "") },
        { json: "question", js: "question", typ: u(undefined, r("Question")) },
    ], "any"),
    "GatewayAskQuestion": o([
        { json: "choices", js: "choices", typ: a(r("ChoiceElement")) },
        { json: "context", js: "context", typ: u(undefined, "") },
        { json: "key", js: "key", typ: "" },
        { json: "kind", js: "kind", typ: r("GatewayAskQuestionKind") },
        { json: "prompt", js: "prompt", typ: "" },
        { json: "recommended", js: "recommended", typ: u(undefined, "") },
    ], "any"),
    "GatewayAskReply": o([
        { json: "answered_at", js: "answered_at", typ: Date },
        { json: "answers", js: "answers", typ: a(r("AnswerElement")) },
        { json: "note", js: "note", typ: u(undefined, "") },
    ], "any"),
    "GatewayAsks": o([
        { json: "event", js: "event", typ: r("GatewayAsksEvent") },
        { json: "ids", js: "ids", typ: a("") },
    ], "any"),
    "GatewayCheckTally": o([
        { json: "confirmed", js: "confirmed", typ: 0 },
        { json: "total", js: "total", typ: 0 },
    ], "any"),
    "GatewayGarden": o([
        { json: "event", js: "event", typ: r("GatewayGardenEvent") },
        { json: "ids", js: "ids", typ: a("") },
    ], "any"),
    "GatewayGardenSeeds": o([
        { json: "event", js: "event", typ: r("GatewayGardenSeedsEvent") },
        { json: "seeds", js: "seeds", typ: a(r("Seed")) },
    ], "any"),
    "Seed": o([
        { json: "checks", js: "checks", typ: u(undefined, r("Checks")) },
        { json: "claimed", js: "claimed", typ: true },
        { json: "id", js: "id", typ: "" },
        { json: "plot_id", js: "plot_id", typ: u(undefined, "") },
        { json: "progress", js: "progress", typ: u(undefined, r("Progress")) },
        { json: "ready", js: "ready", typ: true },
        { json: "reason", js: "reason", typ: u(undefined, "") },
        { json: "slug", js: "slug", typ: "" },
        { json: "state_changed_at", js: "state_changed_at", typ: Date },
        { json: "status", js: "status", typ: r("GatewaySeedStatus") },
        { json: "tender", js: "tender", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waits_for", js: "waits_for", typ: a("") },
    ], "any"),
    "Checks": o([
        { json: "confirmed", js: "confirmed", typ: 0 },
        { json: "total", js: "total", typ: 0 },
    ], "any"),
    "Progress": o([
        { json: "blocked", js: "blocked", typ: 0 },
        { json: "done", js: "done", typ: 0 },
        { json: "growing", js: "growing", typ: 0 },
        { json: "parked", js: "parked", typ: 0 },
        { json: "ready", js: "ready", typ: 0 },
        { json: "total", js: "total", typ: 0 },
        { json: "withered", js: "withered", typ: 0 },
    ], "any"),
    "GatewayHello": o([
        { json: "api", js: "api", typ: 0 },
        { json: "cmd", js: "cmd", typ: r("GatewayHelloCmd") },
        { json: "device_id", js: "device_id", typ: "" },
    ], "any"),
    "GatewayPing": o([
        { json: "cmd", js: "cmd", typ: r("GatewayPingCmd") },
    ], "any"),
    "GatewayPlotProgress": o([
        { json: "blocked", js: "blocked", typ: 0 },
        { json: "done", js: "done", typ: 0 },
        { json: "growing", js: "growing", typ: 0 },
        { json: "parked", js: "parked", typ: 0 },
        { json: "ready", js: "ready", typ: 0 },
        { json: "total", js: "total", typ: 0 },
        { json: "withered", js: "withered", typ: 0 },
    ], "any"),
    "GatewayPong": o([
        { json: "event", js: "event", typ: r("GatewayPongEvent") },
    ], "any"),
    "GatewayProfile": o([
        { json: "id", js: "id", typ: "" },
        { json: "name", js: "name", typ: "" },
    ], "any"),
    "GatewayRefused": o([
        { json: "event", js: "event", typ: r("GatewayRefusedEvent") },
        { json: "message", js: "message", typ: "" },
    ], "any"),
    "GatewaySeed": o([
        { json: "checks", js: "checks", typ: u(undefined, r("Checks")) },
        { json: "claimed", js: "claimed", typ: true },
        { json: "id", js: "id", typ: "" },
        { json: "plot_id", js: "plot_id", typ: u(undefined, "") },
        { json: "progress", js: "progress", typ: u(undefined, r("Progress")) },
        { json: "ready", js: "ready", typ: true },
        { json: "reason", js: "reason", typ: u(undefined, "") },
        { json: "slug", js: "slug", typ: "" },
        { json: "state_changed_at", js: "state_changed_at", typ: Date },
        { json: "status", js: "status", typ: r("GatewaySeedStatus") },
        { json: "tender", js: "tender", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waits_for", js: "waits_for", typ: a("") },
    ], "any"),
    "GatewaySeedChanged": o([
        { json: "event", js: "event", typ: r("GatewaySeedChangedEvent") },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "GatewaySeedCheck": o([
        { json: "from", js: "from", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "state", js: "state", typ: r("GatewayCheckState") },
        { json: "text", js: "text", typ: "" },
    ], "any"),
    "GatewaySeedDetail": o([
        { json: "body", js: "body", typ: "" },
        { json: "body_too_large_bytes", js: "body_too_large_bytes", typ: u(undefined, 0) },
        { json: "checks", js: "checks", typ: a(r("CheckElement")) },
        { json: "children", js: "children", typ: a(r("Seed")) },
        { json: "event", js: "event", typ: r("GatewaySeedDetailEvent") },
        { json: "log", js: "log", typ: a(r("LogElement")) },
        { json: "log_total", js: "log_total", typ: 0 },
        { json: "max_frame_bytes", js: "max_frame_bytes", typ: u(undefined, 0) },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed", js: "seed", typ: r("Seed") },
    ], "any"),
    "CheckElement": o([
        { json: "from", js: "from", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "state", js: "state", typ: r("GatewayCheckState") },
        { json: "text", js: "text", typ: "" },
    ], "any"),
    "LogElement": o([
        { json: "author", js: "author", typ: "" },
        { json: "body", js: "body", typ: "" },
        { json: "created_at", js: "created_at", typ: Date },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
    ], "any"),
    "GatewaySeedError": o([
        { json: "event", js: "event", typ: r("GatewaySeedErrorEvent") },
        { json: "message", js: "message", typ: "" },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "GatewaySeedGet": o([
        { json: "cmd", js: "cmd", typ: r("GatewaySeedGetCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "GatewaySeedLogEntry": o([
        { json: "author", js: "author", typ: "" },
        { json: "body", js: "body", typ: "" },
        { json: "created_at", js: "created_at", typ: Date },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
    ], "any"),
    "GatewaySeedMove": o([
        { json: "cmd", js: "cmd", typ: r("GatewaySeedMoveCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
        { json: "text", js: "text", typ: "" },
        { json: "verb", js: "verb", typ: r("GatewaySeedVerb") },
    ], "any"),
    "GatewaySeedNoteCommand": o([
        { json: "body", js: "body", typ: "" },
        { json: "cmd", js: "cmd", typ: r("GatewaySeedNoteCommandCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "GatewaySeedResult": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("GatewaySeedResultEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "GatewaySession": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "host", js: "host", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "offline", js: "offline", typ: true },
        { json: "state", js: "state", typ: r("GatewaySessionState") },
        { json: "terminal_id", js: "terminal_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waiting_since", js: "waiting_since", typ: u(undefined, Date) },
    ], "any"),
    "GatewaySessions": o([
        { json: "event", js: "event", typ: r("GatewaySessionsEvent") },
        { json: "sessions", js: "sessions", typ: a(r("SessionElement")) },
    ], "any"),
    "SessionElement": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "host", js: "host", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "offline", js: "offline", typ: true },
        { json: "state", js: "state", typ: r("GatewaySessionState") },
        { json: "terminal_id", js: "terminal_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waiting_since", js: "waiting_since", typ: u(undefined, Date) },
    ], "any"),
    "GatewayWelcome": o([
        { json: "api", js: "api", typ: 0 },
        { json: "event", js: "event", typ: r("GatewayWelcomeEvent") },
        { json: "home_name", js: "home_name", typ: "" },
        { json: "profile", js: "profile", typ: r("Profile") },
    ], "any"),
    "Profile": o([
        { json: "id", js: "id", typ: "" },
        { json: "name", js: "name", typ: "" },
    ], "any"),
    "SubmitPrompt": o([
        { json: "cmd", js: "cmd", typ: r("SubmitPromptCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "session_id", js: "session_id", typ: "" },
        { json: "text", js: "text", typ: "" },
    ], "any"),
    "SubmitResult": o([
        { json: "delivered", js: "delivered", typ: true },
        { json: "event", js: "event", typ: r("SubmitResultEvent") },
        { json: "message", js: "message", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "TerminalAttach": o([
        { json: "cmd", js: "cmd", typ: r("TerminalAttachCmd") },
        { json: "cols", js: "cols", typ: 0 },
        { json: "credit", js: "credit", typ: 0 },
        { json: "rows", js: "rows", typ: 0 },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalCredit": o([
        { json: "bytes", js: "bytes", typ: 0 },
        { json: "cmd", js: "cmd", typ: r("TerminalCreditCmd") },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalDetach": o([
        { json: "cmd", js: "cmd", typ: r("TerminalDetachCmd") },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalEnded": o([
        { json: "event", js: "event", typ: r("TerminalEndedEvent") },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalError": o([
        { json: "event", js: "event", typ: r("TerminalErrorEvent") },
        { json: "message", js: "message", typ: "" },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalFocus": o([
        { json: "cmd", js: "cmd", typ: r("TerminalFocusCmd") },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalInput": o([
        { json: "cmd", js: "cmd", typ: r("TerminalInputCmd") },
        { json: "data", js: "data", typ: "" },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalResize": o([
        { json: "cmd", js: "cmd", typ: r("TerminalResizeCmd") },
        { json: "cols", js: "cols", typ: 0 },
        { json: "rows", js: "rows", typ: 0 },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalResized": o([
        { json: "cols", js: "cols", typ: 0 },
        { json: "event", js: "event", typ: r("TerminalResizedEvent") },
        { json: "rows", js: "rows", typ: 0 },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "TerminalScreen": o([
        { json: "ansi", js: "ansi", typ: "" },
        { json: "cols", js: "cols", typ: 0 },
        { json: "event", js: "event", typ: r("TerminalScreenEvent") },
        { json: "rows", js: "rows", typ: 0 },
        { json: "seq", js: "seq", typ: 0 },
        { json: "terminal_id", js: "terminal_id", typ: "" },
    ], "any"),
    "GatewayAskQuestionKind": [
        "any",
        "one",
        "text",
        "unknown",
    ],
    "GatewayAskAnswerCommandCmd": [
        "ask_answer",
    ],
    "GatewayAskAnswerResultEvent": [
        "ask_answer_result",
    ],
    "GatewayAskEventEvent": [
        "ask",
    ],
    "GatewayAskImageCmd": [
        "ask_image",
    ],
    "GatewayAskImageChunkEvent": [
        "ask_image_chunk",
    ],
    "GatewayAskImageErrorEvent": [
        "ask_image_error",
    ],
    "GatewayAsksEvent": [
        "asks",
    ],
    "GatewayGardenEvent": [
        "garden",
    ],
    "GatewayGardenSeedsEvent": [
        "garden_seeds",
    ],
    "GatewaySeedStatus": [
        "growing",
        "harvested",
        "parked",
        "planted",
        "unknown",
        "withered",
    ],
    "GatewayHelloCmd": [
        "hello",
    ],
    "GatewayPingCmd": [
        "ping",
    ],
    "GatewayPongEvent": [
        "pong",
    ],
    "GatewayRefusedEvent": [
        "refused",
    ],
    "GatewaySeedChangedEvent": [
        "seed_changed",
    ],
    "GatewayCheckState": [
        "confirmed",
        "open",
        "rejected",
        "reported",
        "unknown",
    ],
    "GatewaySeedDetailEvent": [
        "seed_detail",
    ],
    "GatewaySeedErrorEvent": [
        "seed_error",
    ],
    "GatewaySeedGetCmd": [
        "seed_get",
    ],
    "GatewaySeedMoveCmd": [
        "seed_move",
    ],
    "GatewaySeedVerb": [
        "harvest",
        "park",
        "replant",
        "unknown",
        "wither",
    ],
    "GatewaySeedNoteCommandCmd": [
        "seed_note",
    ],
    "GatewaySeedResultEvent": [
        "seed_result",
    ],
    "GatewaySessionState": [
        "idle",
        "launching",
        "needs_approval",
        "recoverable",
        "scheduled",
        "unknown",
        "waiting",
        "working",
    ],
    "GatewaySessionsEvent": [
        "sessions",
    ],
    "GatewayWelcomeEvent": [
        "welcome",
    ],
    "SubmitPromptCmd": [
        "submit_prompt",
    ],
    "SubmitResultEvent": [
        "submit_result",
    ],
    "TerminalAttachCmd": [
        "terminal_attach",
    ],
    "TerminalCreditCmd": [
        "terminal_credit",
    ],
    "TerminalDetachCmd": [
        "terminal_detach",
    ],
    "TerminalEndedEvent": [
        "terminal_ended",
    ],
    "TerminalErrorEvent": [
        "terminal_error",
    ],
    "TerminalFocusCmd": [
        "terminal_focus",
    ],
    "TerminalInputCmd": [
        "terminal_input",
    ],
    "TerminalResizeCmd": [
        "terminal_resize",
    ],
    "TerminalResizedEvent": [
        "terminal_resized",
    ],
    "TerminalScreenEvent": [
        "terminal_screen",
    ],
};
