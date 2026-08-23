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
    // Cursor stamps hook stdin with hook_event_name; Claude Code never does.
    // Cursor consumes a single {permission} JSON object on stdout and ignores
    // plain text output and nonzero exit codes.
    const isCursor = typeof data.hook_event_name === 'string' && data.hook_event_name.length > 0;
    const deny = (msg) => {
      if (isCursor) {
        process.stdout.write(JSON.stringify({ permission: 'deny', agent_message: msg }));
        process.exit(0);
      }
      console.log(msg);
      process.exit(2);
    };
    const warnings = [];
    const cmd = (data.tool_input || {}).command || '';

    // BLOCK: Never kill Node processes by name
    if (/killall\s+(node|ng|ts-node)/i.test(cmd) ||
        /pkill\s+(-\w+\s+)*-?f?\s*(node|ng|ts-node|angular)/i.test(cmd)) {
      deny('BLOCKED: Never kill Node processes by name — IDEs depend on Node sub-processes. Kill by PID only: `kill <PID>`. Use `lsof -i :<port>` or `ps aux | grep <pattern>` to find the specific PID.');
    }

    // WARN: Destructive git operations
    if (/git\s+push\s+.*--force/.test(cmd) || /git\s+push\s+-f\b/.test(cmd)) {
      warnings.push('WARNING: Force push detected. This rewrites remote history and can destroy others\' work. Only proceed if the user EXPLICITLY requested force push.');
    }
    if (/git\s+reset\s+--hard/.test(cmd)) {
      warnings.push('WARNING: git reset --hard discards all uncommitted changes permanently. Only proceed if the user EXPLICITLY requested this.');
    }
    if (/--no-verify/.test(cmd)) {
      warnings.push('WARNING: --no-verify skips pre-commit hooks. Code must pass all checks. Only proceed if the user explicitly asked to skip hooks.');
    }
    if (/ECC_GATEGUARD\s*=\s*(off|0|false|disabled?)/i.test(cmd) || /ECC_DISABLED_HOOKS\s*=/.test(cmd)) {
      warnings.push('WARNING: this disables a GateGuard enforcement hook. Fulfill the gate\'s fact-forcing request and retry instead, unless the owner explicitly asked for this override.');
    }
    if (/\brm\s+(-\w*r\w*f|-\w*f\w*r)\b/.test(cmd) && !/node_modules|\.cache|dist|build|tmp/.test(cmd)) {
      warnings.push('WARNING: rm -rf on non-standard target detected. Verify this is safe and intended before proceeding.');
    }

    if (isCursor) {
      process.stdout.write(JSON.stringify(warnings.length
        ? { permission: 'allow', agent_message: warnings.join('\n') }
        : { permission: 'allow' }));
    } else if (warnings.length) {
      console.log(warnings.join('\n'));
    }

  } catch (e) {
    // silent
  }
  process.exit(0);
});
