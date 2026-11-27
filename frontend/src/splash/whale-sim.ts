import type {Outcome} from "./scene";

export const WATERLINE = 74;
export const START = 240;
export const LOOP = 5600;
/** Iconic open-fluke hang-time still frame for prefers-reduced-motion (matches fixed-cam 00:01). */
export const STILL = 2380;
export const CONTACT = {time: 3360, x: 10, y: WATERLINE} as const;

/** Subtle ~22° oblique motion plane matching fixed-camera whale footage for natural 3D depth. */
export const MOTION_PLANE_ANGLE = (22 * Math.PI) / 180;

export interface Point2D { x: number; y: number }
export interface Point3D { x: number; y: number; z: number }

export interface Pose {
  baseX: number;
  baseY: number;
  /** Pitch in the 20° motion plane; rotates monotonically 360° underwater. */
  pitch: number;
  /** Peduncle spine arch curvature (rad). */
  arch: number;
  /** Elastic fluke flex (+ droops heavy downward like 00:00, - curls tips upward like 00:01). */
  flex: number;
  /** Subtle organic twist around the spine axis (rad). */
  twist: number;
  /** Gentle side bank angle (rad). */
  bank: number;
  /** Subtle left/right living wing asymmetry phase (-1..1). */
  asym: number;
  /** Extra downward offset used when sinking smoothly on failure. */
  sinkY: number;
}

export interface MeshTriangle {
  a: Point3D;
  b: Point3D;
  c: Point3D;
  normal: Point3D;
  /** True for dorsal (top) skin, false for ventral (underside) skin. */
  dorsal: boolean;
  station: number;
}

export interface UnifiedTailMesh {
  spine: Point3D[];
  triangles: MeshTriangle[];
  trailingEdge3D: Point3D[];
  wrist: Point3D;
  /** High-resolution projected-ready 3D outline of the real whale fluke + peduncle. */
  flukeOutline3D: Point3D[];
  peduncleOutline3D: Point3D[];
  /** Dorsal-facing factor at the fluke center (-1 = full ventral, +1 = full dorsal). */
  dorsalFacing: number;
}

export interface ProjectedTriangle {
  a: Point2D;
  b: Point2D;
  c: Point2D;
  depth: number;
  shade: number;
  dorsal: boolean;
}

export interface TailProjection {
  triangles: ProjectedTriangle[];
  /** Silky-smooth real-whale outer contour curves (peduncle + flukes) for crisp 2D rendering. */
  flukeContour: Point2D[];
  peduncleContour: Point2D[];
  /** +1 when showing dorsal face, -1 when showing ventral face. */
  dorsalFacing: number;
  trailingEdge: Point2D[];
  waterlineCollar: { x0: number; x1: number } | null;
}

export interface Droplet {
  x: number;
  y: number;
  vx: number;
  vy: number;
  age: number;
  life: number;
  gravity: number;
  drag: number;
  glyph: string;
  alpha: number;
  size: number;
}

export interface FoamBurst {
  x: number;
  age: number;
  life: number;
  spread: number;
}

export interface WakePulse {
  x: number;
  age: number;
  life: number;
  strength: number;
  speed: number;
}

type Beat = readonly [ms: number, baseX: number, baseY: number, pitch: number, arch: number, flex: number, twist: number, bank: number, asym: number];

// Majestic 5.6s biological rhythm with strong timing contrast:
//   0..1450ms:    Slow, heavy, muscular lift out of the sea (drooping umbrella wings + waterfall)
//   1450..2720ms: Poetic floating hang-time at apex, gracefully rolling over to unfurl open flukes
//   2720..3360ms: Accelerating whip downstroke (outer wingtips lagging organically behind!)
//   3360..5600ms: Twin-wing splash + central rebound plume + unhurried 360° underwater revolution
const TWO_PI = Math.PI * 2;
const BEATS: readonly Beat[] = [
  [0,             -14, -210,  1.32,           0.18,  0.16,  0.06, -0.05, -0.10],
  [420,           -12, -162,  1.18,           0.26,  0.38,  0.05, -0.04, -0.18], // slow breach
  [1180,           -6,  -72,  0.66,           0.44,  0.76,  0.02, -0.03, -0.14], // heavy lift + waterfall
  [1780,           -2,  -44,  0.08,           0.20,  0.36, -0.20,  0.07,  0.16], // slow floating apex rollover
  [2380,            4,  -56, -0.48,          -0.16, -0.24, -0.08,  0.10,  0.22], // iconic hang-time open flukes
  [2820,            7,  -76, -0.70,          -0.28, -0.44, -0.03,  0.07,  0.14], // gather tension before slam
  [3140,            9, -126, -1.00,          -0.36, -0.58,  0.00,  0.03,  0.06], // accelerating whip downstroke
  [CONTACT.time,   10, -176, -1.28,          -0.16, -0.16,  0.02,  0.00,  0.00], // flat full-span impact!
  [3920,            8, -252, -2.35,           0.05,  0.12,  0.04, -0.02, -0.04], // underwater 360° turn
  [4780,           -8, -270, -3.92,           0.14,  0.16,  0.05, -0.04, -0.08], // underwater 360° return
  [LOOP,          -14, -210,  1.32 - TWO_PI,  0.18,  0.16,  0.06, -0.05, -0.10],
];

