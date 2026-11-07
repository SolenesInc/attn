// To parse this data:
//
//   import { Convert, HomeGatewayAsk, HomeGatewayAskAnswer, HomeGatewayAskAnswerCommand, HomeGatewayAskAnswerResult, HomeGatewayAskChoice, HomeGatewayAskEvent, HomeGatewayAskImage, HomeGatewayAskImageChunk, HomeGatewayAskImageError, HomeGatewayAskPart, HomeGatewayAskQuestion, HomeGatewayAskQuestionKind, HomeGatewayAskReply, HomeGatewayAsks, HomeGatewayCheckState, HomeGatewayCheckTally, HomeGatewayGarden, HomeGatewayGardenSeeds, HomeGatewayHello, HomeGatewayPing, HomeGatewayPlotProgress, HomeGatewayPong, HomeGatewayProfile, HomeGatewayRefused, HomeGatewaySeed, HomeGatewaySeedChanged, HomeGatewaySeedCheck, HomeGatewaySeedDetail, HomeGatewaySeedError, HomeGatewaySeedGet, HomeGatewaySeedLogEntry, HomeGatewaySeedMove, HomeGatewaySeedNoteCommand, HomeGatewaySeedResult, HomeGatewaySeedStatus, HomeGatewaySeedVerb, HomeGatewaySession, HomeGatewaySessionState, HomeGatewaySessions, HomeGatewayWelcome, SubmitPrompt, SubmitResult, TerminalAttach, TerminalCredit, TerminalDetach, TerminalEnded, TerminalError, TerminalFocus, TerminalInput, TerminalResize, TerminalResized, TerminalScreen } from "./homeGatewayGenerated";
//
//   const homeGatewayAsk = Convert.toHomeGatewayAsk(json);
//   const homeGatewayAskAnswer = Convert.toHomeGatewayAskAnswer(json);
//   const homeGatewayAskAnswerCommand = Convert.toHomeGatewayAskAnswerCommand(json);
//   const homeGatewayAskAnswerResult = Convert.toHomeGatewayAskAnswerResult(json);
//   const homeGatewayAskChoice = Convert.toHomeGatewayAskChoice(json);
//   const homeGatewayAskEvent = Convert.toHomeGatewayAskEvent(json);
//   const homeGatewayAskImage = Convert.toHomeGatewayAskImage(json);
//   const homeGatewayAskImageChunk = Convert.toHomeGatewayAskImageChunk(json);
//   const homeGatewayAskImageError = Convert.toHomeGatewayAskImageError(json);
//   const homeGatewayAskPart = Convert.toHomeGatewayAskPart(json);
//   const homeGatewayAskQuestion = Convert.toHomeGatewayAskQuestion(json);
//   const homeGatewayAskQuestionKind = Convert.toHomeGatewayAskQuestionKind(json);
//   const homeGatewayAskReply = Convert.toHomeGatewayAskReply(json);
//   const homeGatewayAsks = Convert.toHomeGatewayAsks(json);
//   const homeGatewayCheckState = Convert.toHomeGatewayCheckState(json);
//   const homeGatewayCheckTally = Convert.toHomeGatewayCheckTally(json);
//   const homeGatewayGarden = Convert.toHomeGatewayGarden(json);
//   const homeGatewayGardenSeeds = Convert.toHomeGatewayGardenSeeds(json);
//   const homeGatewayHello = Convert.toHomeGatewayHello(json);
//   const homeGatewayPing = Convert.toHomeGatewayPing(json);
//   const homeGatewayPlotProgress = Convert.toHomeGatewayPlotProgress(json);
//   const homeGatewayPong = Convert.toHomeGatewayPong(json);
//   const homeGatewayProfile = Convert.toHomeGatewayProfile(json);
//   const homeGatewayRefused = Convert.toHomeGatewayRefused(json);
//   const homeGatewaySeed = Convert.toHomeGatewaySeed(json);
//   const homeGatewaySeedChanged = Convert.toHomeGatewaySeedChanged(json);
//   const homeGatewaySeedCheck = Convert.toHomeGatewaySeedCheck(json);
//   const homeGatewaySeedDetail = Convert.toHomeGatewaySeedDetail(json);
//   const homeGatewaySeedError = Convert.toHomeGatewaySeedError(json);
//   const homeGatewaySeedGet = Convert.toHomeGatewaySeedGet(json);
//   const homeGatewaySeedLogEntry = Convert.toHomeGatewaySeedLogEntry(json);
//   const homeGatewaySeedMove = Convert.toHomeGatewaySeedMove(json);
//   const homeGatewaySeedNoteCommand = Convert.toHomeGatewaySeedNoteCommand(json);
//   const homeGatewaySeedResult = Convert.toHomeGatewaySeedResult(json);
//   const homeGatewaySeedStatus = Convert.toHomeGatewaySeedStatus(json);
//   const homeGatewaySeedVerb = Convert.toHomeGatewaySeedVerb(json);
//   const homeGatewaySession = Convert.toHomeGatewaySession(json);
//   const homeGatewaySessionState = Convert.toHomeGatewaySessionState(json);
//   const homeGatewaySessions = Convert.toHomeGatewaySessions(json);
//   const homeGatewayWelcome = Convert.toHomeGatewayWelcome(json);
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

