// To parse this data:
//
//   import { Convert, GatewayAsk, GatewayAskAnswer, GatewayAskAnswerCommand, GatewayAskAnswerResult, GatewayAskChoice, GatewayAskEvent, GatewayAskImage, GatewayAskImageChunk, GatewayAskImageError, GatewayAskPart, GatewayAskQuestion, GatewayAskQuestionKind, GatewayAskReply, GatewayAsks, GatewayHello, GatewayPing, GatewayPong, GatewayProfile, GatewayRefused, GatewaySession, GatewaySessionState, GatewaySessions, GatewayWelcome, SubmitPrompt, SubmitResult, TerminalAttach, TerminalCredit, TerminalDetach, TerminalEnded, TerminalError, TerminalFocus, TerminalInput, TerminalResize, TerminalResized, TerminalScreen } from "./gatewayGenerated";
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
//   const gatewayHello = Convert.toGatewayHello(json);
//   const gatewayPing = Convert.toGatewayPing(json);
//   const gatewayPong = Convert.toGatewayPong(json);
//   const gatewayProfile = Convert.toGatewayProfile(json);
//   const gatewayRefused = Convert.toGatewayRefused(json);
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
    "GatewayHello": o([
        { json: "api", js: "api", typ: 0 },
        { json: "cmd", js: "cmd", typ: r("GatewayHelloCmd") },
        { json: "device_id", js: "device_id", typ: "" },
    ], "any"),
    "GatewayPing": o([
        { json: "cmd", js: "cmd", typ: r("GatewayPingCmd") },
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
