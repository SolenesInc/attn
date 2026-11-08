import { readdirSync, readFileSync, writeFileSync } from 'node:fs';

const schemaDirectory = 'internal/protocol/schema/tsp-output/json-schema';
const versionFile = 'internal/protocol/protocol_version.go';
const outputFile = 'cli/protocol/src/generated.rs';

const reservedWords = new Set([
  'abstract', 'as', 'async', 'await', 'become', 'box', 'break', 'const', 'continue', 'do', 'dyn', 'else', 'enum',
  'extern', 'false', 'final', 'fn', 'for', 'gen', 'if', 'impl', 'in', 'let', 'loop', 'macro', 'match', 'mod', 'move',
  'mut', 'override', 'priv', 'pub', 'ref', 'return', 'static', 'struct', 'trait', 'true', 'try', 'type', 'typeof',
  'unsafe', 'unsized', 'use', 'virtual', 'where', 'while', 'yield',
]);
const unusableFieldNames = new Set(['crate', 'self', 'Self', 'super', '_']);

const compare = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
const modelName = reference => reference.replace(/\.json$/, '');
const isModelReference = schema => typeof schema.$ref === 'string' && !schema.$ref.startsWith('#');

const files = readdirSync(schemaDirectory).filter(file => file.endsWith('.json')).sort(compare);
if (files.length === 0) throw new Error(`No protocol schemas found in ${schemaDirectory}; compile TypeSpec first.`);
const models = new Map(files.map(file => [modelName(file), JSON.parse(readFileSync(`${schemaDirectory}/${file}`, 'utf8'))]));

function fieldsOf(name, schema) {
  const inherited = (schema.allOf ?? []).map(parent => {
    if (!isModelReference(parent)) throw new Error(`${name}: allOf supports only model references`);
    return fieldsOf(modelName(parent.$ref), models.get(modelName(parent.$ref)));
  });
  return {
    properties: Object.assign({}, ...inherited.map(fields => fields.properties), schema.properties),
    required: new Set([...inherited.flatMap(fields => [...fields.required]), ...(schema.required ?? [])]),
  };
}

const isStruct = schema => schema.type === 'object';
const structs = new Map([...models].filter(([, schema]) => isStruct(schema)).map(([name, schema]) => [name, fieldsOf(name, schema)]));

const directReferences = new Map([...structs].map(([name, { properties }]) => [
  name,
  Object.values(properties).filter(isModelReference).map(property => modelName(property.$ref)),
]));

function isInCycle(from, target, seen = new Set()) {
  if (from === target) return true;
  if (seen.has(from)) return false;
  seen.add(from);
  return (directReferences.get(from) ?? []).some(next => isInCycle(next, target, seen));
}

const isStringAlternatives = alternatives => alternatives.every(alternative => alternative.type === 'string');

function rustType(schema, model, field) {
  const where = `${model}.${field}`;
  if (schema.$ref?.startsWith('#/$defs/')) return rustType(models.get(model).$defs[schema.$ref.slice('#/$defs/'.length)], model, field);
  if (isModelReference(schema)) return modelName(schema.$ref);
  if (schema.anyOf) {
    if (isStringAlternatives(schema.anyOf)) return 'String';
    throw new Error(`${where}: anyOf is supported only over strings; got ${JSON.stringify(schema.anyOf)}`);
  }
  switch (schema.type) {
    case 'string': return 'String';
    case 'boolean': return 'bool';
    case 'integer': return 'i64';
    case 'number': return 'f64';
    case 'array': return `Vec<${rustType(schema.items, model, field)}>`;
    case 'object': {
      const values = schema.additionalProperties ?? schema.unevaluatedProperties;
      if (values === undefined) throw new Error(`${where}: an object field needs a model or a value type`);
      const isUnknown = Object.keys(values).length === 0;
      return `BTreeMap<String, ${isUnknown ? 'serde_json::Value' : rustType(values, model, field)}>`;
    }
    default: throw new Error(`${where}: unsupported schema ${JSON.stringify(schema)}`);
  }
}

const fieldName = name => {
  if (unusableFieldNames.has(name) || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) throw new Error(`Field ${JSON.stringify(name)} cannot be a Rust identifier`);
  return reservedWords.has(name) ? `r#${name}` : name;
};

function renderField(model, name, schema, required) {
  let type = rustType(schema, model, name);
  if (isModelReference(schema) && isInCycle(modelName(schema.$ref), model)) type = `Box<${type}>`;
  const field = `    pub ${fieldName(name)}:`;
  if (!required) return `    #[serde(default, skip_serializing_if = "Option::is_none")]\n${field} Option<${type}>,`;
  if (type.startsWith('Vec<') || type.startsWith('BTreeMap<')) return `    #[serde(deserialize_with = "null_as_default")]\n${field} ${type},`;
  return `${field} ${type},`;
}

function renderStruct(name, { properties, required }) {
  const fieldNames = Object.keys(properties).sort(compare);
  const fields = fieldNames.map(field => renderField(name, field, properties[field], required.has(field)));
  const constants = fieldNames.filter(field => required.has(field) && properties[field].const !== undefined);
  const lines = ['#[derive(Debug, Clone, Default, Serialize, Deserialize)]', `pub struct ${name} {`, ...fields, '}'];
  if (constants.length > 0) {
    const assignments = constants.map(field => `            ${fieldName(field)}: ${JSON.stringify(properties[field].const)}.to_string(),`);
    lines.push('', `impl ${name} {`, '    #[must_use]', '    pub fn new() -> Self {', '        Self {', ...assignments, '            ..Self::default()', '        }', '    }', '}');
  }
  return lines.join('\n');
}

function responseFields() {
  const counts = new Map();
  const results = Object.entries(models.get('Response').properties).filter(([field, schema]) => field.endsWith('_result') && isModelReference(schema));
  for (const [, schema] of results) counts.set(schema.$ref, (counts.get(schema.$ref) ?? 0) + 1);
  return results.filter(([, schema]) => counts.get(schema.$ref) === 1).sort(([a], [b]) => compare(a, b));
}

const version = /ProtocolVersion = "([0-9a-f]+)"/.exec(readFileSync(versionFile, 'utf8'))?.[1];
if (!version) throw new Error(`No ProtocolVersion in ${versionFile}; run scripts/generate-protocol-version.mjs first.`);

const header = `// Code generated by make generate-types. DO NOT EDIT.

#![allow(non_camel_case_types, non_snake_case, clippy::all, clippy::pedantic)]

use serde::{Deserialize, Deserializer, Serialize};
use std::collections::BTreeMap;

pub const PROTOCOL_VERSION: &str = ${JSON.stringify(version)};

pub trait ResponseField {
    const FIELD: &'static str;
}

fn null_as_default<'de, D: Deserializer<'de>, T: Default + Deserialize<'de>>(deserializer: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}
`;

const items = [...models].map(([name, schema]) => {
  if (isStruct(schema)) return renderStruct(name, structs.get(name));
  if (schema.type === 'string') return `pub type ${name} = String;`;
  throw new Error(`${name}: unsupported top-level schema`);
});
const impls = responseFields().map(([field, schema]) => `impl ResponseField for ${modelName(schema.$ref)} {\n    const FIELD: &'static str = ${JSON.stringify(field)};\n}`);

writeFileSync(outputFile, [header, ...items, ...impls].join('\n') + '\n');
