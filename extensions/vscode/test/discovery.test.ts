import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  DEFAULT_PORT,
  discoverBaseUrl,
  isLoopbackUrl,
  normalizeDaemonUrl,
  parsePort,
  portFileCandidates,
  readPortFile,
  resolveBaseUrl,
} from "../src/discovery";

describe("parsePort", () => {
  it("accepts integers in range", () => {
    expect(parsePort("9211")).toBe(9211);
    expect(parsePort(" 8080\n")).toBe(8080);
    expect(parsePort("1")).toBe(1);
    expect(parsePort("65535")).toBe(65535);
  });

  it("rejects junk", () => {
    expect(parsePort("0")).toBeUndefined();
    expect(parsePort("65536")).toBeUndefined();
    expect(parsePort("-5")).toBeUndefined();
    expect(parsePort("12ab")).toBeUndefined();
    expect(parsePort("9211.5")).toBeUndefined();
    expect(parsePort("")).toBeUndefined();
    expect(parsePort(undefined)).toBeUndefined();
    expect(parsePort(null)).toBeUndefined();
  });
});

describe("normalizeDaemonUrl", () => {
  it("strips trailing slashes and keeps a base path", () => {
    expect(normalizeDaemonUrl("http://127.0.0.1:9211/")).toBe("http://127.0.0.1:9211");
    expect(normalizeDaemonUrl("http://host:9211/onwatch/")).toBe("http://host:9211/onwatch");
    expect(normalizeDaemonUrl("http://host:9211/onwatch///")).toBe("http://host:9211/onwatch");
    expect(normalizeDaemonUrl("  https://quota.example.com  ")).toBe("https://quota.example.com");
  });

  it("drops query strings and fragments", () => {
    expect(normalizeDaemonUrl("http://host:9211/?x=1#frag")).toBe("http://host:9211");
  });

  it("rejects non-http schemes and garbage", () => {
    expect(normalizeDaemonUrl("ftp://host")).toBeUndefined();
    expect(normalizeDaemonUrl("host:9211")).toBeUndefined();
    expect(normalizeDaemonUrl("")).toBeUndefined();
    expect(normalizeDaemonUrl("not a url")).toBeUndefined();
  });
});

describe("resolveBaseUrl", () => {
  it("uses the setting when it is valid", () => {
    expect(resolveBaseUrl({ daemonUrl: "http://host:9211/onwatch/", portFileContents: "1234", envPort: "5678" })).toEqual({
      url: "http://host:9211/onwatch",
      source: "setting",
    });
  });

  it("falls back to the port file", () => {
    expect(resolveBaseUrl({ daemonUrl: "", portFileContents: "1234\n", envPort: "5678" })).toEqual({
      url: "http://127.0.0.1:1234",
      source: "portFile",
    });
  });

  it("falls back to the env var when the port file is missing or invalid", () => {
    expect(resolveBaseUrl({ daemonUrl: "", portFileContents: undefined, envPort: "5678" })).toEqual({
      url: "http://127.0.0.1:5678",
      source: "env",
    });
    expect(resolveBaseUrl({ daemonUrl: "", portFileContents: "garbage", envPort: "5678" }).url).toBe("http://127.0.0.1:5678");
  });

  it("falls back to the default port", () => {
    expect(resolveBaseUrl({})).toEqual({ url: `http://127.0.0.1:${DEFAULT_PORT}`, source: "default" });
    expect(DEFAULT_PORT).toBe(9211);
  });

  it("reports an invalid setting and continues discovery", () => {
    const r = resolveBaseUrl({ daemonUrl: "nope", envPort: "7000" });
    expect(r.url).toBe("http://127.0.0.1:7000");
    expect(r.source).toBe("env");
    expect(r.warning).toMatch(/nope/);
  });
});

describe("isLoopbackUrl", () => {
  it("recognizes loopback hosts", () => {
    expect(isLoopbackUrl("http://127.0.0.1:9211")).toBe(true);
    expect(isLoopbackUrl("http://127.0.0.2:9211")).toBe(true);
    expect(isLoopbackUrl("http://localhost:9211/base")).toBe(true);
    expect(isLoopbackUrl("http://LOCALHOST")).toBe(true);
    expect(isLoopbackUrl("http://[::1]:9211")).toBe(true);
  });

  it("rejects remote hosts", () => {
    expect(isLoopbackUrl("http://192.168.1.4:9211")).toBe(false);
    expect(isLoopbackUrl("https://quota.example.com")).toBe(false);
    expect(isLoopbackUrl("http://localhost.example.com")).toBe(false);
    expect(isLoopbackUrl("garbage")).toBe(false);
  });
});

describe("readPortFile / discoverBaseUrl", () => {
  let home: string;
  afterEach(() => {
    if (home) {
      rmSync(home, { recursive: true, force: true });
    }
  });

  it("reads ~/.onwatch/port", () => {
    home = mkdtempSync(join(tmpdir(), "onwatch-vscode-"));
    mkdirSync(join(home, ".onwatch"));
    writeFileSync(join(home, ".onwatch", "port"), "4321\n");
    expect(readPortFile(home)).toBe("4321\n");
    expect(discoverBaseUrl("", {}, home)).toEqual({ url: "http://127.0.0.1:4321", source: "portFile" });
  });

  it("prefers %LOCALAPPDATA%\\onwatch\\port on Windows, where the daemon writes it", () => {
    home = mkdtempSync(join(tmpdir(), "onwatch-vscode-"));
    const local = join(home, "AppData", "Local");
    mkdirSync(join(local, "onwatch"), { recursive: true });
    writeFileSync(join(local, "onwatch", "port"), "9300\n");
    mkdirSync(join(home, ".onwatch"));
    writeFileSync(join(home, ".onwatch", "port"), "1111\n");
    const env = { LOCALAPPDATA: local };
    expect(portFileCandidates("win32", env, home)).toEqual([join(local, "onwatch", "port"), join(home, ".onwatch", "port")]);
    expect(readPortFile(home, env, "win32")).toBe("9300\n");
    expect(discoverBaseUrl("", env, home, "win32")).toEqual({ url: "http://127.0.0.1:9300", source: "portFile" });
    // Other platforms never look at LOCALAPPDATA.
    expect(portFileCandidates("darwin", env, home)).toEqual([join(home, ".onwatch", "port")]);
    expect(readPortFile(home, env, "linux")).toBe("1111\n");
  });

  it("falls back to the home directory on Windows when LOCALAPPDATA is unset or has no file", () => {
    home = mkdtempSync(join(tmpdir(), "onwatch-vscode-"));
    mkdirSync(join(home, ".onwatch"));
    writeFileSync(join(home, ".onwatch", "port"), "2222\n");
    expect(readPortFile(home, {}, "win32")).toBe("2222\n");
    expect(readPortFile(home, { LOCALAPPDATA: join(home, "nowhere") }, "win32")).toBe("2222\n");
  });

  it("returns undefined when the file is missing", () => {
    home = mkdtempSync(join(tmpdir(), "onwatch-vscode-"));
    expect(readPortFile(home)).toBeUndefined();
    expect(discoverBaseUrl("", { ONWATCH_PORT: "9999" }, home)).toEqual({ url: "http://127.0.0.1:9999", source: "env" });
    expect(discoverBaseUrl("", {}, home)).toEqual({ url: "http://127.0.0.1:9211", source: "default" });
  });
});
