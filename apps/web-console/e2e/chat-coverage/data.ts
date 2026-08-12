// The committed data files this gate is configured by, read once and
// validated on the way in.
//
// Separate from the specs so the live sweep and the offline self-checks read
// exactly the same objects through exactly the same validators. A malformed
// file fails loudly here rather than turning into an undefined floor or an
// empty exclusion list at the moment the gate is deciding whether to fail.
import fs from "node:fs";
import path from "node:path";

import {
  parseDataDriven,
  parseExclusions,
  parseFloors,
  parseRegistry,
  parseRemoved,
  type Floors,
  type InertEntry,
  type RemovedSurfaces,
  type SurfaceExclusion,
} from "./lib";

export const FLOOR_FILE = path.join(__dirname, "surface-floors.json");

function read(file: string): unknown {
  return JSON.parse(fs.readFileSync(path.join(__dirname, file), "utf8"));
}

export const FLOORS: Floors = parseFloors(read("surface-floors.json"));
export const DATA_DRIVEN: Set<string> = parseDataDriven(read("surface-floors.json"));
export const EXCLUSIONS: SurfaceExclusion[] = parseExclusions(read("surface-exclusions.json"));
export const REGISTRY: InertEntry[] = parseRegistry(read("inert-registry.json"));
export const REMOVED: RemovedSurfaces = parseRemoved(read("removed-surfaces.json"));
