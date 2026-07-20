//! Windows confinement plan: the platform-independent computation of what
//! must be enforced for a [`SandboxPolicy`] on Windows. This module has no
//! Win32 dependency and compiles and runs its tests on every platform
//! (including this crate's Linux CI job), which is deliberate: the
//! enforced-defaults invariants are exactly the part of the Windows backend
//! this repository can actually verify without a Windows toolchain. The
//! Win32 calls that apply this plan live in `windows.rs`, are compiled only
//! `cfg(windows)`, and are documented in `VENDORING.md` as needing lab
//! validation.

use crate::SandboxPolicy;
use std::path::{Path, PathBuf};

/// Windows-specific enforcement plan derived from a [`SandboxPolicy`].
/// Every field is a MUST-apply default: there is no policy input that
/// produces a plan without the directory ACL or without
/// `job_object_kill_on_close`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WindowsConfinementPlan {
    /// Parent directory of `hook_config_dir`. The deny-write ACE for the
    /// sandbox principal goes on THIS directory (object-inherit +
    /// container-inherit, inheritance from above disabled), not on
    /// `hook_config_dir` itself. Security spike #307 row 13 confirmed a
    /// file/dir-level ACL alone does not close the TOCTOU
    /// missing-file-create class; only the parent-directory ACE does.
    pub acl_deny_write_parent_dir: PathBuf,
    /// Always `true`. The parent directory's DACL must be protected
    /// (`PROTECTED_DACL_SECURITY_INFORMATION`) so an inherited
    /// Users-writable ACE from further up the tree cannot coexist with the
    /// new deny ACE.
    pub protect_dacl_from_inheritance: bool,
    /// Always `true`. Job Objects give process-tree containment, not
    /// filesystem protection (spike #307: "zero filesystem-CBSE protection
    /// by itself"), but killing the whole tree when the sandbox handle
    /// closes is still required so no sandboxed process outlives its
    /// workspace/ACL lifetime.
    pub job_object_kill_on_close: bool,
    // Network egress is NOT represented in this plan. The old `netsh` codegen
    // fields (`firewall_deny_outbound`, `firewall_allow_outbound_hosts`) were
    // removed in Integration B2: `netsh` evaluates block rules ahead of allow
    // rules, so that codegen could only ever express a strict deny-all, never
    // the requested AllowHosts allowlist, and it was never wired to a live
    // effect. Egress enforcement now lives in the WFP + Windows Firewall COM
    // layer (`windows_firewall.rs` + `codex_windows_sandbox::wfp`), keyed on
    // the sandbox account SID, applied at provision time.
}

impl WindowsConfinementPlan {
    pub fn for_policy(policy: &SandboxPolicy) -> Self {
        // Network egress is not represented in this plan; it is enforced by the
        // WFP + firewall COM layer at provision time (see the struct's network
        // comment), so `policy.network()` is intentionally not read here.
        Self {
            acl_deny_write_parent_dir: parent_dir(policy.hook_config_dir()),
            protect_dacl_from_inheritance: true,
            job_object_kill_on_close: true,
        }
    }
}

/// Computes the parent of a Windows-style path using explicit `\`/`/`
/// parsing rather than `std::path::Path::parent`. This module's tests are
/// required to run meaningfully on the Linux CI job (see module docs), but
/// `Path` uses the host's native separator: on a Unix host it treats `\` as
/// an ordinary filename character, not a component boundary, so
/// `Path::parent` silently gives the wrong answer for `C:\...` input when
/// this crate is checked on Linux.
fn parent_dir(hook_config_dir: &Path) -> PathBuf {
    let raw = hook_config_dir.to_string_lossy();
    let trimmed = raw.trim_end_matches(['\\', '/']);
    match trimmed.rfind(['\\', '/']) {
        Some(idx) if idx > 0 => {
            let parent = &trimmed[..idx];
            // "C:" alone means "current directory on drive C" to Win32,
            // not the drive root; keep the separator so the ACE target is
            // the unambiguous root "C:\", not a relative-to-cwd path.
            if is_bare_drive_letter(parent) {
                PathBuf::from(format!("{parent}\\"))
            } else {
                PathBuf::from(parent)
            }
        }
        // No separator, or only a drive-root separator (e.g. "C:\"): there
        // is no parent to ascend to. Falling back to the dir itself keeps
        // the ACE somewhere rather than producing an empty path, and
        // `protect_dacl_from_inheritance` still applies to it.
        _ => hook_config_dir.to_path_buf(),
    }
}