const FOCAL = 660;
const SPRAY_GLYPHS = ["·", ".", ":", "*", "+", "°", "·"] as const;
const WATERFALL_GLYPHS = ["|", ":", "·", "¦", ".", "·"] as const;
const MAX_DROPLETS = 420;

export const mix = (a: number, b: number, t: number) => a + (b - a) * t;
const clamp = (v: number, lo: number, hi: number) => (v < lo ? lo : v > hi ? hi : v);
const ease = (t: number) => t * t * (3 - 2 * t);
const smoothstep = (e0: number, e1: number, x: number) => {
  const t = clamp((x - e0) / (e1 - e0), 0, 1);
  return t * t * (3 - 2 * t);
};

function noise(n: number): number {
  const h = Math.imul(n ^ (n >>> 16), 0x45d9f3b);
  return ((Math.imul(h ^ (h >>> 16), 0x45d9f3b) ^ (h >>> 15)) >>> 0) / 4294967296;
}

function sub3(a: Point3D, b: Point3D): Point3D {
  return { x: a.x - b.x, y: a.y - b.y, z: a.z - b.z };
}

function cross3(a: Point3D, b: Point3D): Point3D {
  return {
    x: a.y * b.z - a.z * b.y,
    y: a.z * b.x - a.x * b.z,
    z: a.x * b.y - a.y * b.x,
  };
}

function norm3(v: Point3D): Point3D {
  const l = Math.hypot(v.x, v.y, v.z) || 1;
  return { x: v.x / l, y: v.y / l, z: v.z / l };
}

function cubicBezier(p0: number, p1: number, p2: number, p3: number, t: number): number {
  const mt = 1 - t;
  return mt * mt * mt * p0 + 3 * mt * mt * t * p1 + 3 * mt * t * t * p2 + t * t * t * p3;
}

/**
 * Exact real-whale fluke half-planform traced from the fixed-camera reference (00:01):
 * Returns lateral coordinate `u` [0..178] and leading/trailing chord `vLead`, `vTrail`
 * along the fluke axis from the wrist (v = 0).
 */
function sampleRealFlukePlanform(s: number): { u: number; vLead: number; vTrail: number } {
  // Leading edge: smooth convex sweep from peduncle wrist (12, -4) out to hooked tip (178, 96).
  const u = cubicBezier(0, 56, 132, 178, s);
  const vLead = cubicBezier(-8, 4, 34, 96, s);

  // Trailing edge: crisp deep central V-notch at (0, 28), rising into lush rounded paddle lobe
  // and curving cleanly into the exact same hooked tip (178, 96).
  let vTrail: number;
  if (s < 0.22) {
    // Crisp central V-notch wall opening smoothly into the inner lobe
    const k = s / 0.22;
    vTrail = cubicBezier(28, 52, 74, 86, k);
  } else {
    // Broad plush rounded paddle edge sweeping into the slightly upturned tip
    const k = (s - 0.22) / 0.78;
    const scallop = Math.sin(k * Math.PI * 4) * 1.6 * Math.sin(k * Math.PI);
    vTrail = cubicBezier(86, 98, 108, 96, k) + scallop;
  }
  return { u, vLead, vTrail };
}

export function samplePose(time: number): Pose {
  const wrapped = ((time % LOOP) + LOOP) % LOOP;
  const index = BEATS.findIndex(beat => beat[0] > wrapped);
  if (index < 1) {
    const [, baseX, baseY, pitch, arch, flex, twist, bank, asym] = BEATS[0];
    return { baseX, baseY, pitch, arch, flex, twist, bank, asym, sinkY: 0 };
  }
  const a = BEATS[index - 1];
  const b = BEATS[index];
  const previous = BEATS[index >= 2 ? index - 2 : BEATS.length - 2];
  const next = BEATS[index + 1 < BEATS.length ? index + 1 : 1];
  const span = b[0] - a[0];
  const t = (wrapped - a[0]) / span;
  const prevSpan = index >= 2 ? b[0] - previous[0] : b[0] + (LOOP - previous[0]);
  const nextSpan = index + 1 < BEATS.length ? next[0] - a[0] : (LOOP - a[0]) + next[0];

  const interp = (k: 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8) => {
    const prevVal = index >= 2 ? previous[k] : (k === 3 ? previous[k] + TWO_PI : previous[k]);
    const nextVal = index + 1 < BEATS.length ? next[k] : (k === 3 ? next[k] - TWO_PI : next[k]);
    const da = ((b[k] - prevVal) / prevSpan) * span;
    const db = ((nextVal - a[k]) / nextSpan) * span;
    return mix(a[k], b[k], ease(t)) + t * (1 - t) * ((1 - t) * da - t * db);
  };
  return {
    baseX: interp(1),
    baseY: interp(2),
    pitch: interp(3),
    arch: interp(4),
    flex: interp(5),
    twist: interp(6),
    bank: interp(7),
    asym: interp(8),
    sinkY: 0,
  };
}

