use super::support::client;
use super::{
    Run, Stop, USAGE_UNKNOWN_COMMAND, USAGE_UNKNOWN_FLAG, print_json, print_reply_field,
    print_stdout,
};
use crate::client::SendError;
use crate::paths::os_error_text;
use attn::go::json;
use attn::go::quote::quote;
use attn::go::tabwriter;
use attn_protocol::go_values::{
    DOC_DEFAULT_LIMIT, DOC_EXPECT_ABSENT, DOC_FIRST_REV, DOC_MAX_LIMIT, DOC_OP_EQ, DOC_OP_GT,
    DOC_OP_GTE, DOC_OP_LT, DOC_OP_LTE,
};
use attn_protocol::{
    DocCollectionsMessage, DocCollectionsResult, DocCountMessage, DocCountResult, DocDefineMessage,
    DocDefineResult, DocDeleteMessage, DocDeleteResult, DocGetMessage, DocGetResult, DocPutMessage,
    DocPutResult, DocQueryMessage, DocQueryResult, DocUndefineMessage, DocUndefineResult,
    DocumentFieldSpec, DocumentFilter, DocumentQuery, StoredDocument,
};
use std::fmt::{Display, Write as _};
use std::io::Read as _;

const HELP_HEAD: &str = r#"usage: attn doc <command>

Documents are JSON objects addressed by <namespace> <collection> <id>. A
namespace is owner/name (for example example/tasks).

A collection declares which fields may be filtered and sorted on; created_at and
updated_at are always available and are never declared. Everything else in a
document body is stored and returned untouched.

A declared field is indexed, so filtering and sorting on it reads an index
rather than every document. Declaring one rewrites nothing: the index is built
from the bodies already stored.

commands:
  define <namespace> <collection> [field:type ...]
        declare a collection, replacing any previous declaration. Types are
        string, number and bool, and the type decides how stored values compare:
        a body holding "5" in a number field sorts as the number 5. Redeclaring
        is how a collection gains or loses a queryable field, and builds or drops
        that field's index; no document is rewritten.

  undefine <namespace> <collection>
        remove a collection's declaration and every document under it.

  collections [--json]
        list every declared collection, and the indexed field each one offers.

  put <namespace> <collection> <id> <body|-> [--expect <rev|absent>]
        write a document. The body is a JSON object, or - to read stdin.
        Prints the revision the document now has.

        --expect makes the write conditional. Every read reports a document's
        revision, so pass the one you read to change a document only if nobody
        has changed it since; the write is refused, naming both revisions, if
        they have. --expect absent writes only if the document does not exist
        yet. Without --expect the write always wins, which is what you want for
        a value you are setting rather than editing.

  get <namespace> <collection> <id> [--json]
  delete <namespace> <collection> <id> [--expect <rev>]
        --expect removes the document only if it is still at that revision.

  query <namespace> <collection> [query flags] [--json]
        run a query once. Reports, beside the results, the log position the
        answer was true at: put and delete print the position their write
        landed at, so comparing the two tells you whether an answer already
        includes a write you made.

  count <namespace> <collection> [query flags]
        how many documents match, without fetching them. --sort, --desc and
        --limit are ignored: they decide which matches come back, never how
        many there are.

  watch <namespace> <collection> [query flags] [--json] [--resume]
        run a live query: print the current window, then print it again every
        time a write changes it. Runs until interrupted, and exits non-zero if
        the subscription ends — a watch that has stopped watching must not look
        like a watch that finished.

        What is printed is the whole window; what travels is the order plus only
        the bodies this watcher does not already hold, which --json reports as
        "changed". A live query takes no --after: it is a window, not a walk.

        --resume resubscribes when the connection goes — a daemon restart, for
        instance — waiting for the daemon to come back and declaring what it
        holds, so only what changed while it was away comes back. A collection
        that is removed or redeclared without a field the query uses ends the
        watch either way, and so does a daemon that was never there.

