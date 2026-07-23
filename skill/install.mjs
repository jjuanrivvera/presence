#!/usr/bin/env node
// Installs the "mesh" skill into a project's (or user's) Claude Code skills dir.
//
//   npx github:jjuanrivvera/presence            # into ./.claude/skills/mesh
//   npx github:jjuanrivvera/presence --user     # into ~/.claude/skills/mesh
//   npx github:jjuanrivvera/presence <dir>      # into <dir>/.claude/skills/mesh
//
// Copies this folder's SKILL.md; no dependencies, no network.
import { mkdirSync, copyFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";

const here = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);

let base;
if (args.includes("--user")) {
  base = homedir();
} else {
  const pos = args.find((a) => !a.startsWith("-"));
  base = pos ? resolve(pos) : process.cwd();
}

const dest = join(base, ".claude", "skills", "mesh");
mkdirSync(dest, { recursive: true });
copyFileSync(join(here, "SKILL.md"), join(dest, "SKILL.md"));

console.log(`✓ mesh skill installed → ${join(dest, "SKILL.md")}`);
console.log("  Open /skills in Claude Code (or restart) to pick it up.");
