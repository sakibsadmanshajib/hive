#!/usr/bin/env node
// PreToolUse hook: enforces commit discipline.
// - WARNS: autonomous commits (user prefers to be asked first)
// - WARNS: committing documentation files
// - WARNS: non-conventional commit messages

let input = '';
const stdinTimeout = setTimeout(() => process.exit(0), 5000);
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', () => {
  clearTimeout(stdinTimeout);
  try {
    const data = JSON.parse(input);
    const cmd = (data.tool_input || {}).command || '';

    // Only care about git commit commands
    if (!/\bgit\s+commit\b/.test(cmd)) process.exit(0);

    const warnings = [];

    // WARN: User prefers to be asked before committing
    warnings.push('COMMIT GUARD: User preference is to NEVER commit autonomously. Ensure the user explicitly asked you to commit before proceeding. If unsure, ask first.');

    // WARN: Validate conventional commit format
    const commitMsgMatch = cmd.match(/-m\s+["']([^"']+)["']/);
    if (commitMsgMatch) {
      const msg = commitMsgMatch[1];
      const conventionalPattern = /^(feat|fix|docs|chore|refactor|perf|test|build|ci|infra|BREAKING CHANGE)(\(.+\))?(!)?:\s/;
      if (!conventionalPattern.test(msg)) {
        warnings.push(`COMMIT GUARD: Commit message "${msg.substring(0, 50)}..." does not follow conventional commits format. Required: <type>(<scope>): <description>. Types: feat, fix, docs, chore, refactor, perf, test, build, ci, infra.`);
      }
    }

    if (warnings.length > 0) {
      console.log(warnings.join('\n'));
    }

  } catch (e) {
    // silent
  }
  process.exit(0);
});