query flags:
  --where <expr>    repeatable. field=value, or field<value, field<=value,
                    field>value, field>=value. The value is read as JSON when it
                    parses as one, and as a string otherwise, so --where
                    status=pending sends "pending" and --where attempts>=5 sends 5.
  --sort <field>    order by a declared field, or created_at / updated_at.
  --desc            reverse the sort.
  --limit <n>       at most n documents (default "#;

const HELP_TAIL: &str = r").
  --after <id>      the next page: documents that come after this one in the
                    same order. Pass the id of the last document of the previous
                    page. Use this rather than a --where on the sort field —
                    documents sharing a sort value would otherwise be skipped or
                    repeated.
";
const WHERE_OPS: [(&str, &str); 5] = [
    (">=", DOC_OP_GTE),
    ("<=", DOC_OP_LTE),
    ("=", DOC_OP_EQ),
    (">", DOC_OP_GT),
    ("<", DOC_OP_LT),
];

fn help() -> String {
    format!("{HELP_HEAD}{DOC_DEFAULT_LIMIT}, maximum {DOC_MAX_LIMIT}{HELP_TAIL}")
}

fn fail(verb: &str, message: impl Display) -> Stop {
    eprintln!("doc {verb}: {message}");
    Stop(1)
}

fn fail_send(run: &mut Run<'_>, verb: &str, error: &SendError) -> Stop {
    run.refusal.clone_from(&error.refusal);
    fail(verb, error)
}

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let Some(verb) = args
        .first()
        .filter(|verb| !matches!(verb.as_str(), "-h" | "--help"))
    else {
        print_stdout(&help());
        return Ok(());
    };
    let verb = verb.as_str();
    let action: fn(&mut Run<'_>, &[String]) -> Result<(), Stop> = match verb {
        "define" => define,
        "undefine" => undefine,
        "collections" => collections,
        "put" => put,
        "get" => get,
        "delete" => delete,
        "query" => query,
        "count" => count,
        _ => {
            run.usage_error = Some(USAGE_UNKNOWN_COMMAND);
            eprint!(
                "doc: unknown command {}\n{}",
                quote(verb.as_bytes()),
                help()
            );
            return Err(Stop(2));
        }
    };
    run.path = format!("doc {verb}");
    action(run, &args[1..])
}

fn target<'a>(verb: &str, args: &'a [String]) -> Result<(&'a str, &'a str, &'a [String]), Stop> {
    if args.len() < 2 {
        eprintln!("doc {verb}: needs <namespace> <collection>");
        return Err(Stop(2));
    }
    Ok((&args[0], &args[1], &args[2..]))
}

fn define(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, rest) = target("define", args)?;
    let mut fields = Vec::with_capacity(rest.len());
    for spec in rest {
        let Some((name, kind)) = spec.split_once(':') else {
            return Err(fail(
                "define",
                format!(
                    "field {} is not name:type (types: string, number, bool)",
                    quote(spec.as_bytes())
                ),
            ));
        };
        fields.push(DocumentFieldSpec {
            name: name.to_string(),
            r#type: kind.to_string(),
        });
    }
    let declared = fields.len();
    let mut message = DocDefineMessage::new();
    message.schema.namespace = namespace.to_string();
    message.schema.collection = collection.to_string();
    message.schema.fields = fields;
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "define", &error))?;
    let result: DocDefineResult = reply
        .value("doc_define_result")
        .map_err(|error| fail_send(run, "define", &error))?;
    print_stdout(&format!(
        "declared {}/{} with {declared} queryable field(s)\n",
        result.namespace, result.collection
    ));
    Ok(())
}

fn undefine(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, _) = target("undefine", args)?;
    let mut message = DocUndefineMessage::new();
    message.namespace = namespace.to_string();
    message.collection = collection.to_string();
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "undefine", &error))?;
    let result: DocUndefineResult = reply
        .value("doc_undefine_result")
        .map_err(|error| fail_send(run, "undefine", &error))?;
    print_stdout(&format!(
        "removed {}/{} and {} document(s)\n",
        result.namespace, result.collection, result.documents_removed
    ));
    Ok(())
}

fn collections(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let reply = client(run)
        .call(&DocCollectionsMessage::new())
        .map_err(|error| fail_send(run, "collections", &error))?;
    if args.iter().any(|arg| arg == "--json") {
        return print_reply_field(&reply, &["doc_collections_result", "collections"])
            .map_err(|error| fail_send(run, "collections", &error));
    }
    let result: DocCollectionsResult = reply
        .value("doc_collections_result")
        .map_err(|error| fail_send(run, "collections", &error))?;
    if result.collections.is_empty() {
        print_stdout("no collections declared\n");
        return Ok(());
    }
    let mut table = String::from("NAMESPACE\tCOLLECTION\tINDEXED FIELDS\n");
    for collection in &result.collections {
        let names: Vec<String> = collection
            .fields
            .iter()
            .map(|field| format!("{}:{}", field.name, field.r#type))
            .collect();
        let names = if names.is_empty() {
            "-".to_string()
        } else {
            names.join(" ")
        };
        let _ = writeln!(
            table,
            "{}\t{}\t{names}",
            collection.namespace, collection.collection
        );
    }
    print_stdout(&tabwriter::format(&table));
    Ok(())
}

fn put(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, rest) = target("put", args)?;
    let (rest, expected_rev) = take_expect("put", rest, true)?;
    let [id, body, ..] = rest.as_slice() else {
        return Err(fail("put", "needs <id> and a JSON body (or - for stdin)"));
    };
    let body = if body == "-" {
        let mut raw = Vec::new();
        std::io::stdin()
            .read_to_end(&mut raw)
            .map_err(|error| fail("put", format!("reading stdin: {}", os_error_text(&error))))?;
        String::from_utf8_lossy(&raw).into_owned()
    } else {
        body.clone()
    };
    let mut message = DocPutMessage::new();
    message.namespace = namespace.to_string();
    message.collection = collection.to_string();
    message.id.clone_from(id);
    message.body = body;
    message.expected_rev = expected_rev;
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "put", &error))?;
    let result: DocPutResult = reply
        .value("doc_put_result")
        .map_err(|error| fail_send(run, "put", &error))?;
    print_stdout(&format!(
        "wrote {namespace}/{collection}/{id} (rev {}, seq {})\n",
        result.rev, result.seq
    ));
    Ok(())
}