/// True for a bare drive letter with no trailing separator, e.g. `"C:"`.
fn is_bare_drive_letter(s: &str) -> bool {
    let bytes = s.as_bytes();
    bytes.len() == 2 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':'
}

/// Encodes an argv (`command[0]` is the program) into the single, NUL-
/// terminated UTF-16 command-line string `CreateProcessAsUserW` expects in
/// `lpCommandLine`, quoting each argument by the exact rules
/// `CommandLineToArgvW` parses back (the "Everyone quotes command line
/// arguments the wrong way" algorithm): an argument is wrapped in double
/// quotes when it is empty or contains a space or tab; embedded double quotes
/// are escaped with a preceding backslash; and any run of backslashes is
/// doubled only when it immediately precedes a double quote (an escaped one or
/// the wrapping close quote). This is pure computation with no Win32
/// dependency, kept here so it is covered by this crate's Linux CI job rather
/// than only by an unrunnable Windows-only test; `windows.rs` hands the result
/// straight to `CreateProcessAsUserW`.
pub fn command_line_to_utf16(command: &[String]) -> Vec<u16> {
    let mut out: Vec<u16> = Vec::new();
    for (i, arg) in command.iter().enumerate() {
        if i > 0 {
            out.push(u16::from(b' '));
        }
        append_quoted_arg(&mut out, arg);
    }
    out.push(0);
    out
}

/// Appends one argv element to `out`, quoted per the `CommandLineToArgvW`
/// round-trip rules (see [`command_line_to_utf16`]).
fn append_quoted_arg(out: &mut Vec<u16>, arg: &str) {
    let needs_quotes = arg.is_empty() || arg.contains([' ', '\t']);
    if needs_quotes {
        out.push(u16::from(b'"'));
    }
    let mut backslashes: usize = 0;
    for unit in arg.encode_utf16() {
        if unit == u16::from(b'\\') {
            backslashes += 1;
        } else {
            if unit == u16::from(b'"') {
                // Escape every backslash in the run, then the quote itself.
                for _ in 0..=backslashes {
                    out.push(u16::from(b'\\'));
                }
            }
            backslashes = 0;
        }
        out.push(unit);
    }
    if needs_quotes {
        // Double any trailing backslashes so the closing quote is not escaped.
        for _ in 0..backslashes {
            out.push(u16::from(b'\\'));
        }
        out.push(u16::from(b'"'));
    }
}

