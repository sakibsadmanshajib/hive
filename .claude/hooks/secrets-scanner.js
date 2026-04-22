#!/usr/bin/env node
// PreToolUse hook: blocks hardcoded secrets, API keys, and credentials.
// - BLOCKS: high-confidence secret patterns (known key prefixes, private keys, AWS keys)
// - WARNS: lower-confidence patterns (generic api_key, secret, token assignments)
// - SKIPS: .env.example, test files, markdown files

const path = require('path');

let input = '';
const stdinTimeout = setTimeout(() => process.exit(0), 5000);
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => input += chunk);
process.stdin.on('end', () => {
  clearTimeout(stdinTimeout);
  try {
    const data = JSON.parse(input);
    const filePath = (data.tool_input || {}).file_path || '';
    const content = (data.tool_input || {}).content || (data.tool_input || {}).new_string || '';

    if (!content || !filePath) process.exit(0);

    const basename = path.basename(filePath);

    // Skip files that legitimately contain secret-like patterns
    if (basename === '.env.example' || basename === '.env.sample') process.exit(0);
    if (/\.(test|spec)\.(ts|js|tsx|jsx|go|py)$/.test(basename)) process.exit(0);
    if (basename.endsWith('.md')) process.exit(0);

    // HIGH-CONFIDENCE: BLOCK — known API key prefixes and private keys
    const blockPatterns = [
      { pattern: /sk-proj-[a-zA-Z0-9]{20,}/, label: 'OpenAI API key' },
      { pattern: /sk-ant-[a-zA-Z0-9]{20,}/, label: 'Anthropic API key' },
      { pattern: /sk-or-[a-zA-Z0-9]{20,}/, label: 'OpenRouter API key' },
      { pattern: /gsk_[a-zA-Z0-9]{20,}/, label: 'Groq API key' },
      { pattern: /ghp_[a-zA-Z0-9]{36,}/, label: 'GitHub personal access token' },
      { pattern: /gho_[a-zA-Z0-9]{36,}/, label: 'GitHub OAuth token' },
      { pattern: /github_pat_[a-zA-Z0-9]{22,}/, label: 'GitHub fine-grained token' },
      { pattern: /-----BEGIN (RSA |EC |DSA )?PRIVATE KEY-----/, label: 'Private key' },
      { pattern: /AKIA[0-9A-Z]{16}/, label: 'AWS access key' },
      { pattern: /eyJ[a-zA-Z0-9_-]{20,}\.eyJ[a-zA-Z0-9_-]{20,}\.[a-zA-Z0-9_-]{20,}/, label: 'JWT token (possible Supabase service role key)' },
      { pattern: /password\s*[:=]\s*["'][^"']{8,}["']/, label: 'Hardcoded password' },
    ];

    for (const { pattern, label } of blockPatterns) {
      if (pattern.test(content)) {
        console.log(`BLOCKED: Detected ${label} in ${basename}. Never commit secrets to the repository. Use environment variables or a secret manager.`);
        process.exit(2);
      }
    }

    // LOW-CONFIDENCE: WARN — generic patterns
    const warnPatterns = [
      { pattern: /api[_-]?key\s*[:=]\s*["'][^"']{8,}["']/i, label: 'Possible API key assignment' },
      { pattern: /secret\s*[:=]\s*["'][^"']{8,}["']/i, label: 'Possible secret assignment' },
      { pattern: /token\s*[:=]\s*["'][^"']{8,}["']/i, label: 'Possible token assignment' },
    ];

    const warns = [];
    for (const { pattern, label } of warnPatterns) {
      if (pattern.test(content)) {
        warns.push(label);
      }
    }

    if (warns.length > 0) {
      console.log(`SECRET WARNING in ${basename}: ${warns.join(', ')}. Verify these are not real credentials. Use environment variables for sensitive values.`);
    }

  } catch (e) {
    // silent
  }
  process.exit(0);
});
