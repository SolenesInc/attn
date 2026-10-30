import fs from 'node:fs';
import path from 'node:path';
import net from 'node:net';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';

const exec = promisify(execFile);
const repo = fileURLToPath(new URL('../../..', import.meta.url));
export async function outpostSsh(root) {
  const bin = path.join(root, 'ssh-bin'); fs.mkdirSync(bin);
  const binary = path.join(bin, 'ssh');
  await exec('go', ['build', '-o', binary, './scripts/testssh'], { cwd: repo });
  const routesFile = path.join(root, 'ssh-routes.json');
  const routes = {}, servers = [], children = new Set(), connections = new Set();
  const controlRoot = path.join(root, 'masters');
  const env = { PATH: `${bin}:${process.env.PATH}`, ATTN_TESTWORLD_SSH_ROUTES: routesFile, ATTN_HARNESS_CONTROL_ROOT: controlRoot, ATTN_HARNESS_DATA_DIR: root };
  const save = () => { fs.writeFileSync(`${routesFile}.next`, JSON.stringify(routes)); fs.renameSync(`${routesFile}.next`, routesFile); };
  save();
  return {
    env,
    async route(destination, remoteEnv, installedBinary) {
      const machineRoot = path.join(root, `machine-${servers.length}`); fs.mkdirSync(machineRoot);
      const installRoot = path.join(machineRoot, 'home');
      const target = path.join(installRoot, '.local/share/attn-outpost-instances', remoteEnv.ATTN_INSTANCE, 'attn');
      fs.mkdirSync(path.dirname(target), { recursive: true }); fs.copyFileSync(installedBinary, target); fs.chmodSync(target, 0o700);
      const machineBin = path.join(machineRoot, 'bin'); fs.mkdirSync(machineBin);
      for (const [name, answer] of Object.entries({ uname: 'Linux x86_64', busctl: 'b false' })) fs.writeFileSync(path.join(machineBin, name), `#!/bin/sh\necho ${answer}\n`, { mode: 0o700 });
      const socket = path.join(machineRoot, 'machine.sock');
      const server = net.createServer({ allowHalfOpen: true }, conn => {
        connections.add(conn); conn.once('close', () => connections.delete(conn));
        conn.on('error', () => {});
        let pending = Buffer.alloc(0);
        const request = data => {
          pending = Buffer.concat([pending, data]); const end = pending.indexOf(10); if (end < 0) return;
          conn.off('data', request);
          const { Script: script, ResultFile: resultFile, Lifeline: lifeline } = JSON.parse(pending.subarray(0, end));
          if (lifeline) { conn.once('end', () => conn.end()); return; }
          const child = spawn('sh', ['-c', script], { env: { ...remoteEnv, PATH: `${machineBin}:${remoteEnv.PATH}`, ATTN_HARNESS_OUTPOST_INSTALL_ROOT: installRoot }, stdio: ['pipe', 'pipe', 'pipe'] });
          children.add(child); child.once('exit', () => children.delete(child));
          let stderr = '';
          child.stderr.on('data', data => { stderr += data.toString(); });
          child.once('close', code => { fs.writeFileSync(resultFile, JSON.stringify({Code: code ?? 255, Stderr: stderr})); conn.end(); });
          child.stdin.on('error', () => {}); conn.on('error', () => {});
          child.stdout.pipe(conn, {end: false}); conn.pipe(child.stdin);
          if (pending.length > end + 1) child.stdin.write(pending.subarray(end + 1));
          conn.once('close', () => { child.stdin.end(); });
        };
        conn.on('data', request);
      });
      server.listen(socket); await once(server, 'listening'); servers.push(server);
      routes[destination] = socket; save();
      return target;
    },
    async close() {
      if (fs.existsSync(controlRoot)) for (const entry of fs.readdirSync(controlRoot)) {
        if (!entry.endsWith('.sock')) continue;
        await exec(binary, ['-o', `ControlPath=${path.join(controlRoot, entry)}`, '-O', 'exit', 'unused'], { env: { ...process.env, ...env } }).catch(() => {});
      }
      for (const child of children) { child.kill('SIGTERM'); await once(child, 'exit'); }
      for (const connection of connections) connection.destroy();
      for (const server of servers) await new Promise(resolve => server.close(resolve));
    },
  };
}