/**
 * Hybrid 3D whale tail:
 * - Flexible 3D skeleton in the 20° oblique motion plane (360° monotonic underwater turn)
 * - Sculpted 3D peduncle tube seamlessly merging into the real Bezier-traced humpback flukes
 * - True dorsal & ventral 3D surfaces with smooth airfoil thickness & elastic wing flex
 */
export function buildUnifiedTailMesh(pose: Pose): UnifiedTailMesh {
  const cosP = Math.cos(MOTION_PLANE_ANGLE);
  const sinP = Math.sin(MOTION_PLANE_ANGLE);
  const planeSide: Point3D = { x: cosP, y: 0, z: -sinP };
  const planeFwd: Point3D = { x: sinP, y: 0, z: cosP };

  const pedSteps = 16;
  const pedRadial = 18;
  const pedLen = 162;
  const stepLen = pedLen / pedSteps;

  const spine: Point3D[] = [];
  const tangents: Point3D[] = [];
  const normals: Point3D[] = [];
  const binormals: Point3D[] = [];

  let mf = 0;
  let my = pose.baseY + pose.sinkY;
  let mx = pose.baseX;
  let pitchAngle = pose.pitch;

  for (let i = 0; i <= pedSteps; i++) {
    const s = i / pedSteps;
    spine.push({ x: mx + mf * planeFwd.x, y: my, z: mf * planeFwd.z });

    const sf = Math.sin(pitchAngle);
    const cy = Math.cos(pitchAngle);
    const T: Point3D = norm3({
      x: sf * planeFwd.x + Math.sin(pose.bank) * planeSide.x * s,
      y: cy,
      z: sf * planeFwd.z + Math.sin(pose.bank) * planeSide.z * s,
    });
    const baseN: Point3D = norm3({ x: cy * planeFwd.x, y: -sf, z: cy * planeFwd.z });
    const baseB: Point3D = norm3(cross3(T, baseN));

    const localTwist = pose.twist * smoothstep(0.15, 1.0, s);
    const ct = Math.cos(localTwist);
    const st = Math.sin(localTwist);
    const N: Point3D = {
      x: baseN.x * ct + baseB.x * st,
      y: baseN.y * ct + baseB.y * st,
      z: baseN.z * ct + baseB.z * st,
    };
    const B: Point3D = {
      x: baseB.x * ct - baseN.x * st,
      y: baseB.y * ct - baseN.y * st,
      z: baseB.z * ct - baseN.z * st,
    };

    tangents.push(T);
    normals.push(N);
    binormals.push(B);

    const curArch = pose.arch * (0.7 + 0.55 * s);
    pitchAngle += (curArch / pedSteps) * 1.25;
    mf += Math.sin(pitchAngle) * stepLen;
    my += Math.cos(pitchAngle) * stepLen;
    mx += Math.sin(pose.bank) * stepLen * 0.3;
  }

  const triangles: MeshTriangle[] = [];
  const pedRings: Point3D[][] = [];
  const pedLeft: Point3D[] = [];
  const pedRight: Point3D[] = [];

  // 1) Sleek, muscular, laterally-compressed peduncle tube merging into wrist.
  for (let i = 0; i <= pedSteps; i++) {
    const s = i / pedSteps;
    const c = spine[i];
    const N = normals[i];
    const B = binormals[i];
    const halfW = mix(27, 13.5, s ** 0.78);
    const halfH = mix(42, 14.5, s ** 0.78);
    const ring: Point3D[] = [];
    for (let j = 0; j < pedRadial; j++) {
      const th = (j / pedRadial) * TWO_PI;
      const lat = Math.cos(th) * halfW;
      const dor = Math.sin(th) * halfH;
      ring.push({
        x: c.x + B.x * lat + N.x * dor,
        y: c.y + B.y * lat + N.y * dor,
        z: c.z + B.z * lat + N.z * dor,
      });
    }
    pedRings.push(ring);
    pedLeft.push({ x: c.x - B.x * halfW, y: c.y - B.y * halfW, z: c.z - B.z * halfW });
    pedRight.push({ x: c.x + B.x * halfW, y: c.y + B.y * halfW, z: c.z + B.z * halfW });
  }

  for (let i = 0; i < pedSteps; i++) {
    const r0 = pedRings[i], r1 = pedRings[i + 1];
    const station = (i + 0.5) / (pedSteps * 2);
    for (let j = 0; j < pedRadial; j++) {
      const jn = (j + 1) % pedRadial;
      const dorsal = j < pedRadial / 2;
      const n1 = norm3(cross3(sub3(r1[j], r0[j]), sub3(r0[jn], r0[j])));
      const n2 = norm3(cross3(sub3(r1[jn], r1[j]), sub3(r0[jn], r1[j])));
      triangles.push({ a: r0[j], b: r1[j], c: r0[jn], normal: n1, dorsal, station });
      triangles.push({ a: r1[j], b: r1[jn], c: r0[jn], normal: n2, dorsal, station });
    }
  }

  // 2) Real Bezier-traced humpback flukes skinned onto the 3D wrist frame with true airfoil volume & flex!
  const wrist = spine[pedSteps];
  const Tw = tangents[pedSteps];
  const Nw = normals[pedSteps];
  const Bw = binormals[pedSteps];

  const spanSegs = 28; // per side -> 56 across full wingspan!
  const chordSegs = 8;
  const trailingEdge3D: Point3D[] = [];
  const leadLeft3D: Point3D[] = [];
  const leadRight3D: Point3D[] = [];
  const trailLeft3D: Point3D[] = [];
  const trailRight3D: Point3D[] = [];

  // Map a point (side * u, v) on the real whale fluke into 3D space with supple organic cartilage
  // bend, cupped lobe curvature, living left/right asymmetry, and pronounced central muscular keel!
  const mapFlukePt = (side: -1 | 1, uAbs: number, v: number, cFrac: number, isDorsal: boolean): Point3D => {
    const uNorm = uAbs / 178;
    const lat = side * uAbs;
    // Living asymmetry: one wing slightly leads/cups ahead of the other instead of a rigid flat plank.
    const wingFlex = pose.flex + side * pose.asym * 0.28;
    const chordBend = Math.max(0, v / 100) ** 1.35 * wingFlex * 52;
    const spanBend = (uNorm ** 1.45) * wingFlex * 86 * (0.28 + 0.72 * cFrac);
    // Pronounced organic lobe cupping + trailing-edge wave so 3D normals vary richly across the skin.
    const lobeCup = 14 * Math.sin(uNorm * Math.PI) * Math.sin(cFrac * Math.PI * 0.9);
    const edgeRipple = Math.sin(uNorm * Math.PI * 2 + side * 0.4) * 5 * (cFrac ** 1.5);

    // Muscular central keel tapering smoothly into sculpted airfoil blades.
    const airfoil = Math.sin(cFrac * Math.PI) ** 0.62 * (1 - 0.30 * cFrac);
    const spanTaper = Math.max(0.02, (1 - uNorm * uNorm) ** 0.50);
    const centerKeel = Math.exp(-((uNorm / 0.16) ** 2)) * (1 - cFrac * 0.85) * 14.5;
    const thick = (13.5 * airfoil * spanTaper + centerKeel) * (isDorsal ? 1 : -1);

    const dor = thick + lobeCup + edgeRipple + chordBend + spanBend;
    return {
      x: wrist.x + Bw.x * lat + Tw.x * v + Nw.x * dor,
      y: wrist.y + Bw.y * lat + Tw.y * v + Nw.y * dor,
      z: wrist.z + Bw.z * lat + Tw.z * v + Nw.z * dor,
    };
  };

  for (const side of [-1, 1] as const) {
    // Build dorsal & ventral surface grids for this wing
    const dorsalGrid: Point3D[][] = [];
    const ventralGrid: Point3D[][] = [];
    for (let i = 0; i <= spanSegs; i++) {
      const s = i / spanSegs;
      const { u, vLead, vTrail } = sampleRealFlukePlanform(s);
      const dRow: Point3D[] = [];
      const vRow: Point3D[] = [];
      for (let k = 0; k <= chordSegs; k++) {
        const cf = k / chordSegs;
        const v = mix(vLead, vTrail, cf);
        dRow.push(mapFlukePt(side, u, v, cf, true));
        vRow.push(mapFlukePt(side, u, v, cf, false));
      }
      dorsalGrid.push(dRow);
      ventralGrid.push(vRow);

      // Record exact outer silhouette curves for silky-smooth Canvas 2D rendering
      const pLead = dRow[0];
      const pTrail = dRow[chordSegs];
      if (side === -1) {
        leadLeft3D.push(pLead);
        trailLeft3D.push(pTrail);
      } else {
        leadRight3D.push(pLead);
        trailRight3D.push(pTrail);
      }
      if (pTrail.y > 4 && i % 2 === 0) trailingEdge3D.push(pTrail);
    }

    // Triangulate dorsal and ventral skins with consistent outward winding
    for (let i = 0; i < spanSegs; i++) {
      const station = 0.5 + (i / spanSegs) * 0.5;
      for (let k = 0; k < chordSegs; k++) {
        // Dorsal skin
        const d00 = dorsalGrid[i][k], d10 = dorsalGrid[i + 1][k];
        const d01 = dorsalGrid[i][k + 1], d11 = dorsalGrid[i + 1][k + 1];
        if (side === 1) {
          const n1 = norm3(cross3(sub3(d10, d00), sub3(d01, d00)));
          const n2 = norm3(cross3(sub3(d11, d10), sub3(d01, d10)));
          triangles.push({ a: d00, b: d10, c: d01, normal: n1, dorsal: true, station });
          triangles.push({ a: d10, b: d11, c: d01, normal: n2, dorsal: true, station });
        } else {
          const n1 = norm3(cross3(sub3(d01, d00), sub3(d10, d00)));
          const n2 = norm3(cross3(sub3(d01, d10), sub3(d11, d10)));
          triangles.push({ a: d00, b: d01, c: d10, normal: n1, dorsal: true, station });
          triangles.push({ a: d10, b: d01, c: d11, normal: n2, dorsal: true, station });
        }

        // Ventral skin (opposite winding)
        const v00 = ventralGrid[i][k], v10 = ventralGrid[i + 1][k];
        const v01 = ventralGrid[i][k + 1], v11 = ventralGrid[i + 1][k + 1];
        if (side === 1) {
          const n1 = norm3(cross3(sub3(v01, v00), sub3(v10, v00)));
          const n2 = norm3(cross3(sub3(v01, v10), sub3(v11, v10)));
          triangles.push({ a: v00, b: v01, c: v10, normal: n1, dorsal: false, station });
          triangles.push({ a: v10, b: v01, c: v11, normal: n2, dorsal: false, station });
        } else {
          const n1 = norm3(cross3(sub3(v10, v00), sub3(v01, v00)));
          const n2 = norm3(cross3(sub3(v11, v10), sub3(v01, v10)));
          triangles.push({ a: v00, b: v10, c: v01, normal: n1, dorsal: false, station });
          triangles.push({ a: v10, b: v11, c: v01, normal: n2, dorsal: false, station });
        }
      }
    }
  }

  // Closed outer 3D silhouette of the real whale flukes:
  // center lead -> right lead -> right tip -> right trail -> center V-notch -> left trail -> left tip -> left lead
  const flukeOutline3D: Point3D[] = [
    ...leadRight3D,
    ...trailRight3D.slice().reverse(),
    ...trailLeft3D,
    ...leadLeft3D.slice().reverse(),
  ];
  const peduncleOutline3D: Point3D[] = [...pedLeft, ...pedRight.slice().reverse()];

  // Camera looks along +Z, so Nw.z < 0 means dorsal faces camera!
  const dorsalFacing = clamp(-Nw.z, -1, 1);

  return {
    spine,
    triangles,
    trailingEdge3D,
    wrist,
    flukeOutline3D,
    peduncleOutline3D,
    dorsalFacing,
  };
}

