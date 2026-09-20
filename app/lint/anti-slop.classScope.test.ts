import { describe, it } from 'vitest';
import { RuleTester } from 'oxlint/plugins-dev';
import { noUnknownTypeAliasesRule } from './anti-slop/rules/no-unknown-type-aliases';

RuleTester.describe = describe;
RuleTester.it = it;

const tester = new RuleTester({ languageOptions: { parserOptions: { lang: 'ts' } } });

tester.run('anti-slop/no-unknown-type-aliases class scope', noUnknownTypeAliasesRule, {
  valid: [
    'type Identity<T = unknown> = T; const C = class Identity { method() { type Local = Identity; } };',
    'type Identity<T = unknown> = T; function inner() { class Identity {} type Local = Identity; }',
  ],
  invalid: [{
    code: 'type Identity<T> = T; const C = class Identity {}; type Hidden = Identity<unknown>;',
    errors: [{ messageId: 'unknownAlias', data: { alias: 'Hidden' } }],
  }],
});
