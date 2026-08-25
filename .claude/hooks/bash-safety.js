#!/usr/bin/env node
// PreToolUse hook: enforces safety rules for Bash commands.
// - BLOCKS: killing Node processes by name (killall/pkill)
// - WARNS: force push, hard reset, --no-verify, rm -rf

let input = '';
const stdinTimeout = setTimeout(() => process.exit(0), 5000);
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', () => {
  clearTimeout(stdinTimeout);
  try {
    const data = JSON.parse(input);
    const cmd = (data.tool_input || {}).command || data.command || '';

    // Match against the command with quoted string literals blanked out, so a
    // read-only command that merely mentions a dangerous pattern as TEXT (a
    // grep/rg search, an echo, a --grep filter) is not treated as running it.
    // A real invocation of killall/pkill/rm/git never needs its own name or
    // flags quoted, so this cannot hide an actual dangerous command.
    const stripQuoted = (s) => s
      .replace(/"(?:[^"\\]|\\.)*"/g, '""')
      .replace(/'(?:[^'\\]|\\.)*'/g, "''");
    const scan = stripQuoted(cmd);

    // BLOCK: Never kill Node processes by name
    if (/killall\s+(node|ng|ts-node)/i.test(scan) ||
        /pkill\s+(-\w+\s+)*-?f?\s*(node|ng|ts-node|angular)/i.test(scan)) {
      console.log('BLOCKED: Never kill Node processes by name — IDEs depend on Node sub-processes. Kill by PID only: `kill <PID>`. Use `lsof -i :<port>` or `ps aux | grep <pattern>` to find the specific PID.');
      process.exit(2);
    }

    // WARN: Destructive git operations
    if (/git\s+push\s+.*--force/.test(scan) || /git\s+push\s+-f\b/.test(scan)) {
      console.log('WARNING: Force push detected. This rewrites remote history and can destroy others\' work. Only proceed if the user EXPLICITLY requested force push.');
    }
    if (/git\s+reset\s+--hard/.test(scan)) {
      console.log('WARNING: git reset --hard discards all uncommitted changes permanently. Only proceed if the user EXPLICITLY requested this.');
    }
    if (/--no-verify/.test(scan)) {
      console.log('WARNING: --no-verify skips pre-commit hooks. Code must pass all checks. Only proceed if the user explicitly asked to skip hooks.');
    }
    if (/ECC_GATEGUARD\s*=\s*(off|0|false|disabled?)/i.test(scan) || /ECC_DISABLED_HOOKS\s*=/.test(scan)) {
      console.log('WARNING: this disables a GateGuard enforcement hook. Fulfill the gate\'s fact-forcing request and retry instead, unless the owner explicitly asked for this override.');
    }
    if (/\brm\s+(-\w*r\w*f|-\w*f\w*r)\b/.test(scan) && !/node_modules|\.cache|dist|build|tmp/.test(scan)) {
      console.log('WARNING: rm -rf on non-standard target detected. Verify this is safe and intended before proceeding.');
    }

  } catch (e) {
    // silent
  }
  process.exit(0);
});