export interface HomeGatewayAsk {
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
    kind:         HomeGatewayAskQuestionKind;
    prompt:       string;
    recommended?: string;
    [property: string]: any;
}

export interface ChoiceElement {
    description?: string;
    label:        string;
    [property: string]: any;
}

export enum HomeGatewayAskQuestionKind {
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

export interface HomeGatewayAskAnswer {
    key:    string;
    other?: string;
    picked: string[];
    text?:  string;
    [property: string]: any;
}

export interface HomeGatewayAskAnswerCommand {
    answers:    AnswerElement[];
    ask_id:     string;
    cmd:        HomeGatewayAskAnswerCommandCmd;
    note?:      string;
    request_id: string;
    [property: string]: any;
}

export enum HomeGatewayAskAnswerCommandCmd {
    AskAnswer = "ask_answer",
}

export interface HomeGatewayAskAnswerResult {
    ask_id:     string;
    error?:     string;
    event:      HomeGatewayAskAnswerResultEvent;
    request_id: string;
    [property: string]: any;
}

export enum HomeGatewayAskAnswerResultEvent {
    AskAnswerResult = "ask_answer_result",
}

export interface HomeGatewayAskChoice {
    description?: string;
    label:        string;
    [property: string]: any;
}

export interface HomeGatewayAskEvent {
    ask:   Ask;
    event: HomeGatewayAskEventEvent;
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

export enum HomeGatewayAskEventEvent {
    Ask = "ask",
}

export interface HomeGatewayAskImage {
    ask_id:     string;
    cmd:        HomeGatewayAskImageCmd;
    offset:     number;
    request_id: string;
    src:        string;
    [property: string]: any;
}

export enum HomeGatewayAskImageCmd {
    AskImage = "ask_image",
}

export interface HomeGatewayAskImageChunk {
    data_base64: string;
    event:       HomeGatewayAskImageChunkEvent;
    mime_type:   string;
    offset:      number;
    request_id:  string;
    size:        number;
    [property: string]: any;
}

export enum HomeGatewayAskImageChunkEvent {
    AskImageChunk = "ask_image_chunk",
}

export interface HomeGatewayAskImageError {
    event:      HomeGatewayAskImageErrorEvent;
    message:    string;
    request_id: string;
    [property: string]: any;
}

export enum HomeGatewayAskImageErrorEvent {
    AskImageError = "ask_image_error",
}

export interface HomeGatewayAskPart {
    markdown?: string;
    question?: Question;
    [property: string]: any;
}

export interface HomeGatewayAskQuestion {
    choices:      ChoiceElement[];
    context?:     string;
    key:          string;
    kind:         HomeGatewayAskQuestionKind;
    prompt:       string;
    recommended?: string;
    [property: string]: any;
}

export interface HomeGatewayAskReply {
    answered_at: Date;
    answers:     AnswerElement[];
    note?:       string;
    [property: string]: any;
}

export interface HomeGatewayAsks {
    event: HomeGatewayAsksEvent;
    ids:   string[];
    [property: string]: any;
}

export enum HomeGatewayAsksEvent {
    Asks = "asks",
}

export interface HomeGatewayCheckTally {
    confirmed: number;
    total:     number;
    [property: string]: any;
}

export interface HomeGatewayGarden {
    event: HomeGatewayGardenEvent;
    ids:   string[];
    [property: string]: any;
}

export enum HomeGatewayGardenEvent {
    Garden = "garden",
}

export interface HomeGatewayGardenSeeds {
    event: HomeGatewayGardenSeedsEvent;
    seeds: Seed[];
    [property: string]: any;
}

export enum HomeGatewayGardenSeedsEvent {
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
    status:           HomeGatewaySeedStatus;
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

export enum HomeGatewaySeedStatus {
    Growing = "growing",
    Harvested = "harvested",
    Parked = "parked",
    Planted = "planted",
    Unknown = "unknown",
    Withered = "withered",
}

export interface HomeGatewayHello {
    api:       number;
    cmd:       HomeGatewayHelloCmd;
    device_id: string;
    [property: string]: any;
}

export enum HomeGatewayHelloCmd {
    Hello = "hello",
}

export interface HomeGatewayPing {
    cmd: HomeGatewayPingCmd;
    [property: string]: any;
}

export enum HomeGatewayPingCmd {
    Ping = "ping",
}

export interface HomeGatewayPlotProgress {
    blocked:  number;
    done:     number;
    growing:  number;
    parked:   number;
    ready:    number;
    total:    number;
    withered: number;
    [property: string]: any;
}

export interface HomeGatewayPong {
    event: HomeGatewayPongEvent;
    [property: string]: any;
}

export enum HomeGatewayPongEvent {
    Pong = "pong",
}

export interface HomeGatewayProfile {
    id:   string;
    name: string;
    [property: string]: any;
}

export interface HomeGatewayRefused {
    event:   HomeGatewayRefusedEvent;
    message: string;
    [property: string]: any;
}

export enum HomeGatewayRefusedEvent {
    Refused = "refused",
}

export interface HomeGatewaySeed {
    checks?:          Checks;
    claimed:          boolean;
    id:               string;
    plot_id?:         string;
    progress?:        Progress;
    ready:            boolean;
    reason?:          string;
    slug:             string;
    state_changed_at: Date;
    status:           HomeGatewaySeedStatus;
    tender?:          string;
    title:            string;
    waits_for:        string[];
    [property: string]: any;
}

export interface HomeGatewaySeedChanged {
    event:   HomeGatewaySeedChangedEvent;
    seed_id: string;
    [property: string]: any;
}

export enum HomeGatewaySeedChangedEvent {
    SeedChanged = "seed_changed",
}

export interface HomeGatewaySeedCheck {
    from?: string;
    id:    string;
    state: HomeGatewayCheckState;
    text:  string;
    [property: string]: any;
}

export enum HomeGatewayCheckState {
    Confirmed = "confirmed",
    Open = "open",
    Rejected = "rejected",
    Reported = "reported",
    Unknown = "unknown",
}

export interface HomeGatewaySeedDetail {
    body:                  string;
    body_too_large_bytes?: number;
    checks:                CheckElement[];
    children:              Seed[];
    event:                 HomeGatewaySeedDetailEvent;
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
    state: HomeGatewayCheckState;
    text:  string;
    [property: string]: any;
}

export enum HomeGatewaySeedDetailEvent {
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

export interface HomeGatewaySeedError {
    event:      HomeGatewaySeedErrorEvent;
    message:    string;
    request_id: string;
    [property: string]: any;
}

export enum HomeGatewaySeedErrorEvent {
    SeedError = "seed_error",
}

export interface HomeGatewaySeedGet {
    cmd:        HomeGatewaySeedGetCmd;
    request_id: string;
    seed_id:    string;
    [property: string]: any;
}

export enum HomeGatewaySeedGetCmd {
    SeedGet = "seed_get",
}

export interface HomeGatewaySeedLogEntry {
    author:     string;
    body:       string;
    created_at: Date;
    id:         string;
    kind:       string;
    [property: string]: any;
}

export interface HomeGatewaySeedMove {
    cmd:        HomeGatewaySeedMoveCmd;
    request_id: string;
    seed_id:    string;
    text:       string;
    verb:       HomeGatewaySeedVerb;
    [property: string]: any;
}

export enum HomeGatewaySeedMoveCmd {
    SeedMove = "seed_move",
}

export enum HomeGatewaySeedVerb {
    Harvest = "harvest",
    Park = "park",
    Replant = "replant",
    Unknown = "unknown",
    Wither = "wither",
}

export interface HomeGatewaySeedNoteCommand {
    body:       string;
    cmd:        HomeGatewaySeedNoteCommandCmd;
    request_id: string;
    seed_id:    string;
    [property: string]: any;
}

export enum HomeGatewaySeedNoteCommandCmd {
    SeedNote = "seed_note",
}

export interface HomeGatewaySeedResult {
    error?:     string;
    event:      HomeGatewaySeedResultEvent;
    request_id: string;
    seed_id:    string;
    [property: string]: any;
}

export enum HomeGatewaySeedResultEvent {
    SeedResult = "seed_result",
}

export interface HomeGatewaySession {
    agent:          string;
    host?:          string;
    id:             string;
    offline:        boolean;
    state:          HomeGatewaySessionState;
    terminal_id?:   string;
    title:          string;
    waiting_since?: Date;
    [property: string]: any;
}

export enum HomeGatewaySessionState {
    Idle = "idle",
    Launching = "launching",
    NeedsApproval = "needs_approval",
    Recoverable = "recoverable",
    Scheduled = "scheduled",
    Unknown = "unknown",
    Waiting = "waiting",
    Working = "working",
}

export interface HomeGatewaySessions {
    event:    HomeGatewaySessionsEvent;
    sessions: SessionElement[];
    [property: string]: any;
}

export enum HomeGatewaySessionsEvent {
    Sessions = "sessions",
}

export interface SessionElement {
    agent:          string;
    host?:          string;
    id:             string;
    offline:        boolean;
    state:          HomeGatewaySessionState;
    terminal_id?:   string;
    title:          string;
    waiting_since?: Date;
    [property: string]: any;
}

export interface HomeGatewayWelcome {
    api:       number;
    event:     HomeGatewayWelcomeEvent;
    home_name: string;
    profile:   Profile;
    [property: string]: any;
}

export enum HomeGatewayWelcomeEvent {
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
    public static toHomeGatewayAsk(json: string): HomeGatewayAsk {
        return cast(JSON.parse(json), r("HomeGatewayAsk"));
    }