export function project3D(pt: Point3D): { x: number; y: number; depth: number } {
  const s = FOCAL / Math.max(180, FOCAL + pt.z + 110);
  return { x: pt.x * s, y: WATERLINE - pt.y * s, depth: pt.z };
}

export function projectTail(pose: Pose): TailProjection {
  const mesh = buildUnifiedTailMesh(pose);
  const triangles: ProjectedTriangle[] = [];
  let collarMin = Infinity;
  let collarMax = -Infinity;

  const lx = -0.32, ly = 0.80, lz = -0.50;

  for (const tri of mesh.triangles) {
    // Sea-level view: everything below the opaque water surface is hidden.
    if (tri.a.y < -4 && tri.b.y < -4 && tri.c.y < -4) continue;

    const pa = project3D(tri.a);
    const pb = project3D(tri.b);
    const pc = project3D(tri.c);

    const cross2D = (pb.x - pa.x) * (pc.y - pa.y) - (pb.y - pa.y) * (pc.x - pa.x);
    if (cross2D <= 0) continue;

    if ((tri.a.y <= 8 && tri.b.y >= -8) || (tri.b.y <= 8 && tri.a.y >= -8)) {
      if (pa.x < collarMin) collarMin = pa.x;
      if (pa.x > collarMax) collarMax = pa.x;
    }

    const ndotl = Math.max(0, tri.normal.x * lx + tri.normal.y * ly + tri.normal.z * lz);
    const base = tri.dorsal ? 0.22 : 0.36;
    const shade = clamp(base + (ndotl ** 1.15) * 0.78, 0.14, 1.0);
    const depth = (pa.depth + pb.depth + pc.depth) / 3;

    triangles.push({
      a: { x: pa.x, y: pa.y },
      b: { x: pb.x, y: pb.y },
      c: { x: pc.x, y: pc.y },
      depth,
      shade,
      dorsal: tri.dorsal,
    });
  }

  triangles.sort((u, v) => v.depth - u.depth);

  const flukeContour = mesh.flukeOutline3D.map(pt => {
    const p = project3D(pt);
    return { x: p.x, y: p.y };
  });
  const peduncleContour = mesh.peduncleOutline3D.map(pt => {
    const p = project3D(pt);
    return { x: p.x, y: p.y };
  });
  const trailingEdge = mesh.trailingEdge3D
    .map(pt => project3D(pt))
    .filter(p => p.y < WATERLINE - 2)
    .map(p => ({ x: p.x, y: p.y }));

  return {
    triangles,
    flukeContour,
    peduncleContour,
    dorsalFacing: mesh.dorsalFacing,
    trailingEdge,
    waterlineCollar: collarMax - collarMin > 10 ? { x0: collarMin, x1: collarMax } : null,
  };
}

