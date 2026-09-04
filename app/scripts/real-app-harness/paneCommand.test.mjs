import { execFileSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';
import { createPaneCommand } from './paneCommand.mjs';

describe('pane command completion', () => {
  it('waits through echoed input and notification-driven prompt redraws', () => {
    const command = createPaneCommand("printf 'is harvested\\n'");
    const output = execFileSync('/bin/sh', ['-c', command.text], { encoding: 'utf8' });
    const [start, result, end] = output.trim().split('\n').filter(Boolean);
    const redraws = `$ ${command.text}\nnotification\n$ ${command.text}\n`;

    expect(command.readOutput(redraws)).toBeNull();
    expect(command.readOutput(`${redraws}${start}\n${result}\n`)).toBeNull();
    expect(command.readOutput(`${redraws}${start}\n${result}\n${end}\n$`)).toBe('is harvested');
  });

  it('keeps command output separate from older output and later prompts across wrapping', () => {
    const command = createPaneCommand("printf 'current result\\n'");
    const output = execFileSync('/bin/sh', ['-c', command.text], { encoding: 'utf8' });
    const pane = `older result\n$ ${command.text}\n${output}$ prompt`;
    const wrapped = pane.replace(/(.{17})/g, '$1\n');

    expect(command.readOutput(wrapped)).toBe('current result');
  });

  it('returns empty or rejected command output without accepting a previous marker', () => {
    const previous = createPaneCommand("printf 'previous result\\n'");
    const previousOutput = execFileSync('/bin/sh', ['-c', previous.text], { encoding: 'utf8' });
    const empty = createPaneCommand('true');
    const rejected = createPaneCommand("printf 'claim refused\\n'; false");

    expect(empty.readOutput(previousOutput)).toBeNull();
    expect(empty.readOutput(execFileSync('/bin/sh', ['-c', empty.text], { encoding: 'utf8' }))).toBe('');
    expect(rejected.readOutput(execFileSync('/bin/sh', ['-c', rejected.text], { encoding: 'utf8' }))).toBe('claim refused');
  });
});
