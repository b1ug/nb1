/* Visual mating fit for the separately exported official enclosure meshes.
 * The caller supplies common-CAD coordinates, with the diffuser's USB/front
 * min-Y datum aligned to the frame. This is not an engineering assembly file.
 * Only the returned frame derivative changes. Plastic, engraving and source assets
 * are untouched; no cover, trim strip or additional surface is introduced.
 */

const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
const smooth = value => { const t = clamp(value, 0, 1); return t * t * (3 - 2 * t); };

// Grid the projection of each triangle once. An axis-aligned intersection then
// visits only one cell, rather than raycasting every triangle for every vertex.
function intersectionIndex(geometry, axis, cellSize = .5) {
  const position = geometry.getAttribute('position'), index = geometry.getIndex();
  const first = (axis + 1) % 3, second = (axis + 2) % 3;
  const coordinate = (vertex, component) => position.getComponent(vertex, component);
  let minimumU = Infinity, maximumU = -Infinity, minimumV = Infinity, maximumV = -Infinity;
  for (let i = 0; i < position.count; i++) {
    const u = coordinate(i, first), v = coordinate(i, second);
    minimumU = Math.min(minimumU, u); maximumU = Math.max(maximumU, u);
    minimumV = Math.min(minimumV, v); maximumV = Math.max(maximumV, v);
  }
  const width = Math.max(1, Math.floor((maximumU - minimumU) / cellSize) + 1);
  const cells = new Map(), cache = new Map();
  const count = index ? index.count : position.count;
  for (let i = 0; i < count; i += 3) {
    const ids = [0, 1, 2].map(offset => index ? index.getX(i + offset) : i + offset);
    const u = ids.map(vertex => coordinate(vertex, first));
    const v = ids.map(vertex => coordinate(vertex, second));
    const w = ids.map(vertex => coordinate(vertex, axis));
    const du = u[1] - u[0], dv = v[1] - v[0], eu = u[2] - u[0], ev = v[2] - v[0];
    const determinant = du * ev - dv * eu;
    if (Math.abs(determinant) < 1e-12) continue;
    const triangle = [u[0], v[0], w[0], du, dv, w[1] - w[0], eu, ev, w[2] - w[0], 1 / determinant];
    const left = Math.floor((Math.min(...u) - minimumU) / cellSize);
    const right = Math.floor((Math.max(...u) - minimumU) / cellSize);
    const bottom = Math.floor((Math.min(...v) - minimumV) / cellSize);
    const top = Math.floor((Math.max(...v) - minimumV) / cellSize);
    for (let y = bottom; y <= top; y++) for (let x = left; x <= right; x++) {
      const key = x + y * width;
      if (!cells.has(key)) cells.set(key, []);
      cells.get(key).push(triangle);
    }
  }
  let queries = 0;
  return {
    at(a, b) {
      // Public coordinates remain XYZ; the arguments use the two non-ray axes
      // in their cyclic order (Z: X,Y; Y: Z,X).
      const key = `${Math.round(a * 1e6)}:${Math.round(b * 1e6)}`;
      if (cache.has(key)) return cache.get(key);
      queries++;
      if (a < minimumU - 1e-6 || a > maximumU + 1e-6 || b < minimumV - 1e-6 || b > maximumV + 1e-6) return [];
      const x = clamp(Math.floor((a - minimumU) / cellSize), 0, width - 1);
      const y = Math.floor((b - minimumV) / cellSize);
      const hits = [];
      for (const t of cells.get(x + y * width) || []) {
        const u = a - t[0], v = b - t[1];
        const s = (u * t[7] - v * t[6]) * t[9];
        const r = (t[3] * v - t[4] * u) * t[9];
        if (s >= -1e-6 && r >= -1e-6 && s + r <= 1 + 1e-6) hits.push(t[2] + s * t[5] + r * t[8]);
      }
      hits.sort((a, b) => a - b);
      const unique = hits.filter((value, i) => !i || value - hits[i - 1] > 1e-5);
      cache.set(key, unique);
      return unique;
    },
    get queries() { return queries; },
  };
}