type SimMode = "waiting" | "ready" | "failed" | "settled";

interface Transition {
  outcome: Outcome;
  elapsed: number;
  duration: number;
  from: Pose;
  strategy: "complete-slap" | "settle";
  slapped: boolean;
  resolve: () => void;
}

/**
 * Decoupled hybrid 3D whale simulation + live waterfall curtain & twin-wing/rebound splash physics.
 */
export class WhaleSimulation {
  clock = START;
  waveMs = START;
  pose: Pose = samplePose(START);
  mode: SimMode = "waiting";
  progressValue = 0;

  droplets: Droplet[] = [];
  foams: FoamBurst[] = [];
  wakes: WakePulse[] = [];

  private transition?: Transition;
  private rng = 1;
  private curtainCarry = 0;

  private rand(): number {
    return noise(this.rng++);
  }

  setProgress(value: number) {
    this.progressValue = clamp(Number.isFinite(value) ? value : 0, 0, 1);
  }

  reset() {
    this.settleTransition();
    this.mode = "waiting";
    this.clock = START;
    this.pose = samplePose(START);
    this.droplets.length = 0;
    this.foams.length = 0;
    this.wakes.length = 0;
    this.curtainCarry = 0;
  }

  finish(outcome: Outcome, immediate = false): Promise<void> {
    this.settleTransition();
    if (immediate) {
      this.droplets.length = 0;
      this.foams.length = 0;
      this.wakes.length = 0;
      if (outcome === "failed") {
        this.pose = { ...samplePose(0), sinkY: -260 };
      } else {
        this.clock = CONTACT.time + 380;
        this.pose = samplePose(this.clock);
        this.wakes.push({ x: CONTACT.x, age: 0.36, life: 1.55, strength: 0.95, speed: 185 });
      }
      this.mode = "settled";
      return Promise.resolve();
    }

    const from = { ...this.pose };
    if (outcome === "failed") {
      this.mode = "failed";
      return new Promise<void>(resolve => {
        this.transition = {
          outcome, elapsed: 0, duration: 540, from,
          strategy: "settle", slapped: true, resolve,
        };
      });
    }

    this.mode = "ready";
    const approachingSlap = this.clock >= 2650 && this.clock < CONTACT.time;
    const justSlapped = this.clock >= CONTACT.time && this.clock < CONTACT.time + 900;
    if (approachingSlap) {
      const duration = Math.min(880, (CONTACT.time - this.clock) + 320);
      return new Promise<void>(resolve => {
        this.transition = {
          outcome, elapsed: 0, duration, from,
          strategy: "complete-slap", slapped: false, resolve,
        };
      });
    }
    if (justSlapped) {
      return new Promise<void>(resolve => {
        this.transition = {
          outcome, elapsed: 0, duration: 380, from,
          strategy: "complete-slap", slapped: true, resolve,
        };
      });
    }
    this.wakes.push({ x: 0, age: 0, life: 1.25, strength: 0.58, speed: 170 });
    return new Promise<void>(resolve => {
      this.transition = {
        outcome, elapsed: 0, duration: 400, from,
        strategy: "settle", slapped: true, resolve,
      };
    });
  }