fn expect_absent_hint(allow_absent: bool) -> &'static str {
    if allow_absent {
        r#" (a number from a previous read, or "absent")"#
    } else {
        " (a number from a previous read)"
    }
}

fn take_expect(
    verb: &str,
    args: &[String],
    allow_absent: bool,
) -> Result<(Vec<String>, Option<i64>), Stop> {
    let mut rest = Vec::with_capacity(args.len());
    let mut expect = None;
    let mut arguments = args.iter();
    while let Some(argument) = arguments.next() {
        if argument != "--expect" {
            rest.push(argument.clone());
            continue;
        }
        let Some(value) = arguments.next() else {
            return Err(fail(
                verb,
                format!(
                    "--expect needs a revision{}",
                    expect_absent_hint(allow_absent)
                ),
            ));
        };
        if value == "absent" {
            if !allow_absent {
                return Err(fail(
                    verb,
                    format!(
                        "--expect absent asks to act on a document that is not there, which {verb} cannot do"
                    ),
                ));
            }
            expect = Some(DOC_EXPECT_ABSENT);
            continue;
        }
        match value.parse::<i64>() {
            Ok(rev) if rev >= DOC_FIRST_REV => expect = Some(rev),
            _ => {
                return Err(fail(
                    verb,
                    format!(
                        "--expect {} is not a revision{}",
                        quote(value.as_bytes()),
                        expect_absent_hint(allow_absent)
                    ),
                ));
            }
        }
    }
    Ok((rest, expect))
}

fn get(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, rest) = target("get", args)?;
    let Some(id) = rest.first() else {
        return Err(fail("get", "needs <id>"));
    };
    let mut message = DocGetMessage::new();
    message.namespace = namespace.to_string();
    message.collection = collection.to_string();
    message.id.clone_from(id);
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "get", &error))?;
    let result: DocGetResult = reply
        .value("doc_get_result")
        .map_err(|error| fail_send(run, "get", &error))?;
    let document = result.document.filter(|_| result.found);
    let Some(document) = document else {
        return Err(fail(
            "get",
            format!("{namespace}/{collection}/{id} does not exist"),
        ));
    };
    if rest.iter().any(|arg| arg == "--json") {
        print_json(document_json(&document).as_bytes());
        return Ok(());
    }
    print_stdout(&format!("{}\n", document.body));
    eprintln!("as of seq {}", result.as_of_seq);
    Ok(())
}

fn delete(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, rest) = target("delete", args)?;
    let (rest, expected_rev) = take_expect("delete", rest, false)?;
    let Some(id) = rest.first() else {
        return Err(fail("delete", "needs <id>"));
    };
    let mut message = DocDeleteMessage::new();
    message.namespace = namespace.to_string();
    message.collection = collection.to_string();
    message.id.clone_from(id);
    message.expected_rev = expected_rev;
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "delete", &error))?;
    let result: DocDeleteResult = reply
        .value("doc_delete_result")
        .map_err(|error| fail_send(run, "delete", &error))?;
    if result.existed {
        print_stdout(&format!(
            "deleted {namespace}/{collection}/{id} (seq {})\n",
            result.seq
        ));
    } else {
        print_stdout(&format!("{namespace}/{collection}/{id} did not exist\n"));
    }
    Ok(())
}

fn query(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, rest) = target("query", args)?;
    let (document_query, as_json) = parse_query_flags(run, "query", namespace, collection, rest)?;
    let mut message = DocQueryMessage::new();
    message.query = document_query;
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "query", &error))?;
    let result: DocQueryResult = reply
        .value("doc_query_result")
        .map_err(|error| fail_send(run, "query", &error))?;
    if as_json {
        let items: Vec<String> = result.documents.iter().map(document_json).collect();
        print_json(format!("[{}]", items.join(",")).as_bytes());
        return Ok(());
    }
    print_document_table(&result.documents);
    eprintln!("as of seq {}", result.as_of_seq);
    Ok(())
}