// Refine shared edges together, so projecting a curved mating boundary never
// leaves a coarse neighbouring triangle spanning the new contour. The source
// is welded by position; only the USB corner neighbourhood is refined.
function refineFrontCorners(THREE, input, box, edgeLength = .22) {
  const points = [], faces = [], ids = new Map(), p = input.getAttribute('position');
  const index = input.getIndex();
  const id = vertex => {
    const point = new THREE.Vector3().fromBufferAttribute(p, vertex);
    const key = point.toArray().map(v => Math.round(v * 1e6)).join(':');
    if (!ids.has(key)) { ids.set(key, points.length); points.push(point); }
    return ids.get(key);
  };
  for (let i = 0; i < (index?.count ?? p.count); i += 3) {
    faces.push([0,1,2].map(j => id(index ? index.getX(i+j) : i+j)));
  }
  let triangles = faces, subdivisions = 0;
  const edgeKey = (a,b) => a < b ? `${a}:${b}` : `${b}:${a}`;
  const needsSplit = (a,b) => {
    const q = points[a], r = points[b];
    return Math.min(q.y,r.y) < box.min.y + 3.8
      && (Math.min(q.x,r.x) < box.min.x + 3 || Math.max(q.x,r.x) > box.max.x - 3)
      && q.distanceToSquared(r) > edgeLength * edgeLength;
  };
  for (let pass = 0; pass < 10; pass++) {
    const splits = new Map();
    for (const [a,b,c] of triangles) for (const [u,v] of [[a,b],[b,c],[c,a]]) {
      const key = edgeKey(u,v);
      if (needsSplit(u,v) && !splits.has(key)) {
        splits.set(key,points.length); points.push(points[u].clone().add(points[v]).multiplyScalar(.5));
      }
    }
    if (!splits.size) break;
    const next = [];
    for (const [a,b,c] of triangles) {
      const d = splits.get(edgeKey(a,b)), e = splits.get(edgeKey(b,c)), f = splits.get(edgeKey(c,a));
      const count = Number(d !== undefined) + Number(e !== undefined) + Number(f !== undefined);
      if (!count) next.push([a,b,c]);
      else if (count === 3) next.push([a,d,f],[d,b,e],[f,e,c],[d,e,f]);
      else if (count === 1) {
        if (d !== undefined) next.push([a,d,c],[d,b,c]);
        else if (e !== undefined) next.push([b,e,a],[e,c,a]);
        else next.push([c,f,b],[f,a,b]);
      } else if (f === undefined) next.push([b,e,d],[a,d,c],[d,e,c]);
      else if (d === undefined) next.push([c,f,e],[b,e,a],[e,f,a]);
      else next.push([a,d,f],[c,f,b],[f,d,b]);
    }
    subdivisions += splits.size; triangles = next;
  }
  const result = new THREE.BufferGeometry();
  result.setAttribute('position',new THREE.Float32BufferAttribute(points.flatMap(p=>p.toArray()),3));
  result.setIndex(triangles.flat());
  return {geometry: result, subdivisions, edgeLength};
}