  settleTransition() {
    const tr = this.transition;
    if (!tr) return;
    this.transition = undefined;
    tr.resolve();
  }

  get isActive(): boolean {
    if (this.mode === "waiting" || this.mode === "ready" || this.mode === "failed") return true;
    return this.droplets.length > 0 || this.foams.length > 0 || this.wakes.length > 0;
  }

  step(dtMs: number) {
    const dt = clamp(dtMs, 0, 48);
    const dtSec = dt / 1000;
    this.waveMs += dt * (1 + this.progressValue * 0.25);
    const prevClock = this.clock;

    if (this.transition) {
      const tr = this.transition;
      tr.elapsed += dt;

      if (tr.outcome === "failed") {
        const u = clamp(tr.elapsed / tr.duration, 0, 1);
        const p = ease(u);
        this.pose = {
          baseX: tr.from.baseX,
          baseY: tr.from.baseY,
          pitch: mix(tr.from.pitch, 0.2, p),
          arch: mix(tr.from.arch, 0.05, p),
          flex: mix(tr.from.flex, 0, p),
          twist: mix(tr.from.twist, 0, p),
          bank: mix(tr.from.bank, 0, p),
          asym: mix(tr.from.asym, 0, p),
          sinkY: mix(tr.from.sinkY, -280, p),
        };
        this.stepWater(dtSec, true);
        const waterClear = this.droplets.length === 0 && this.foams.length === 0 && this.wakes.length === 0;
        if (u >= 1 && (waterClear || tr.elapsed >= 1020)) {
          if (tr.elapsed >= 1020) {
            this.droplets.length = 0;
            this.foams.length = 0;
            this.wakes.length = 0;
          }
          this.mode = "settled";
          this.settleTransition();
        }
      } else {
        const u = clamp(tr.elapsed / tr.duration, 0, 1);
        if (tr.strategy === "complete-slap") {
          this.clock = (this.clock + dt) % LOOP;
          this.pose = samplePose(this.clock);
          const impactTrigger = CONTACT.time - 90;
          if (!tr.slapped && prevClock < impactTrigger && this.clock >= impactTrigger) {
            tr.slapped = true;
            this.spawnTwinWingImpact(CONTACT.x);
          }
          if (!tr.slapped) this.emitWaterfallAndSpray(dtSec);
        } else {
          const p = ease(u);
          this.pose = {
            ...tr.from,
            arch: mix(tr.from.arch, 0, p),
            flex: mix(tr.from.flex, 0, p),
            sinkY: mix(tr.from.sinkY, -250, p),
          };
        }
        this.stepWater(dtSec, false);
        if (u >= 1) {
          this.mode = "settled";
          this.settleTransition();
        }
      }
    } else if (this.mode === "waiting") {
      this.clock = (this.clock + dt) % LOOP;
      this.pose = samplePose(this.clock);
      if (prevClock < 380 && this.clock >= 380) {
        this.wakes.push({ x: -12, age: 0, life: 1.35, strength: 0.42, speed: 125 });
      }
      // Fire the primary twin-wing eruption 90ms BEFORE full submersion — the exact instant
      // the broad underside of the flukes kisses the water surface (zero visual lag!).
      const impactTrigger = CONTACT.time - 90;
      if (prevClock < impactTrigger && this.clock >= impactTrigger) {
        this.spawnTwinWingImpact(CONTACT.x);
      }
      this.emitWaterfallAndSpray(dtSec);
      this.stepWater(dtSec, false);
    } else {
      this.stepWater(dtSec, true);
    }
  }