    public static homeGatewayAskToJson(value: HomeGatewayAsk): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAsk")), null, 2);
    }

    public static toHomeGatewayAskAnswer(json: string): HomeGatewayAskAnswer {
        return cast(JSON.parse(json), r("HomeGatewayAskAnswer"));
    }

    public static homeGatewayAskAnswerToJson(value: HomeGatewayAskAnswer): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskAnswer")), null, 2);
    }

    public static toHomeGatewayAskAnswerCommand(json: string): HomeGatewayAskAnswerCommand {
        return cast(JSON.parse(json), r("HomeGatewayAskAnswerCommand"));
    }

    public static homeGatewayAskAnswerCommandToJson(value: HomeGatewayAskAnswerCommand): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskAnswerCommand")), null, 2);
    }

    public static toHomeGatewayAskAnswerResult(json: string): HomeGatewayAskAnswerResult {
        return cast(JSON.parse(json), r("HomeGatewayAskAnswerResult"));
    }

    public static homeGatewayAskAnswerResultToJson(value: HomeGatewayAskAnswerResult): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskAnswerResult")), null, 2);
    }

    public static toHomeGatewayAskChoice(json: string): HomeGatewayAskChoice {
        return cast(JSON.parse(json), r("HomeGatewayAskChoice"));
    }