fn count(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    let (namespace, collection, rest) = target("count", args)?;
    let (document_query, as_json) = parse_query_flags(run, "count", namespace, collection, rest)?;
    let mut message = DocCountMessage::new();
    message.query = document_query;
    let reply = client(run)
        .call(&message)
        .map_err(|error| fail_send(run, "count", &error))?;
    let result: DocCountResult = reply
        .value("doc_count_result")
        .map_err(|error| fail_send(run, "count", &error))?;
    if as_json {
        print_json(
            format!(
                "{{\"count\":{},\"as_of_seq\":{}}}",
                result.count, result.as_of_seq
            )
            .as_bytes(),
        );
    } else {
        print_stdout(&format!("{}\n", result.count));
    }
    Ok(())
}

fn parse_query_flags(
    run: &mut Run<'_>,
    verb: &str,
    namespace: &str,
    collection: &str,
    args: &[String],
) -> Result<(DocumentQuery, bool), Stop> {
    let mut query = DocumentQuery {
        namespace: namespace.to_string(),
        collection: collection.to_string(),
        ..DocumentQuery::default()
    };
    let mut as_json = false;
    let mut arguments = args.iter();
    while let Some(flag) = arguments.next() {
        let mut value = || {
            arguments
                .next()
                .ok_or_else(|| fail(verb, format!("{flag} needs a value")))
        };
        match flag.as_str() {
            "--json" => as_json = true,
            "--resume" => {
                return Err(fail(
                    verb,
                    "--resume is only for watch; a one-shot read has nothing to resume",
                ));
            }
            "--desc" => query.sort.get_or_insert_default().desc = Some(true),
            "--sort" => query
                .sort
                .get_or_insert_default()
                .field
                .clone_from(value()?),
            "--after" => query.after = Some(value()?.clone()),
            "--limit" => {
                let raw = value()?;
                let limit = raw.parse::<i64>().map_err(|_| {
                    fail(
                        verb,
                        format!("--limit {} is not a number", quote(raw.as_bytes())),
                    )
                })?;
                query.limit = Some(limit);
            }
            "--where" => {
                let filter = parse_where(value()?).map_err(|message| fail(verb, message))?;
                query.filters.get_or_insert_default().push(filter);
            }
            _ => {
                run.usage_error = Some(USAGE_UNKNOWN_FLAG);
                return Err(fail(
                    verb,
                    format!("unknown flag {}", quote(flag.as_bytes())),
                ));
            }
        }
    }
    if query
        .sort
        .as_ref()
        .is_some_and(|sort| sort.field.is_empty())
    {
        return Err(fail(verb, "--desc needs --sort <field>"));
    }
    Ok((query, as_json))
}

fn parse_where(expression: &str) -> Result<DocumentFilter, String> {
    for (token, op) in WHERE_OPS {
        let Some((field, value)) = expression.split_once(token) else {
            continue;
        };
        if field.is_empty() {
            return Err(format!(
                "--where {} has no field name",
                quote(expression.as_bytes())
            ));
        }
        return Ok(DocumentFilter {
            field: field.to_string(),
            op: op.to_string(),
            value_json: bound_as_json(value),
        });
    }
    Err(format!(
        "--where {} needs one of = < <= > >= (for example status=pending)",
        quote(expression.as_bytes())
    ))
}

fn bound_as_json(raw: &str) -> String {
    if serde_json::from_str::<serde_json::Value>(raw).is_ok() {
        return raw.to_string();
    }
    encoded_string(raw)
}

fn encoded_string(value: &str) -> String {
    String::from_utf8(json::encode(value).unwrap_or_default()).unwrap_or_default()
}

fn document_json(document: &StoredDocument) -> String {
    format!(
        "{{\"id\":{},\"body\":{},\"rev\":{},\"created_at\":{},\"updated_at\":{}}}",
        encoded_string(&document.id),
        json::compact_escaped(&document.body),
        document.rev,
        encoded_string(&document.created_at),
        encoded_string(&document.updated_at)
    )
}

fn print_document_table(documents: &[StoredDocument]) {
    if documents.is_empty() {
        print_stdout("no documents\n");
        return;
    }
    let mut table = String::from("ID\tREV\tUPDATED\tBODY\n");
    for document in documents {
        let _ = writeln!(
            table,
            "{}\t{}\t{}\t{}",
            document.id, document.rev, document.updated_at, document.body
        );
    }
    print_stdout(&tabwriter::format(&table));
}