  private emitWaterfallAndSpray(dtSec: number) {
    if (dtSec <= 0) return;

    // Pre-impact waterline boil (3060..3320ms): as the accelerating flukes slice down into
    // the surface, water immediately erupts upward from the entry zone before the main crown!
    if (this.clock >= 3060 && this.clock <= 3320) {
      this.curtainCarry += 180 * dtSec;
      const boilCount = Math.floor(this.curtainCarry);
      this.curtainCarry -= boilCount;
      for (let i = 0; i < boilCount && this.droplets.length < MAX_DROPLETS; i++) {
        const side = i % 2 === 0 ? -1 : 1;
        const u = this.rand();
        this.droplets.push({
          x: CONTACT.x + side * (16 + u * 120),
          y: WATERLINE - 1,
          vx: side * (55 + u * 95),
          vy: 55 + this.rand() * 95,
          age: 0,
          life: 0.42 + this.rand() * 0.25,
          gravity: 480,
          drag: 0.99,
          glyph: SPRAY_GLYPHS[i % SPRAY_GLYPHS.length],
          alpha: 0.28 + this.rand() * 0.45,
          size: 9.2,
        });
      }
      return;
    }

    if (this.clock < 520 || this.clock > 2350) return;
    const mesh = buildUnifiedTailMesh(this.pose);
    if (mesh.trailingEdge3D.length === 0) return;

    const isLiftWaterfall = this.clock < 1580;
    const rate = isLiftWaterfall ? 210 : 75;
    this.curtainCarry += rate * dtSec;
    const count = Math.floor(this.curtainCarry);
    this.curtainCarry -= count;

    for (let i = 0; i < count && this.droplets.length < MAX_DROPLETS; i++) {
      const origin3D = mesh.trailingEdge3D[Math.floor(this.rand() * mesh.trailingEdge3D.length)];
      const origin = project3D(origin3D);
      if (origin.y >= WATERLINE - 2) continue;
      this.droplets.push({
        x: origin.x + (this.rand() - 0.5) * (isLiftWaterfall ? 8 : 14),
        y: origin.y + (this.rand() - 0.5) * 4,
        vx: (this.rand() - 0.5) * (isLiftWaterfall ? 22 : 64),
        vy: isLiftWaterfall ? -20 - this.rand() * 45 : 20 + this.rand() * 55,
        age: 0,
        life: isLiftWaterfall ? 0.52 + this.rand() * 0.25 : 0.44 + this.rand() * 0.22,
        gravity: isLiftWaterfall ? 560 : 720,
        drag: 0.992,
        glyph: isLiftWaterfall
          ? WATERFALL_GLYPHS[Math.floor(this.rand() * WATERFALL_GLYPHS.length)]
          : SPRAY_GLYPHS[Math.floor(this.rand() * SPRAY_GLYPHS.length)],
        alpha: isLiftWaterfall ? 0.26 + this.rand() * 0.44 : 0.20 + this.rand() * 0.40,
        size: isLiftWaterfall ? 9.5 : 9,
      });
    }
  }