    public static homeGatewayAskChoiceToJson(value: HomeGatewayAskChoice): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskChoice")), null, 2);
    }

    public static toHomeGatewayAskEvent(json: string): HomeGatewayAskEvent {
        return cast(JSON.parse(json), r("HomeGatewayAskEvent"));
    }

    public static homeGatewayAskEventToJson(value: HomeGatewayAskEvent): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskEvent")), null, 2);
    }

    public static toHomeGatewayAskImage(json: string): HomeGatewayAskImage {
        return cast(JSON.parse(json), r("HomeGatewayAskImage"));
    }

    public static homeGatewayAskImageToJson(value: HomeGatewayAskImage): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskImage")), null, 2);
    }

    public static toHomeGatewayAskImageChunk(json: string): HomeGatewayAskImageChunk {
        return cast(JSON.parse(json), r("HomeGatewayAskImageChunk"));
    }

    public static homeGatewayAskImageChunkToJson(value: HomeGatewayAskImageChunk): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskImageChunk")), null, 2);
    }

    public static toHomeGatewayAskImageError(json: string): HomeGatewayAskImageError {
        return cast(JSON.parse(json), r("HomeGatewayAskImageError"));
    }

    public static homeGatewayAskImageErrorToJson(value: HomeGatewayAskImageError): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskImageError")), null, 2);
    }

    public static toHomeGatewayAskPart(json: string): HomeGatewayAskPart {
        return cast(JSON.parse(json), r("HomeGatewayAskPart"));
    }

    public static homeGatewayAskPartToJson(value: HomeGatewayAskPart): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskPart")), null, 2);
    }

    public static toHomeGatewayAskQuestion(json: string): HomeGatewayAskQuestion {
        return cast(JSON.parse(json), r("HomeGatewayAskQuestion"));
    }

    public static homeGatewayAskQuestionToJson(value: HomeGatewayAskQuestion): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskQuestion")), null, 2);
    }

    public static toHomeGatewayAskQuestionKind(json: string): HomeGatewayAskQuestionKind {
        return cast(JSON.parse(json), r("HomeGatewayAskQuestionKind"));
    }

    public static homeGatewayAskQuestionKindToJson(value: HomeGatewayAskQuestionKind): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskQuestionKind")), null, 2);
    }

    public static toHomeGatewayAskReply(json: string): HomeGatewayAskReply {
        return cast(JSON.parse(json), r("HomeGatewayAskReply"));
    }

    public static homeGatewayAskReplyToJson(value: HomeGatewayAskReply): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAskReply")), null, 2);
    }

    public static toHomeGatewayAsks(json: string): HomeGatewayAsks {
        return cast(JSON.parse(json), r("HomeGatewayAsks"));
    }

    public static homeGatewayAsksToJson(value: HomeGatewayAsks): string {
        return JSON.stringify(uncast(value, r("HomeGatewayAsks")), null, 2);
    }

    public static toHomeGatewayCheckState(json: string): HomeGatewayCheckState {
        return cast(JSON.parse(json), r("HomeGatewayCheckState"));
    }

    public static homeGatewayCheckStateToJson(value: HomeGatewayCheckState): string {
        return JSON.stringify(uncast(value, r("HomeGatewayCheckState")), null, 2);
    }

    public static toHomeGatewayCheckTally(json: string): HomeGatewayCheckTally {
        return cast(JSON.parse(json), r("HomeGatewayCheckTally"));
    }

    public static homeGatewayCheckTallyToJson(value: HomeGatewayCheckTally): string {
        return JSON.stringify(uncast(value, r("HomeGatewayCheckTally")), null, 2);
    }

    public static toHomeGatewayGarden(json: string): HomeGatewayGarden {
        return cast(JSON.parse(json), r("HomeGatewayGarden"));
    }

    public static homeGatewayGardenToJson(value: HomeGatewayGarden): string {
        return JSON.stringify(uncast(value, r("HomeGatewayGarden")), null, 2);
    }

    public static toHomeGatewayGardenSeeds(json: string): HomeGatewayGardenSeeds {
        return cast(JSON.parse(json), r("HomeGatewayGardenSeeds"));
    }

    public static homeGatewayGardenSeedsToJson(value: HomeGatewayGardenSeeds): string {
        return JSON.stringify(uncast(value, r("HomeGatewayGardenSeeds")), null, 2);
    }

    public static toHomeGatewayHello(json: string): HomeGatewayHello {
        return cast(JSON.parse(json), r("HomeGatewayHello"));
    }

    public static homeGatewayHelloToJson(value: HomeGatewayHello): string {
        return JSON.stringify(uncast(value, r("HomeGatewayHello")), null, 2);
    }

    public static toHomeGatewayPing(json: string): HomeGatewayPing {
        return cast(JSON.parse(json), r("HomeGatewayPing"));
    }

    public static homeGatewayPingToJson(value: HomeGatewayPing): string {
        return JSON.stringify(uncast(value, r("HomeGatewayPing")), null, 2);
    }

    public static toHomeGatewayPlotProgress(json: string): HomeGatewayPlotProgress {
        return cast(JSON.parse(json), r("HomeGatewayPlotProgress"));
    }

    public static homeGatewayPlotProgressToJson(value: HomeGatewayPlotProgress): string {
        return JSON.stringify(uncast(value, r("HomeGatewayPlotProgress")), null, 2);
    }

    public static toHomeGatewayPong(json: string): HomeGatewayPong {
        return cast(JSON.parse(json), r("HomeGatewayPong"));
    }

    public static homeGatewayPongToJson(value: HomeGatewayPong): string {
        return JSON.stringify(uncast(value, r("HomeGatewayPong")), null, 2);
    }

    public static toHomeGatewayProfile(json: string): HomeGatewayProfile {
        return cast(JSON.parse(json), r("HomeGatewayProfile"));
    }

    public static homeGatewayProfileToJson(value: HomeGatewayProfile): string {
        return JSON.stringify(uncast(value, r("HomeGatewayProfile")), null, 2);
    }

    public static toHomeGatewayRefused(json: string): HomeGatewayRefused {
        return cast(JSON.parse(json), r("HomeGatewayRefused"));
    }

    public static homeGatewayRefusedToJson(value: HomeGatewayRefused): string {
        return JSON.stringify(uncast(value, r("HomeGatewayRefused")), null, 2);
    }

    public static toHomeGatewaySeed(json: string): HomeGatewaySeed {
        return cast(JSON.parse(json), r("HomeGatewaySeed"));
    }

    public static homeGatewaySeedToJson(value: HomeGatewaySeed): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeed")), null, 2);
    }

    public static toHomeGatewaySeedChanged(json: string): HomeGatewaySeedChanged {
        return cast(JSON.parse(json), r("HomeGatewaySeedChanged"));
    }

    public static homeGatewaySeedChangedToJson(value: HomeGatewaySeedChanged): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedChanged")), null, 2);
    }

    public static toHomeGatewaySeedCheck(json: string): HomeGatewaySeedCheck {
        return cast(JSON.parse(json), r("HomeGatewaySeedCheck"));
    }

    public static homeGatewaySeedCheckToJson(value: HomeGatewaySeedCheck): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedCheck")), null, 2);
    }

    public static toHomeGatewaySeedDetail(json: string): HomeGatewaySeedDetail {
        return cast(JSON.parse(json), r("HomeGatewaySeedDetail"));
    }

    public static homeGatewaySeedDetailToJson(value: HomeGatewaySeedDetail): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedDetail")), null, 2);
    }

    public static toHomeGatewaySeedError(json: string): HomeGatewaySeedError {
        return cast(JSON.parse(json), r("HomeGatewaySeedError"));
    }

    public static homeGatewaySeedErrorToJson(value: HomeGatewaySeedError): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedError")), null, 2);
    }

    public static toHomeGatewaySeedGet(json: string): HomeGatewaySeedGet {
        return cast(JSON.parse(json), r("HomeGatewaySeedGet"));
    }

    public static homeGatewaySeedGetToJson(value: HomeGatewaySeedGet): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedGet")), null, 2);
    }

    public static toHomeGatewaySeedLogEntry(json: string): HomeGatewaySeedLogEntry {
        return cast(JSON.parse(json), r("HomeGatewaySeedLogEntry"));
    }

    public static homeGatewaySeedLogEntryToJson(value: HomeGatewaySeedLogEntry): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedLogEntry")), null, 2);
    }

    public static toHomeGatewaySeedMove(json: string): HomeGatewaySeedMove {
        return cast(JSON.parse(json), r("HomeGatewaySeedMove"));
    }

    public static homeGatewaySeedMoveToJson(value: HomeGatewaySeedMove): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedMove")), null, 2);
    }

    public static toHomeGatewaySeedNoteCommand(json: string): HomeGatewaySeedNoteCommand {
        return cast(JSON.parse(json), r("HomeGatewaySeedNoteCommand"));
    }

    public static homeGatewaySeedNoteCommandToJson(value: HomeGatewaySeedNoteCommand): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedNoteCommand")), null, 2);
    }

    public static toHomeGatewaySeedResult(json: string): HomeGatewaySeedResult {
        return cast(JSON.parse(json), r("HomeGatewaySeedResult"));
    }

    public static homeGatewaySeedResultToJson(value: HomeGatewaySeedResult): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedResult")), null, 2);
    }

    public static toHomeGatewaySeedStatus(json: string): HomeGatewaySeedStatus {
        return cast(JSON.parse(json), r("HomeGatewaySeedStatus"));
    }

    public static homeGatewaySeedStatusToJson(value: HomeGatewaySeedStatus): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedStatus")), null, 2);
    }

    public static toHomeGatewaySeedVerb(json: string): HomeGatewaySeedVerb {
        return cast(JSON.parse(json), r("HomeGatewaySeedVerb"));
    }

    public static homeGatewaySeedVerbToJson(value: HomeGatewaySeedVerb): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySeedVerb")), null, 2);
    }

    public static toHomeGatewaySession(json: string): HomeGatewaySession {
        return cast(JSON.parse(json), r("HomeGatewaySession"));
    }

    public static homeGatewaySessionToJson(value: HomeGatewaySession): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySession")), null, 2);
    }

    public static toHomeGatewaySessionState(json: string): HomeGatewaySessionState {
        return cast(JSON.parse(json), r("HomeGatewaySessionState"));
    }

    public static homeGatewaySessionStateToJson(value: HomeGatewaySessionState): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySessionState")), null, 2);
    }

    public static toHomeGatewaySessions(json: string): HomeGatewaySessions {
        return cast(JSON.parse(json), r("HomeGatewaySessions"));
    }

    public static homeGatewaySessionsToJson(value: HomeGatewaySessions): string {
        return JSON.stringify(uncast(value, r("HomeGatewaySessions")), null, 2);
    }

    public static toHomeGatewayWelcome(json: string): HomeGatewayWelcome {
        return cast(JSON.parse(json), r("HomeGatewayWelcome"));
    }

    public static homeGatewayWelcomeToJson(value: HomeGatewayWelcome): string {
        return JSON.stringify(uncast(value, r("HomeGatewayWelcome")), null, 2);
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
    "HomeGatewayAsk": o([
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
        { json: "kind", js: "kind", typ: r("HomeGatewayAskQuestionKind") },
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
    "HomeGatewayAskAnswer": o([
        { json: "key", js: "key", typ: "" },
        { json: "other", js: "other", typ: u(undefined, "") },
        { json: "picked", js: "picked", typ: a("") },
        { json: "text", js: "text", typ: u(undefined, "") },
    ], "any"),
    "HomeGatewayAskAnswerCommand": o([
        { json: "answers", js: "answers", typ: a(r("AnswerElement")) },
        { json: "ask_id", js: "ask_id", typ: "" },
        { json: "cmd", js: "cmd", typ: r("HomeGatewayAskAnswerCommandCmd") },
        { json: "note", js: "note", typ: u(undefined, "") },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "HomeGatewayAskAnswerResult": o([
        { json: "ask_id", js: "ask_id", typ: "" },
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("HomeGatewayAskAnswerResultEvent") },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "HomeGatewayAskChoice": o([
        { json: "description", js: "description", typ: u(undefined, "") },
        { json: "label", js: "label", typ: "" },
    ], "any"),
    "HomeGatewayAskEvent": o([
        { json: "ask", js: "ask", typ: r("Ask") },
        { json: "event", js: "event", typ: r("HomeGatewayAskEventEvent") },
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
    "HomeGatewayAskImage": o([
        { json: "ask_id", js: "ask_id", typ: "" },
        { json: "cmd", js: "cmd", typ: r("HomeGatewayAskImageCmd") },
        { json: "offset", js: "offset", typ: 0 },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "src", js: "src", typ: "" },
    ], "any"),
    "HomeGatewayAskImageChunk": o([
        { json: "data_base64", js: "data_base64", typ: "" },
        { json: "event", js: "event", typ: r("HomeGatewayAskImageChunkEvent") },
        { json: "mime_type", js: "mime_type", typ: "" },
        { json: "offset", js: "offset", typ: 0 },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "size", js: "size", typ: 0 },
    ], "any"),
    "HomeGatewayAskImageError": o([
        { json: "event", js: "event", typ: r("HomeGatewayAskImageErrorEvent") },
        { json: "message", js: "message", typ: "" },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "HomeGatewayAskPart": o([
        { json: "markdown", js: "markdown", typ: u(undefined, "") },
        { json: "question", js: "question", typ: u(undefined, r("Question")) },
    ], "any"),
    "HomeGatewayAskQuestion": o([
        { json: "choices", js: "choices", typ: a(r("ChoiceElement")) },
        { json: "context", js: "context", typ: u(undefined, "") },
        { json: "key", js: "key", typ: "" },
        { json: "kind", js: "kind", typ: r("HomeGatewayAskQuestionKind") },
        { json: "prompt", js: "prompt", typ: "" },
        { json: "recommended", js: "recommended", typ: u(undefined, "") },
    ], "any"),
    "HomeGatewayAskReply": o([
        { json: "answered_at", js: "answered_at", typ: Date },
        { json: "answers", js: "answers", typ: a(r("AnswerElement")) },
        { json: "note", js: "note", typ: u(undefined, "") },
    ], "any"),
    "HomeGatewayAsks": o([
        { json: "event", js: "event", typ: r("HomeGatewayAsksEvent") },
        { json: "ids", js: "ids", typ: a("") },
    ], "any"),
    "HomeGatewayCheckTally": o([
        { json: "confirmed", js: "confirmed", typ: 0 },
        { json: "total", js: "total", typ: 0 },
    ], "any"),
    "HomeGatewayGarden": o([
        { json: "event", js: "event", typ: r("HomeGatewayGardenEvent") },
        { json: "ids", js: "ids", typ: a("") },
    ], "any"),
    "HomeGatewayGardenSeeds": o([
        { json: "event", js: "event", typ: r("HomeGatewayGardenSeedsEvent") },
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
        { json: "status", js: "status", typ: r("HomeGatewaySeedStatus") },
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
    "HomeGatewayHello": o([
        { json: "api", js: "api", typ: 0 },
        { json: "cmd", js: "cmd", typ: r("HomeGatewayHelloCmd") },
        { json: "device_id", js: "device_id", typ: "" },
    ], "any"),
    "HomeGatewayPing": o([
        { json: "cmd", js: "cmd", typ: r("HomeGatewayPingCmd") },
    ], "any"),
    "HomeGatewayPlotProgress": o([
        { json: "blocked", js: "blocked", typ: 0 },
        { json: "done", js: "done", typ: 0 },
        { json: "growing", js: "growing", typ: 0 },
        { json: "parked", js: "parked", typ: 0 },
        { json: "ready", js: "ready", typ: 0 },
        { json: "total", js: "total", typ: 0 },
        { json: "withered", js: "withered", typ: 0 },
    ], "any"),
    "HomeGatewayPong": o([
        { json: "event", js: "event", typ: r("HomeGatewayPongEvent") },
    ], "any"),
    "HomeGatewayProfile": o([
        { json: "id", js: "id", typ: "" },
        { json: "name", js: "name", typ: "" },
    ], "any"),
    "HomeGatewayRefused": o([
        { json: "event", js: "event", typ: r("HomeGatewayRefusedEvent") },
        { json: "message", js: "message", typ: "" },
    ], "any"),
    "HomeGatewaySeed": o([
        { json: "checks", js: "checks", typ: u(undefined, r("Checks")) },
        { json: "claimed", js: "claimed", typ: true },
        { json: "id", js: "id", typ: "" },
        { json: "plot_id", js: "plot_id", typ: u(undefined, "") },
        { json: "progress", js: "progress", typ: u(undefined, r("Progress")) },
        { json: "ready", js: "ready", typ: true },
        { json: "reason", js: "reason", typ: u(undefined, "") },
        { json: "slug", js: "slug", typ: "" },
        { json: "state_changed_at", js: "state_changed_at", typ: Date },
        { json: "status", js: "status", typ: r("HomeGatewaySeedStatus") },
        { json: "tender", js: "tender", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waits_for", js: "waits_for", typ: a("") },
    ], "any"),
    "HomeGatewaySeedChanged": o([
        { json: "event", js: "event", typ: r("HomeGatewaySeedChangedEvent") },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "HomeGatewaySeedCheck": o([
        { json: "from", js: "from", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "state", js: "state", typ: r("HomeGatewayCheckState") },
        { json: "text", js: "text", typ: "" },
    ], "any"),
    "HomeGatewaySeedDetail": o([
        { json: "body", js: "body", typ: "" },
        { json: "body_too_large_bytes", js: "body_too_large_bytes", typ: u(undefined, 0) },
        { json: "checks", js: "checks", typ: a(r("CheckElement")) },
        { json: "children", js: "children", typ: a(r("Seed")) },
        { json: "event", js: "event", typ: r("HomeGatewaySeedDetailEvent") },
        { json: "log", js: "log", typ: a(r("LogElement")) },
        { json: "log_total", js: "log_total", typ: 0 },
        { json: "max_frame_bytes", js: "max_frame_bytes", typ: u(undefined, 0) },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed", js: "seed", typ: r("Seed") },
    ], "any"),
    "CheckElement": o([
        { json: "from", js: "from", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "state", js: "state", typ: r("HomeGatewayCheckState") },
        { json: "text", js: "text", typ: "" },
    ], "any"),
    "LogElement": o([
        { json: "author", js: "author", typ: "" },
        { json: "body", js: "body", typ: "" },
        { json: "created_at", js: "created_at", typ: Date },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
    ], "any"),
    "HomeGatewaySeedError": o([
        { json: "event", js: "event", typ: r("HomeGatewaySeedErrorEvent") },
        { json: "message", js: "message", typ: "" },
        { json: "request_id", js: "request_id", typ: "" },
    ], "any"),
    "HomeGatewaySeedGet": o([
        { json: "cmd", js: "cmd", typ: r("HomeGatewaySeedGetCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "HomeGatewaySeedLogEntry": o([
        { json: "author", js: "author", typ: "" },
        { json: "body", js: "body", typ: "" },
        { json: "created_at", js: "created_at", typ: Date },
        { json: "id", js: "id", typ: "" },
        { json: "kind", js: "kind", typ: "" },
    ], "any"),
    "HomeGatewaySeedMove": o([
        { json: "cmd", js: "cmd", typ: r("HomeGatewaySeedMoveCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
        { json: "text", js: "text", typ: "" },
        { json: "verb", js: "verb", typ: r("HomeGatewaySeedVerb") },
    ], "any"),
    "HomeGatewaySeedNoteCommand": o([
        { json: "body", js: "body", typ: "" },
        { json: "cmd", js: "cmd", typ: r("HomeGatewaySeedNoteCommandCmd") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "HomeGatewaySeedResult": o([
        { json: "error", js: "error", typ: u(undefined, "") },
        { json: "event", js: "event", typ: r("HomeGatewaySeedResultEvent") },
        { json: "request_id", js: "request_id", typ: "" },
        { json: "seed_id", js: "seed_id", typ: "" },
    ], "any"),
    "HomeGatewaySession": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "host", js: "host", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "offline", js: "offline", typ: true },
        { json: "state", js: "state", typ: r("HomeGatewaySessionState") },
        { json: "terminal_id", js: "terminal_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waiting_since", js: "waiting_since", typ: u(undefined, Date) },
    ], "any"),
    "HomeGatewaySessions": o([
        { json: "event", js: "event", typ: r("HomeGatewaySessionsEvent") },
        { json: "sessions", js: "sessions", typ: a(r("SessionElement")) },
    ], "any"),
    "SessionElement": o([
        { json: "agent", js: "agent", typ: "" },
        { json: "host", js: "host", typ: u(undefined, "") },
        { json: "id", js: "id", typ: "" },
        { json: "offline", js: "offline", typ: true },
        { json: "state", js: "state", typ: r("HomeGatewaySessionState") },
        { json: "terminal_id", js: "terminal_id", typ: u(undefined, "") },
        { json: "title", js: "title", typ: "" },
        { json: "waiting_since", js: "waiting_since", typ: u(undefined, Date) },
    ], "any"),
    "HomeGatewayWelcome": o([
        { json: "api", js: "api", typ: 0 },
        { json: "event", js: "event", typ: r("HomeGatewayWelcomeEvent") },
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
    "HomeGatewayAskQuestionKind": [
        "any",
        "one",
        "text",
        "unknown",
    ],
    "HomeGatewayAskAnswerCommandCmd": [
        "ask_answer",
    ],
    "HomeGatewayAskAnswerResultEvent": [
        "ask_answer_result",
    ],
    "HomeGatewayAskEventEvent": [
        "ask",
    ],
    "HomeGatewayAskImageCmd": [
        "ask_image",
    ],
    "HomeGatewayAskImageChunkEvent": [
        "ask_image_chunk",
    ],
    "HomeGatewayAskImageErrorEvent": [
        "ask_image_error",
    ],
    "HomeGatewayAsksEvent": [
        "asks",
    ],
    "HomeGatewayGardenEvent": [
        "garden",
    ],
    "HomeGatewayGardenSeedsEvent": [
        "garden_seeds",
    ],
    "HomeGatewaySeedStatus": [
        "growing",
        "harvested",
        "parked",
        "planted",
        "unknown",
        "withered",
    ],
    "HomeGatewayHelloCmd": [
        "hello",
    ],
    "HomeGatewayPingCmd": [
        "ping",
    ],
    "HomeGatewayPongEvent": [
        "pong",
    ],
    "HomeGatewayRefusedEvent": [
        "refused",
    ],
    "HomeGatewaySeedChangedEvent": [
        "seed_changed",
    ],
    "HomeGatewayCheckState": [
        "confirmed",
        "open",
        "rejected",
        "reported",
        "unknown",
    ],
    "HomeGatewaySeedDetailEvent": [
        "seed_detail",
    ],
    "HomeGatewaySeedErrorEvent": [
        "seed_error",
    ],
    "HomeGatewaySeedGetCmd": [
        "seed_get",
    ],
    "HomeGatewaySeedMoveCmd": [
        "seed_move",
    ],
    "HomeGatewaySeedVerb": [
        "harvest",
        "park",
        "replant",
        "unknown",
        "wither",
    ],
    "HomeGatewaySeedNoteCommandCmd": [
        "seed_note",
    ],
    "HomeGatewaySeedResultEvent": [
        "seed_result",
    ],
    "HomeGatewaySessionState": [
        "idle",
        "launching",
        "needs_approval",
        "recoverable",
        "scheduled",
        "unknown",
        "waiting",
        "working",
    ],
    "HomeGatewaySessionsEvent": [
        "sessions",
    ],
    "HomeGatewayWelcomeEvent": [
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
