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
    const cmd = (data.tool_input || {}).command || '';

    // BLOCK: Never kill Node processes by name
    if (/killall\s+(node|ng|ts-node)/i.test(cmd) ||
        /pkill\s+(-\w+\s+)*-?f?\s*(node|ng|ts-node|angular)/i.test(cmd)) {
      console.log('BLOCKED: Never kill Node processes by name — IDEs depend on Node sub-processes. Kill by PID only: `kill <PID>`. Use `lsof -i :<port>` or `ps aux | grep <pattern>` to find the specific PID.');
      process.exit(2);
    }

    // WARN: Destructive git operations
    if (/git\s+push\s+.*--force/.test(cmd) || /git\s+push\s+-f\b/.test(cmd)) {
      console.log('WARNING: Force push detected. This rewrites remote history and can destroy others\' work. Only proceed if the user EXPLICITLY requested force push.');
    }
    if (/git\s+reset\s+--hard/.test(cmd)) {
      console.log('WARNING: git reset --hard discards all uncommitted changes permanently. Only proceed if the user EXPLICITLY requested this.');
    }
    if (/--no-verify/.test(cmd)) {
      console.log('WARNING: --no-verify skips pre-commit hooks. Code must pass all checks. Only proceed if the user explicitly asked to skip hooks.');
    }
    if (/\brm\s+(-\w*r\w*f|-\w*f\w*r)\b/.test(cmd) && !/node_modules|\.cache|dist|build|tmp/.test(cmd)) {
      console.log('WARNING: rm -rf on non-standard target detected. Verify this is safe and intended before proceeding.');
    }

  } catch (e) {
    // silent
  }
  process.exit(0);
});