export function fitFrameToDiffuser(THREE, frameGeometry, alignedDiffuserGeometry) {
  for (const input of [frameGeometry, alignedDiffuserGeometry]) {
    const position = input?.getAttribute('position');
    if (!position || position.itemSize !== 3 || position.count < 3) throw new TypeError('Expected two enclosure geometries');
  }
  const frameBox = new THREE.Box3().setFromBufferAttribute(frameGeometry.getAttribute('position'));
  const shellBox = new THREE.Box3().setFromBufferAttribute(alignedDiffuserGeometry.getAttribute('position'));
  const width = frameBox.max.x - frameBox.min.x, depth = frameBox.max.y - frameBox.min.y;
  const height = frameBox.max.z - frameBox.min.z;
  if (width <= 0 || depth <= 0 || height <= 0) throw new TypeError('Frame has no enclosing volume');
  const middleX = (frameBox.min.x + frameBox.max.x) / 2;
  const middleZ = (frameBox.min.z + frameBox.max.z) / 2;
  const inset = Math.min(.01, width / 1000);
  const exteriorInset = .02;
  let overlap = .006; // A visual, sub-pixel internal bite, not a trim surface.
  const frameZ = intersectionIndex(frameGeometry, 2);
  const frameY = intersectionIndex(frameGeometry, 1);
  const shellZ = intersectionIndex(alignedDiffuserGeometry, 2);
  const shellY = intersectionIndex(alignedDiffuserGeometry, 1);
  const rightRailX = frameBox.max.x - inset;

  function cavity(hits) {
    const below = hits.filter(value => value < middleZ - 1e-5);
    const above = hits.filter(value => value > middleZ + 1e-5);
    if (below.length < 2 || above.length < 2) return null;
    return {low: below[below.length - 1], high: above[0]};
  }

  // The closed return at the back has a genuine seating datum. Matching its
  // tip avoids dividing a collapsed frame end by a still-open plastic cavity.
  const backHits = shellY.at(middleZ, rightRailX);
  const rearCandidates = backHits.filter(value => value > frameBox.max.y - height);
  const shellBackTip = rearCandidates.length >= 2 ? rearCandidates[rearCandidates.length - 2] : frameBox.max.y;
  // The source frame's rear facets are chords of the closed plastic return.
  // Project that actual rear boundary into the mating surface below; its
  // 20-micron longitudinal bite closes the last point without a global Z pad.
  const rearOverlap = .02;
  const backExtension = clamp(shellBackTip + rearOverlap - frameBox.max.y, -height * .15, height * .15);
  const backStart = frameBox.max.y - Math.min(5, depth * .3);
  const frontBand = Math.min(3.7, depth * .22);
  let frameSamples = 0, shellSamples = 0, missingFrameSamples = 0, missingShellSamples = 0;

  // Find an exterior seating rail. The short USB roundovers are narrower than
  // the long side; bracket their real footprint instead of using bbox corners.
  function railSample(index, y, plastic) {
    const parse = hits => plastic ? cavity(hits) : hits.length >= 2 ? {low: hits[0], high: hits[hits.length - 1]} : null;
    let outside = rightRailX, inside = null, result = null;
    const spacing = width / 64;
    for (let x = rightRailX; x >= middleX; x -= spacing) {
      const sample = parse(index.at(x, y));
      if (sample) { inside = x; result = sample; break; }
      outside = x;
    }
    if (inside === null) return null;
    if (inside < rightRailX) {
      for (let i = 0; i < 15; i++) {
        const x = (inside + outside) / 2;
        if (parse(index.at(x, y))) inside = x;
        else outside = x;
      }
      result = parse(index.at(Math.max(middleX, inside - inset), y)) || result;
    }
    return result;
  }

  const frameCache = new Map(), shellCache = new Map();
  function frameSeat(y) {
    const sampleY = clamp(y, frameBox.min.y + .003, frameBox.max.y - .003);
    const key = Math.round(sampleY * 1e6);
    if (frameCache.has(key)) return frameCache.get(key);
    frameSamples++;
    const result = railSample(frameZ, sampleY, false);
    if (!result) missingFrameSamples++;
    const value = result || {low: frameBox.min.z, high: frameBox.max.z};
    frameCache.set(key, value);
    return value;
  }
  function shellSeat(y) {
    const sampleY = clamp(y, shellBox.min.y + .003, shellBackTip - .003);
    const key = Math.round(sampleY * 1e6);
    if (shellCache.has(key)) return shellCache.get(key);
    shellSamples++;
    let result = railSample(shellZ, sampleY, true);
    if (!result && sampleY < shellBox.min.y + .15) {
      // The very first rounded point has no finite cavity. A nearby finite
      // section supplies a continuous endpoint rather than an invented plate.
      result = railSample(shellZ, shellBox.min.y + .15, true);
    }
    if (!result) missingShellSamples++;
    const value = result || {low: frameBox.min.z, high: frameBox.max.z};
    shellCache.set(key, value);
    return value;
  }

  let geometry = frameGeometry.clone();
  const position = geometry.getAttribute('position');
  const fractions = new Float32Array(position.count);
  let maxSeatCorrection = 0, maxFrontRetreat = 0, maxBackShift = 0, maxDisplacement = 0;
  for (let i = 0; i < position.count; i++) {
    const x = position.getX(i), y = position.getY(i), z = position.getZ(i);
    let newY = y;
    // Retreat a roundover along the USB axis instead of squeezing X: this
    // retains the connector opening's width and never inverts a thin wall.
    if (y < frameBox.min.y + frontBand && Math.abs(x - middleX) > width * .5 - 2.7) {
      const sampleX = clamp(x, frameBox.min.x + inset, frameBox.max.x - inset);
      const original = frameY.at(middleZ, sampleX)[0];
      const upper = shellY.at(frameBox.max.z - .01, sampleX)[0];
      const lower = shellY.at(frameBox.min.z + .01, sampleX)[0];
      if ([original, upper, lower].every(Number.isFinite)) {
        const required = Math.max(upper, lower) - original;
        if (original < frameBox.min.y + frontBand && required > 0 && required < 1) {
          const weight = 1 - smooth((y - original) / .9);
          const retreat = required * weight;
          newY += retreat;
          maxFrontRetreat = Math.max(maxFrontRetreat, retreat);
        }
      }
    }
    const backShift = backExtension * smooth((y - backStart) / (frameBox.max.y - backStart));
    newY += backShift;
    maxBackShift = Math.max(maxBackShift, Math.abs(backShift));
    const oldSeat = frameSeat(y), newSeat = shellSeat(newY);
    const fraction = clamp((z - oldSeat.low) / Math.max(1e-6, oldSeat.high - oldSeat.low), 0, 1);
    fractions[i] = fraction;
    const newZ = newSeat.low - overlap + fraction * (newSeat.high - newSeat.low + 2 * overlap);
    position.setXYZ(i, x, newY, newZ);
    maxSeatCorrection = Math.max(maxSeatCorrection, Math.abs(newZ - z));
    maxDisplacement = Math.max(maxDisplacement, Math.hypot(newY - y, newZ - z));
  }

  // At the final return, matching only per-Y heights can pinch the source tip
  // before the cavity closes. Fit the original back boundary along Y at its
  // *new Z*, and fade that correction inward. X and the connector hole's width
  // remain unchanged; no additional boundary, face or trim is created.
  const rawPosition = frameGeometry.getAttribute('position');
  let maximumRearProjection = 0;
  for (let i = 0; i < position.count; i++) {
    const oldY = rawPosition.getY(i), oldZ = rawPosition.getZ(i);
    if (oldY < backStart) continue;
    if (Math.abs(position.getX(i) - middleX) < width * .5 - 1.21) continue;
    const sampleX = clamp(position.getX(i), frameBox.min.x + inset, frameBox.max.x - inset);
    const originalBack = frameY.at(oldZ, sampleX).at(-1);
    const plasticBack = shellY.at(position.getZ(i), sampleX);
    if (!Number.isFinite(originalBack) || plasticBack.length < 2) continue;
    const desiredY = plasticBack[plasticBack.length - 2] + rearOverlap;
    if (desiredY <= backStart) continue;
    if (oldY > originalBack + .01 || Math.abs(desiredY - position.getY(i)) > .3) continue;
    const weight = 1 - smooth((originalBack - oldY) / .7);
    const correction = (desiredY - position.getY(i)) * weight;
    position.setY(i, position.getY(i) + correction);
    maximumRearProjection = Math.max(maximumRearProjection, Math.abs(correction));
  }
  // Measure actual derived triangles, not only the intended vertex mapping.
  let verificationQueries = 0;
  function measure() {
    const fittedZ = intersectionIndex(geometry, 2);
    let maximumAirGap = 0, maximumOverlap = 0, residualSamples = 0;
    const longitudinalSamples = [];
    for (let y = frameBox.min.y + frontBand; y < shellBackTip - .003; y += .075) longitudinalSamples.push(y);
    for (const distance of [.08, .04, .02, .01, .005, .003]) longitudinalSamples.push(shellBackTip - distance);
    for (const x of [frameBox.min.x + exteriorInset + inset, frameBox.max.x - exteriorInset - inset]) {
      for (const y of longitudinalSamples) {
        const metal = fittedZ.at(x, y), plastic = cavity(shellZ.at(x, y));
        if (metal.length < 2 || !plastic) continue;
        const upperGap = plastic.high - metal[metal.length - 1];
        const lowerGap = metal[0] - plastic.low;
        maximumAirGap = Math.max(maximumAirGap, upperGap, lowerGap);
        maximumOverlap = Math.max(maximumOverlap, -upperGap, -lowerGap);
        residualSamples += 2;
      }
    }
    verificationQueries += fittedZ.queries;
    return {maximumAirGap, maximumOverlap, samples: residualSamples};
  }
  let residual = measure();
  const initialAirGap = residual.maximumAirGap;
  // Flat source triangles approximate a curved seat by chords. A measured,
  // bounded internal bite closes those sub-pixel chord errors without adding
  // triangles or modifying the plastic. It is recorded, never hidden as CAD fit.
  if (residual.maximumAirGap > .002) {
    // Do not enlarge the whole-frame Z bite to solve a local rear mismatch.
    const correction = Math.min(.0328, residual.maximumAirGap + .004);
    overlap += correction;
    for (let i = 0; i < position.count; i++) {
      position.setZ(i, position.getZ(i) + (2 * fractions[i] - 1) * correction);
    }
    residual = measure();
  }
  // The moulded diffuser and source metal share exactly the same exterior
  // X planes. Their *internal* overlap is a mounting seat, but two coincident
  // visible planes do not define material ownership: the depth buffer picks
  // alternating source triangles (the former USB-facing teeth). Recess only
  // the exterior metal wall by 0.02 mm, blending to zero before the connector
  // aperture. This gives the plastic a real, unambiguous outside surface;
  // it does not add a cover or change the source mould/engraving.
  for (let i = 0; i < position.count; i++) {
    const x = position.getX(i), distance = Math.min(x - frameBox.min.x, frameBox.max.x - x);
    const weight = 1 - smooth(distance / 1.5);
    position.setX(i, x + Math.sign(middleX - x) * exteriorInset * weight);
  }
  const originalPosition = frameGeometry.getAttribute('position');
  maxSeatCorrection = 0; maxDisplacement = 0;
  for (let i = 0; i < position.count; i++) {
    const dx = position.getX(i) - originalPosition.getX(i), dy = position.getY(i) - originalPosition.getY(i), dz = position.getZ(i) - originalPosition.getZ(i);
    if (originalPosition.getY(i) >= backStart) maxBackShift = Math.max(maxBackShift, Math.abs(dy));
    maxSeatCorrection = Math.max(maxSeatCorrection, Math.abs(dz));
    maxDisplacement = Math.max(maxDisplacement, Math.hypot(dx, dy, dz));
  }
  // The front roundover must be seated at the *final* Z coordinate. The old
  // fixed-Z retreat ran before height fitting and left front-facing triangles
  // ahead of the moulded skin; those appeared as teeth even after X inset.
  const refined = refineFrontCorners(THREE, geometry, frameBox);
  geometry.dispose(); geometry = refined.geometry;
  const finalPosition = geometry.getAttribute('position');
  let frontProjectedVertices = 0, maximumFrontProjection = 0;
  const frontInset = .025;
  for (let i = 0; i < finalPosition.count; i++) {
    const x = finalPosition.getX(i), y = finalPosition.getY(i), z = finalPosition.getZ(i);
    if (y >= frameBox.min.y + 3.8) continue;
    const front = shellY.at(z, x)[0];
    if (!Number.isFinite(front) || front >= frameBox.min.y + 3.8) continue;
    const desired = front + frontInset;
    if (y < desired) {
      finalPosition.setY(i,desired);
      maximumFrontProjection = Math.max(maximumFrontProjection,desired-y);
      frontProjectedVertices++;
    }
  }
  // A continuous 0.06-mm longitudinal seat recess bridges the inner lip's
  // silhouette, where a ray suddenly changes from the front cap to the rear
  // return. Applying it to the complete front neighbourhood avoids a jump in
  // displacement at that material boundary; it fades to zero inside the body.
  const frontSeatRecess = .06;
  for (let i = 0; i < finalPosition.count; i++) {
    const y = finalPosition.getY(i);
    if (y >= frameBox.min.y + 3.8) continue;
    finalPosition.setY(i,y + frontSeatRecess * (1 - smooth((y - frameBox.min.y) / 3.8)));
  }
  // Report the final returned triangles, after side inset and front projection.
  residual = measure();
  finalPosition.needsUpdate = true;
  geometry.computeVertexNormals(); // The caller can subsequently rebuild its crease-preserving normals and UVs.
  geometry.computeBoundingBox(); geometry.computeBoundingSphere();
  const stats = {
    kind: 'derived-frame-seating-fit/v2',
    units: 'source CAD units, interpreted as mm; not engineering-certified',
    datum: {front: 'diffuser min-Y aligned to frame min-Y', axes: 'X width; Y USB-to-back; Z thickness',
      frameFrontY: frameBox.min.y, diffuserFrontY: shellBox.min.y, frameBackY: frameBox.max.y,
      diffuserInnerBackY: shellBackTip},
    contactOverlap: overlap,
    exteriorInset,
    frontInset,
    frontSeatRecess,
    frontProjectedVertices,
    maximumFrontProjection,
    tessellation: {sourceTriangles: (frameGeometry.getIndex()?.count ?? frameGeometry.getAttribute('position').count) / 3, triangles: geometry.getIndex().count / 3, splitEdges: refined.subdivisions, maximumFrontCornerEdge: refined.edgeLength},
    rearOverlap,
    maximumRearProjection,
    maximumSeatCorrection: maxSeatCorrection,
    preRefinementMaximumFrontRetreat: maxFrontRetreat,
    frontDisplacementScope: 'Original-vertex seating before refinement; final front surface also receives the separately recorded projection and continuous seat recess.',
    maximumFrontRetreatUpperBound: maxFrontRetreat + maximumFrontProjection + frontSeatRecess,
    maximumBackExtension: maxBackShift,
    preRefinementMaximumVertexDisplacement: maxDisplacement,
    maximumVertexDisplacementUpperBound: maxDisplacement + maximumFrontProjection + frontSeatRecess,
    residual: {...residual, beforeChordCorrectionAirGap: initialAirGap,
      scope: 'final returned triangles after all fits: both long-side contact rails at metal exterior inset + 0.01, rear bend and final tip down to 0.003 source units before the plastic inner tip',
      longitudinalSpacing: .075, finalTipDistances: [.08, .04, .02, .01, .005, .003]},
    sampling: {frameSeats: frameSamples, diffuserSeats: shellSamples, missingFrameSeats: missingFrameSamples,
      missingDiffuserSeats: missingShellSamples, indexedIntersections: frameZ.queries + frameY.queries + shellZ.queries + shellY.queries + verificationQueries},
    preserves: ['input geometries', 'diffuser vertices and engraving', 'connector aperture inner-wall X coordinates', 'connector opening width'],
  };
  geometry.userData = {...geometry.userData, enclosureFit: stats};
  return {geometry, stats};
}
