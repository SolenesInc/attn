// To parse this data:
//
//   import { Convert, GatewayHello, GatewayProfile, GatewayRefused, GatewaySession, GatewaySessionState, GatewaySessions, GatewayWelcome } from "./gatewayGenerated";
//
//   const gatewayHello = Convert.toGatewayHello(json);
//   const gatewayProfile = Convert.toGatewayProfile(json);
//   const gatewayRefused = Convert.toGatewayRefused(json);
//   const gatewaySession = Convert.toGatewaySession(json);
//   const gatewaySessionState = Convert.toGatewaySessionState(json);
//   const gatewaySessions = Convert.toGatewaySessions(json);
//   const gatewayWelcome = Convert.toGatewayWelcome(json);
//
// These functions will throw an error if the JSON doesn't
// match the expected interface, even if the JSON is valid.

export interface GatewayHello {
    api:       number;
    cmd:       Cmd;
    device_id: string;
    [property: string]: any;
}

export enum Cmd {
    Hello = "hello",
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

// Converts JSON strings to/from your types
// and asserts the results of JSON.parse at runtime
export class Convert {
    public static toGatewayHello(json: string): GatewayHello {
        return cast(JSON.parse(json), r("GatewayHello"));
    }

    public static gatewayHelloToJson(value: GatewayHello): string {
        return JSON.stringify(uncast(value, r("GatewayHello")), null, 2);
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
    "GatewayHello": o([
        { json: "api", js: "api", typ: 0 },
        { json: "cmd", js: "cmd", typ: r("Cmd") },
        { json: "device_id", js: "device_id", typ: "" },
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
    "Cmd": [
        "hello",
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
};