  private spawnTwinWingImpact(impactX: number) {
    this.wakes.push({ x: impactX, age: 0, life: 1.75, strength: 1.0, speed: 192 });
    this.foams.push({ x: impactX, age: 0, life: 1.25, spread: 165 });

    const wingBurst = 240;
    for (let i = 0; i < wingBurst && this.droplets.length < MAX_DROPLETS; i++) {
      const side = i % 2 === 0 ? -1 : 1;
      const u = this.rand();
      const isMist = i % 5 === 0;
      const xOff = side * (36 + u * 135);
      const speedBoost = 0.65 + 0.65 * Math.sin(u * Math.PI * 0.9);
      this.droplets.push({
        x: impactX + xOff,
        y: WATERLINE - 1,
        vx: side * (85 + u * 145 + this.rand() * 55),
        vy: (isMist ? 95 : 130) + this.rand() * 145 + 135 * speedBoost,
        age: 0, // zero lag: erupts on the exact contact frame!
        life: isMist ? 1.18 : 1.36,
        gravity: isMist ? 250 : 490,
        drag: isMist ? 0.96 : 0.990,
        glyph: SPRAY_GLYPHS[i % SPRAY_GLYPHS.length],
        alpha: isMist ? 0.22 + this.rand() * 0.28 : 0.38 + this.rand() * 0.58,
        size: isMist ? 8.5 : 10,
      });
    }

    const reboundBurst = 75;
    for (let i = 0; i < reboundBurst && this.droplets.length < MAX_DROPLETS; i++) {
      const spread = (this.rand() - 0.5) * 2;
      const center = 1 - Math.abs(spread);
      this.droplets.push({
        x: impactX + spread * 54,
        y: WATERLINE - 1,
        vx: spread * (42 + this.rand() * 48),
        vy: 85 + this.rand() * 90 + 110 * center * center,
        age: -(0.38 + this.rand() * 0.22),
        life: 0.95,
        gravity: 460,
        drag: 0.988,
        glyph: SPRAY_GLYPHS[i % SPRAY_GLYPHS.length],
        alpha: 0.30 + this.rand() * 0.54,
        size: 9.5,
      });
    }
  }

  private stepWater(dtSec: number, accelerateDecay: boolean) {
    const decayBoost = accelerateDecay ? 2.15 : 1.0;

    let dWrite = 0;
    for (let i = 0; i < this.droplets.length; i++) {
      const d = this.droplets[i];
      const stepDt = dtSec * (accelerateDecay ? 1.55 : 1.0);
      d.age += stepDt;
      if (d.age < 0) {
        if (!accelerateDecay) this.droplets[dWrite++] = d;
        continue;
      }
      d.vx *= d.drag ** (stepDt * 60);
      d.x += d.vx * stepDt;
      d.y -= d.vy * stepDt;
      d.vy -= d.gravity * stepDt;
      if (d.y <= WATERLINE && d.age < d.life) {
        this.droplets[dWrite++] = d;
      }
    }
    this.droplets.length = dWrite;

    let fWrite = 0;
    for (let i = 0; i < this.foams.length; i++) {
      const f = this.foams[i];
      f.age += dtSec * decayBoost;
      if (f.age < f.life) this.foams[fWrite++] = f;
    }
    this.foams.length = fWrite;

    let wWrite = 0;
    for (let i = 0; i < this.wakes.length; i++) {
      const w = this.wakes[i];
      w.age += dtSec * decayBoost;
      if (w.age < w.life) this.wakes[wWrite++] = w;
    }
    this.wakes.length = wWrite;
  }

  wakePulseAt(x: number, row: number): { pulse: number; dist: number } {
    let pulse = 0;
    let dist = Math.abs(x - CONTACT.x);
    for (const w of this.wakes) {
      const d = Math.abs(x - w.x);
      const env = 1 - w.age / w.life;
      const p = Math.exp(-(((d - w.age * w.speed - row * 8) / 52) ** 2)) * env * w.strength;
      if (p > pulse) {
        pulse = p;
        dist = d;
      }
    }
    return { pulse, dist };
  }
}
