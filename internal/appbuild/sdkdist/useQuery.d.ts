import type { Document, Filter } from "./index";
/** What a live query takes. No `after`: a live query is a window, a cursor is a walk. */
export interface LiveQueryOptions {
    filters?: Filter[];
    sort?: {
        field: string;
        desc?: boolean;
    };
    /** Defaults to the store's own 100, and refuses more than 1000. */
    limit?: number;
}
export interface QueryError {
    code: string;
    message: string;
}
export interface QueryResult<Body> {
    /** The window, in the server's order. */
    docs: Array<Document<Body>>;
    /** The log position this window was true at. Opaque and monotonic. */
    asOfSeq: number;
    /** Whether the daemon is serving this query right now. */
    live: boolean;
    /** Set when the subscription ended and will not resume: a state to render. */
    error: QueryError | null;
}
export declare function useQuery<Body = unknown>(collection: string, options?: LiveQueryOptions): QueryResult<Body>;
