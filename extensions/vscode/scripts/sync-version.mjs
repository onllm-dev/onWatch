// Stamps package.json and package-lock.json with the onWatch daemon version so
// the extension never carries a separately maintained number.
//
//   node scripts/sync-version.mjs            # reads ../../VERSION
//   node scripts/sync-version.mjs 2.14.0     # explicit version
//   node scripts/sync-version.mjs v2.14.0-beta.1
//
// The Marketplace only accepts major.minor.patch, so a leading "v" and any
// pre-release suffix are stripped; the suffix belongs in the .vsix file name.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const raw = (process.argv[2] ?? readFileSync(join(root, "..", "..", "VERSION"), "utf8")).trim();
const match = /^v?(\d+\.\d+\.\d+)/.exec(raw);
if (!match) {
  console.error(`sync-version: cannot derive major.minor.patch from ${JSON.stringify(raw)}`);
  process.exit(1);
}
const version = match[1];

for (const file of ["package.json", "package-lock.json"]) {
  const path = join(root, file);
  const data = JSON.parse(readFileSync(path, "utf8"));
  data.version = version;
  if (data.packages && data.packages[""]) {
    data.packages[""].version = version;
  }
  writeFileSync(path, `${JSON.stringify(data, null, 2)}\n`);
}
console.log(version);