/// True only for a Windows program path that is fully qualified: a
/// drive-absolute path (`C:\tool.exe` or `C:/tool.exe`) or a UNC / device path
/// (`\\server\share\tool.exe`, `\\?\C:\tool.exe`). Rejects relative paths,
/// bare program names (`notepad.exe`), rooted-but-driveless paths (`\tool.exe`,
/// which resolves against the current drive), and drive-RELATIVE paths
/// (`C:tool.exe`, which Win32 resolves against drive C's current directory).
///
/// `windows::launch` requires this for `command[0]` because it calls
/// `CreateProcessAsUserW` with `lpApplicationName = NULL`: Windows then runs
/// the module search, which consults the child's CURRENT DIRECTORY before
/// PATH. A fully qualified path removes cwd from that search, closing the
/// binary-planting vector. Parsed here with explicit `\`/`/` handling (not via
/// `Path::is_absolute`, which is host-relative) so the check is Windows-correct
/// and unit-tested on the Linux CI job, exactly like [`parent_dir`].
pub fn is_fully_qualified_program(program: &str) -> bool {
    let bytes = program.as_bytes();
    let is_sep = |b: u8| b == b'\\' || b == b'/';
    // UNC or device path: begins with two separators (e.g. `\\server\share`,
    // `\\?\C:\...`, `//server/share`). Either separator flavour counts.
    if bytes.len() >= 2 && is_sep(bytes[0]) && is_sep(bytes[1]) {
        return true;
    }
    // Drive-absolute: drive letter, `:`, then a separator (`C:\...`, `C:/...`).
    // A drive letter with no following separator (`C:tool.exe`) is
    // drive-relative and must be rejected.
    bytes.len() >= 3 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':' && is_sep(bytes[2])
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::policy::NetworkPolicy;
    use crate::policy::SandboxPolicy;
    use pretty_assertions::assert_eq;

    #[test]
    fn plan_acls_the_parent_of_hook_config_dir_not_the_dir_itself() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert_eq!(
            plan.acl_deny_write_parent_dir,
            PathBuf::from(r"C:\Users\agent\AppData\Hive")
        );
    }

    #[test]
    fn plan_always_protects_dacl_and_sets_kill_on_close() {
        let policy = SandboxPolicy::build(
            vec![PathBuf::from(r"C:\Users\agent\workspace")],
            vec![],
            PathBuf::from(r"C:\Users\agent\AppData\Hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert!(
            plan.protect_dacl_from_inheritance,
            "DACL protection must not be optional"
        );
        assert!(
            plan.job_object_kill_on_close,
            "kill-on-close must not be optional"
        );
    }

    #[test]
    fn plan_for_hook_config_dir_at_drive_root_falls_back_to_itself() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        assert_eq!(plan.acl_deny_write_parent_dir, PathBuf::from(r"C:\"));
    }

    #[test]
    fn plan_for_hook_config_dir_one_level_below_drive_root_keeps_root_separator() {
        let policy = SandboxPolicy::build(
            vec![],
            vec![],
            PathBuf::from(r"C:\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("valid policy");

        let plan = WindowsConfinementPlan::for_policy(&policy);
        // Must be "C:\" (the drive root), not "C:" (Win32's "current
        // directory on drive C", a different and non-deterministic path).
        assert_eq!(plan.acl_deny_write_parent_dir, PathBuf::from(r"C:\"));
    }

    /// Strips the trailing NUL and decodes the UTF-16 command line back to a
    /// `String` for readable assertions.
    fn decode_command_line(units: &[u16]) -> String {
        assert_eq!(
            units.last().copied(),
            Some(0),
            "command line must be NUL-terminated"
        );
        String::from_utf16(&units[..units.len() - 1]).expect("valid UTF-16")
    }

    #[test]
    fn command_line_joins_and_quotes_only_where_required() {
        let argv = vec!["foo".to_string(), "bar baz".to_string()];
        assert_eq!(
            decode_command_line(&command_line_to_utf16(&argv)),
            r#"foo "bar baz""#
        );
    }

    #[test]
    fn command_line_leaves_simple_single_arg_unquoted() {
        assert_eq!(
            decode_command_line(&command_line_to_utf16(&["notepad.exe".to_string()])),
            "notepad.exe"
        );
    }

    #[test]
    fn command_line_quotes_empty_argument() {
        assert_eq!(
            decode_command_line(&command_line_to_utf16(&["".to_string()])),
            r#""""#
        );
    }

    #[test]
    fn command_line_escapes_embedded_quote_even_when_unquoted() {
        // `a"b` has no whitespace, so it is not wrapped, but the embedded
        // quote must still be backslash-escaped or CommandLineToArgvW would
        // mis-split it.
        assert_eq!(
            decode_command_line(&command_line_to_utf16(&[r#"a"b"#.to_string()])),
            r#"a\"b"#
        );
    }

    #[test]
    fn command_line_doubles_trailing_backslashes_before_closing_quote() {
        // `a b\` is wrapped for the space; the trailing backslash must be
        // doubled so it does not escape the wrapping close quote.
        assert_eq!(
            decode_command_line(&command_line_to_utf16(&[r"a b\".to_string()])),
            r#""a b\\""#
        );
    }

    #[test]
    fn command_line_for_empty_argv_is_just_a_nul() {
        assert_eq!(command_line_to_utf16(&[]), vec![0u16]);
    }

    #[test]
    fn command_line_quotes_and_escapes_arg_with_both_quote_and_whitespace() {
        // `a "b` has BOTH whitespace (forces wrapping) and an embedded quote
        // (forces backslash-escaping). The two rules must compose: the arg is
        // wrapped in quotes AND the inner quote is escaped, so
        // CommandLineToArgvW parses it back as the single original argument.
        assert_eq!(
            decode_command_line(&command_line_to_utf16(&[r#"a "b"#.to_string()])),
            r#""a \"b""#
        );
    }

    #[test]
    fn fully_qualified_program_accepts_drive_absolute_and_unc() {
        for good in [
            r"C:\Windows\System32\notepad.exe",
            r"C:/tools/task.exe",
            r"\\server\share\task.exe",
            r"\\?\C:\tools\task.exe",
            r"//server/share/task.exe",
        ] {
            assert!(
                is_fully_qualified_program(good),
                "{good} should be accepted"
            );
        }
    }

    #[test]
    fn fully_qualified_program_rejects_relative_and_drive_relative() {
        for bad in [
            "",
            "notepad.exe",
            r".\task.exe",
            r"..\task.exe",
            "tools/task.exe",
            r"C:task.exe", // drive-RELATIVE: resolves against C:'s current dir
            "C:",
            r"\task.exe", // rooted but drive-less: current-drive-relative
        ] {
            assert!(
                !is_fully_qualified_program(bad),
                "{bad:?} should be rejected"
            );
        }
    }
}
