/* blink(1) mk2 object for the isolated local design study.
 * Derived from ThingM/todbot blink1 official enclosure CAD at fixed commit
 * 101abe3599c661c4dd4d1204b4d50fb592293143. Changes: visual assembly alignment,
 * derived frame contact fitting, crease-preserving shading normals, procedural finishes, colour illustration
 * and supplemental USB geometry.
 * CAD derivatives: CC BY-SA 4.0; see model-assets/LICENSE.txt and model-assets/source.json.
 * THREE and the raw official STL geometries are supplied by the caller.
 * The diffuser is continuous; LED 1 and LED 2 illuminate opposite PCB faces.
 * The project author's original LED table identifies blink(1) as LED 1/top
 * and ThingM as LED 2/bottom; that identity is fixed in object coordinates.
 * Colour rendering is a screen illustration, never an optical measurement.
 */
import {fitFrameToDiffuser} from './enclosure-fit.js';

export function createBlink1Model(THREE, { diffuserGeometry, frameGeometry } = {}) {
  const group = new THREE.Group();
  group.name = 'blink(1) mk2';
  const geometries = new Set(), materials = new Set(), textures = new Set();
  const geometry = value => { geometries.add(value); return value; };
  const material = value => { materials.add(value); return value; };
  const texture = value => { textures.add(value); return value; };
  let disposed = false, lastColors = '';

  function add(name, shape, surface, parent = group) {
    const mesh = new THREE.Mesh(geometry(shape), material(surface));
    mesh.name = name;
    mesh.castShadow = true;
    mesh.receiveShadow = true;
    parent.add(mesh);
    return mesh;
  }
  function bounds(shape) {
    return new THREE.Box3().setFromBufferAttribute(shape.getAttribute('position'));
  }
  function roundedRectangle(length, width, radius) {
    const path = new THREE.Shape(), x = -length / 2, z = -width / 2;
    const r = Math.min(radius, length / 2, width / 2);
    path.moveTo(x + r, z);
    path.lineTo(x + length - r, z);
    path.quadraticCurveTo(x + length, z, x + length, z + r);
    path.lineTo(x + length, z + width - r);
    path.quadraticCurveTo(x + length, z + width, x + length - r, z + width);
    path.lineTo(x + r, z + width);
    path.quadraticCurveTo(x, z + width, x, z + width - r);
    path.lineTo(x, z + r);
    path.quadraticCurveTo(x, z, x + r, z);
    return path;
  }
  function plate(path, thickness, edgeRadius = 0) {
    const bevel = Math.min(edgeRadius, thickness * .28);
    const result = new THREE.ExtrudeGeometry(path, {
      depth: thickness - bevel * 2, bevelEnabled: bevel > 0,
      bevelThickness: bevel, bevelSize: bevel, bevelSegments: 3,
      curveSegments: 16, steps: 1,
    });
    // Local x/y form the footprint; extrusion becomes upward world y.
    result.rotateX(-Math.PI / 2);
    if (bevel) result.translate(0, bevel, 0);
    return result;
  }
  function creasedNormals(input, threshold = Math.PI / 6) {
    // Smooth curved CAD facets, but keep the actual 90-degree engraving and
    // sheet edges sharp. Angle weighting avoids skinny STL triangles pulling
    // a moulded surface's highlight towards a large neighbouring triangle.
    const position = input.getAttribute('position'), index = input.getIndex();
    const count = index ? index.count : position.count;
    const faces = [], adjoining = new Map(), cosine = Math.cos(threshold);
    const point = i => new THREE.Vector3().fromBufferAttribute(position, index ? index.getX(i) : i);
    const key = p => `${Math.round(p.x * 100000)}:${Math.round(p.y * 100000)}:${Math.round(p.z * 100000)}`;
    for (let i = 0; i < count; i += 3) {
      const corners = [point(i), point(i + 1), point(i + 2)];
      const normal = corners[1].clone().sub(corners[0]).cross(corners[2].clone().sub(corners[0])).normalize();
      const face = { corners, normal };
      faces.push(face);
      for (let c = 0; c < 3; c++) {
        const a = corners[(c + 1) % 3].clone().sub(corners[c]).normalize();
        const b = corners[(c + 2) % 3].clone().sub(corners[c]).normalize();
        const weight = Math.acos(Math.max(-1, Math.min(1, a.dot(b))));
        const id = key(corners[c]);
        if (!adjoining.has(id)) adjoining.set(id, []);
        adjoining.get(id).push({ normal, weight });
      }
    }
    const points = [], normals = [];
    for (const face of faces) for (const corner of face.corners) {
      const normal = new THREE.Vector3();
      for (const neighbour of adjoining.get(key(corner))) {
        if (face.normal.dot(neighbour.normal) >= cosine) normal.addScaledVector(neighbour.normal, neighbour.weight);
      }
      if (normal.lengthSq() < .000001) normal.copy(face.normal); else normal.normalize();
      points.push(corner.x, corner.y, corner.z); normals.push(normal.x, normal.y, normal.z);
    }
    const result = new THREE.BufferGeometry();
    result.setAttribute('position', new THREE.Float32BufferAttribute(points, 3));
    result.setAttribute('normal', new THREE.Float32BufferAttribute(normals, 3));
    return result;
  }

  let shell, frame, shellBox, frameBox, unit;
  const hasCAD = Boolean(diffuserGeometry?.getAttribute('position') && frameGeometry?.getAttribute('position'));
  if (hasCAD) {
    const frameCAD = bounds(frameGeometry), diffuserCAD = bounds(diffuserGeometry);
    const center = frameCAD.getCenter(new THREE.Vector3());
    const rotate = new THREE.Matrix4().makeRotationY(Math.PI / 2);
    const rotated = diffuserCAD.clone().applyMatrix4(rotate);
    const offset = center.clone().sub(rotated.getCenter(new THREE.Vector3()));
    // The USB/front opening is the shared longitudinal datum. Centering the
    // unequal-length bounding boxes shifted the plastic over the USB corner.
    offset.y = frameCAD.min.y - rotated.min.y;
    const diffuserToFrame = new THREE.Matrix4().makeTranslation(offset.x, offset.y, offset.z).multiply(rotate);
    const alignedDiffuser = diffuserCAD.clone().applyMatrix4(diffuserToFrame);
    // The files have different export axes. Align width/thickness centers and
    // the real front opening, then fit only the derived metal seating edges.
    // Source CAD and plastic/engraving vertices are never edited.
    // A proper half-turn fixes blink(1) at +Y and ThingM at -Y, according to
    // the project author's original LED address table. Never swap colours to
    // compensate for the camera. The USB is built separately with its roof up.
    const world = new THREE.Matrix4().set(
      0, 1, 0, -center.y,
      0, 0, -1, alignedDiffuser.max.z,
      -1, 0, 0, center.x,
      0, 0, 0, 1,
    );
    const rawShellBox = alignedDiffuser.clone().applyMatrix4(world);
    const rawFrameBox = frameCAD.clone().applyMatrix4(world);
    // Keep CAD proportions intact and normalize the complete object to ~4.4.
    // The plug exits the low-Y CAD end, whose aperture is ~12.38 mm wide.
    unit = 4.4 / (rawShellBox.max.x - rawFrameBox.min.x + 11);
    const normalized = new THREE.Matrix4().makeScale(unit, unit, unit).multiply(world);
    const seatingShell = diffuserGeometry.clone().applyMatrix4(diffuserToFrame);
    let seating;
    try { seating = fitFrameToDiffuser(THREE, frameGeometry, seatingShell); }
    finally { seatingShell.dispose(); }
    const transformedFrame = seating.geometry.applyMatrix4(normalized);
    frame = creasedNormals(transformedFrame);
    transformedFrame.dispose();
    const transformed = diffuserGeometry.clone().applyMatrix4(normalized.clone().multiply(diffuserToFrame));
    shell = creasedNormals(transformed);
    transformed.dispose();
    shellBox = bounds(shell); frameBox = bounds(frame);
    group.userData.geometrySource = 'official blink1mk2 enclosure v3k STL; derived metal seating fit';
    group.userData.assemblyAlignment = 'front datum plus derived frame seating fit; not an engineering assembly constraint';
    group.userData.enclosureFit = seating.stats;
  } else {
    // A small loading/failure fallback. The reviewed renderer supplies both
    // official geometries; this shape is never identified as the official CAD.
    unit = .136;
    frame = plate(roundedRectangle(2.72, 2.31, .35), .76);
    frame.translate(0, .32, 0);
    const cap = roundedRectangle(3.08, 2.31, .43);
    shell = new THREE.ExtrudeGeometry(cap, { depth: 1.13, bevelEnabled: true, bevelThickness: .11, bevelSize: .055, bevelSegments: 4, curveSegments: 16 });
    shell.rotateX(-Math.PI / 2); shell.translate(0, .11, 0);
    shellBox = bounds(shell); frameBox = bounds(frame);
    group.userData.geometrySource = 'approximate loading fallback';
  }

  const shellSize = shellBox.getSize(new THREE.Vector3());
  const midY = (frameBox.min.y + frameBox.max.y) / 2;
  function finishTexture(kind) {
    const size = 256, data = new Uint8Array(size * size * 4);
    const hash = n => { let v = Math.imul(n ^ 0x9e3779b9, 0x85ebca6b); v ^= v >>> 13; return (v >>> 0) / 4294967295; };
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
      const offset = (y * size + x) * 4;
      const grain = hash(y + 631) * 2 - 1;
      const fine = hash(x + y * 65537) * 2 - 1;
      if (kind === 'metal-normal') {
        data[offset] = Math.round(128 + fine * 2);
        data[offset + 1] = Math.round(128 + grain * 21 + fine * 2);
        data[offset + 2] = 254;
      } else if (kind === 'frost-normal') {
        data[offset] = Math.round(128 + fine * 8);
        data[offset + 1] = Math.round(128 + (hash(offset + 941) * 2 - 1) * 8);
        data[offset + 2] = 255;
      } else {
        const value = kind === 'metal-roughness' ? 210 + grain * 22 + fine * 3 : 223 + fine * 15;
        data[offset] = data[offset + 1] = data[offset + 2] = Math.round(value);
      }
      data[offset + 3] = 255;
    }
    const result = texture(new THREE.DataTexture(data, size, size, THREE.RGBAFormat));
    result.wrapS = result.wrapT = THREE.RepeatWrapping;
    result.magFilter = THREE.LinearFilter;
    result.minFilter = THREE.LinearMipmapLinearFilter;
    result.generateMipmaps = true; result.needsUpdate = true;
    return result;
  }
  const metalNormal = finishTexture('metal-normal'), metalRoughness = finishTexture('metal-roughness');
  const frostNormal = finishTexture('frost-normal'), frostRoughness = finishTexture('frost-roughness');
  function metalUV(shape, wrapSides = false) {
    const p = shape.getAttribute('position'), n = shape.getAttribute('normal');
    const values = [];
    const box = bounds(shape), center = box.getCenter(new THREE.Vector3()), size = box.getSize(new THREE.Vector3());
    const radiusX = Math.max(.0001, size.x / 2), radiusZ = Math.max(.0001, size.z / 2);
    const perimeterScale = 2 * (size.x + size.z) * 3 / (Math.PI * 2);
    // Every supplied metal surface is non-indexed, so a UV seam can be
    // unwrapped per triangle without changing the authored CAD vertices.
    for (let i = 0; i < p.count; i += 3) {
      const face = new THREE.Vector3();
      for (let c = 0; c < 3; c++) face.add(new THREE.Vector3().fromBufferAttribute(n, i + c));
      face.normalize();
      if (wrapSides && Math.abs(face.y) < .75) {
        const angles = [0, 1, 2].map(c => Math.atan2((p.getZ(i + c) - center.z) / radiusZ, (p.getX(i + c) - center.x) / radiusX));
        const crossesSeam = Math.max(...angles) - Math.min(...angles) > Math.PI;
        for (let c = 0; c < 3; c++) {
          const angle = crossesSeam && angles[c] < 0 ? angles[c] + Math.PI * 2 : angles[c];
          values.push(angle * perimeterScale, p.getY(i + c) * 3);
        }
      } else {
        // Choose a plane for the whole triangle, never independently for its
        // corners. A per-vertex dominant-axis switch made the former bright
        // vertical stripe by reversing the tangent frame inside faces 471/472.
        const a = Math.abs(face.x), b = Math.abs(face.y), c = Math.abs(face.z);
        for (let corner = 0; corner < 3; corner++) {
          const vertex = i + corner, x = p.getX(vertex), y = p.getY(vertex), z = p.getZ(vertex);
          if (b > a && b > c) values.push(x * 3, z * 3);
          else if (a > c) values.push(z * 3, y * 3);
          else values.push(x * 3, y * 3);
        }
      }
    }
    shape.setAttribute('uv', new THREE.Float32BufferAttribute(values, 2));
    return shape;
  }
  const positions = shell.getAttribute('position'), uv = [];
  for (let i = 0; i < positions.count; i++) {
    const u = (positions.getX(i) - shellBox.min.x) / shellSize.x;
    const across = (positions.getZ(i) - shellBox.min.z) / shellSize.z;
    // UVs now carry only the fine frosted finish. Light uses continuous model
    // coordinates below, so neither UV chart nor a triangle edge divides it.
    uv.push(u, across);
  }
  shell.setAttribute('uv', new THREE.Float32BufferAttribute(uv, 2));

  const plastic = material(new THREE.MeshPhysicalMaterial({
    color: 0xf8faf7,
    metalness: 0, roughness: .47, roughnessMap: frostRoughness, envMapIntensity: .32,
    normalMap: frostNormal, normalScale: new THREE.Vector2(.11, .11),
    transmission: .12, thickness: .13, ior: 1.44,
    attenuationColor: new THREE.Color(0xf5faf7), attenuationDistance: 1.6,
    clearcoat: .035, clearcoatRoughness: .42, specularIntensity: .75,
    emissive: new THREE.Color(0xffffff), emissiveIntensity: 2.15,
  }));
  // Both LEDs illuminate the same continuous volume. Work in linear light:
  // contributions add before tone mapping, while distance gives each main
  // face its own dominant source and allows a soft overlap around the rim.
  // These widths are visual scattering parameters, not measured optics.
  const lightField = {
    uniforms: {
      nb1SourceOne: {value: new THREE.Color(0, 0, 0)},
      nb1SourceTwo: {value: new THREE.Color(0, 0, 0)},
      nb1ShellMin: {value: shellBox.min.clone()},
      nb1ShellSize: {value: shellSize.clone()},
    },
    fragment: `
      uniform vec3 nb1SourceOne;
      uniform vec3 nb1SourceTwo;
      uniform vec3 nb1ShellMin;
      uniform vec3 nb1ShellSize;
      vec3 nb1LightField(vec3 localPosition) {
        vec3 p = (localPosition - nb1ShellMin) / nb1ShellSize;
        vec2 plane = (p.xz - 0.5) * 2.0 - vec2(0.12, -0.08);
        float radius = dot(plane * plane, vec2(1.8, 2.2));
        float radial = 0.72 * exp(-0.55 * radius) + 0.28 * exp(-3.2 * radius);
        float upper = exp(-8.0 * (p.y - 0.8) * (p.y - 0.8));
        float lower = exp(-8.0 * (p.y - 0.2) * (p.y - 0.2));
        return 1.38 * radial * (nb1SourceOne * upper + nb1SourceTwo * lower);
      }
    `,
  };
  plastic.userData.lightField = lightField;
  plastic.onBeforeCompile = shader => {
    Object.assign(shader.uniforms, lightField.uniforms);
    shader.vertexShader = shader.vertexShader
      .replace('#include <common>', '#include <common>\nvarying vec3 nb1LocalPosition;')
      .replace('#include <begin_vertex>', '#include <begin_vertex>\nnb1LocalPosition = position;');
    shader.fragmentShader = shader.fragmentShader
      .replace('#include <common>', `#include <common>\nvarying vec3 nb1LocalPosition;\n${lightField.fragment}`)
      .replace('#include <color_fragment>', `#include <color_fragment>
        vec3 nb1Radiance = nb1LightField(nb1LocalPosition);
        float nb1Strength = max(nb1Radiance.r, max(nb1Radiance.g, nb1Radiance.b));
        vec3 nb1Hue = nb1Strength > 0.0 ? nb1Radiance / nb1Strength : vec3(1.0);
        float nb1PeakOne = max(nb1SourceOne.r, max(nb1SourceOne.g, nb1SourceOne.b));
        float nb1PeakTwo = max(nb1SourceTwo.r, max(nb1SourceTwo.g, nb1SourceTwo.b));
        float nb1Peak = max(nb1PeakOne, nb1PeakTwo);
        float nb1Coverage = clamp(nb1Strength / max(nb1Peak, 0.000001), 0.0, 1.0);
        float nb1Tint = 0.94 * pow(clamp(nb1Peak, 0.0, 1.0), 0.15) * nb1Coverage;
        // Keep the lit core from being washed out by the milky reflection.
        // This surface tint is separate from the additive linear emission.
        vec3 nb1SurfaceTint = mix(vec3(1.0), pow(nb1Hue, vec3(1.0 / 2.2)), nb1Tint);
        diffuseColor.rgb *= pow(nb1SurfaceTint, vec3(2.2));
      `)
      .replace('#include <emissivemap_fragment>', 'totalEmissiveRadiance *= nb1Radiance;');
  };
  plastic.customProgramCacheKey = () => 'nb1-continuous-scattering-v1';
  const graphite = material(new THREE.MeshPhysicalMaterial({
    color: 0x91999f, metalness: .94, roughness: .64,
    roughnessMap: metalRoughness, normalMap: metalNormal, normalScale: new THREE.Vector2(.08, .1),
    anisotropy: 0,
  }));
  const steel = material(new THREE.MeshPhysicalMaterial({
    color: 0xb4bcc0, metalness: .96, roughness: .54,
    roughnessMap: metalRoughness, normalMap: metalNormal, normalScale: new THREE.Vector2(.08, .1), anisotropy: 0,
  }));
  const edgeSteel = material(new THREE.MeshStandardMaterial({ color: 0xc4c9cb, metalness: .9, roughness: .55 }));
  const darkPolymer = material(new THREE.MeshStandardMaterial({ color: 0x151d22, metalness: 0, roughness: .84 }));
  const contacts = material(new THREE.MeshStandardMaterial({ color: 0xbca16b, metalness: .88, roughness: .31 }));
  add('Original frame — CAD details retained', metalUV(frame, true), graphite);
  add('One continuous milky diffuser', shell, plastic);

  // USB-A shell: open at the end, with genuine holes cut through its roof.
  // Values below are millimetres before normalization to the CAD assembly.
  const plug = new THREE.Group(); plug.name = 'USB-A plug'; group.add(plug);
  const plugEnd = frameBox.min.x - 11 * unit;
  const plugRoot = frameBox.min.x + .65 * unit;
  const plugLength = plugRoot - plugEnd;
  const plugCenter = (plugRoot + plugEnd) / 2;
  const plugWidth = 12 * unit, plugHeight = 4.5 * unit, wall = .26 * unit;
  const plugBottom = midY - plugHeight / 2;
  const roof = roundedRectangle(plugLength, plugWidth, .18 * unit);
  for (const z of [-3.1, 3.1]) {
    const hole = new THREE.Path(), x = -.9 * unit, centerZ = z * unit;
    const halfX = 1.22 * unit, halfZ = 1.15 * unit;
    hole.moveTo(x - halfX, centerZ - halfZ);
    hole.lineTo(x - halfX, centerZ + halfZ);
    hole.lineTo(x + halfX, centerZ + halfZ);
    hole.lineTo(x + halfX, centerZ - halfZ);
    hole.closePath(); roof.holes.push(hole);
  }
  const roofMesh = add('Perforated steel roof', metalUV(plate(roof, wall, .065 * unit)), steel, plug);
  roofMesh.position.set(plugCenter, plugBottom + plugHeight - wall, 0);
  function box(name, x, y, z, cx, cy, cz, surface = steel) {
    const bevel = Math.min(.075 * unit, Math.min(x, y, z) * .22);
    const rounded = roundedRectangle(Math.max(.001, x - bevel * 2), Math.max(.001, z - bevel * 2), bevel);
    const body = plate(rounded, y, bevel); body.translate(0, -y / 2, 0);
    const result = add(name, metalUV(body), surface, plug);
    result.position.set(cx, cy, cz); return result;
  }
  box('Steel floor', plugLength, wall, plugWidth, plugCenter, plugBottom + wall / 2, 0);
  for (const sign of [-1, 1]) box('Folded side wall', plugLength, plugHeight - wall, wall, plugCenter, midY - wall / 2, sign * (plugWidth - wall) / 2);
  // Four narrow front edges expose the open rectangular socket and sheet thickness.
  const edge = .13 * unit;
  for (const sign of [-1, 1]) {
    box('Exposed end edge', edge, wall, plugWidth, plugEnd, midY + sign * (plugHeight - wall) / 2, 0, edgeSteel);
    box('Exposed side edge', edge, plugHeight - wall * 2, wall, plugEnd, midY, sign * (plugWidth - wall) / 2, edgeSteel);
  }
  box('Rear insulating wall', .28 * unit, plugHeight - wall * 2, plugWidth - wall * 2, plugRoot, midY, 0, darkPolymer);
  const tongueLength = plugLength - 1.45 * unit;
  const tongueCenter = plugRoot - tongueLength / 2;
  const tongueTop = plugBottom + 3.3 * unit;
  const interior = [box('Black recessed tongue', tongueLength, 1.45 * unit, 9.6 * unit, tongueCenter, tongueTop - .725 * unit, 0, darkPolymer)];
  for (let i = 0; i < 4; i++) {
    interior.push(box('Recessed contact ' + (i + 1), 6.5 * unit, .09 * unit, 1 * unit,
      plugRoot - 4.55 * unit, tongueTop - 1.5 * unit, (i - 1.5) * 2.35 * unit, contacts));
  }
  // User-verified USB orientation: blink(1)/+Y looks into the open half;
  // the tongue belongs to ThingM/-Y and its four contacts face the +Y cavity.
  // Turn only these five internal parts about the plug's longitudinal axis.
  const turnInterior = new THREE.Matrix4().makeTranslation(0, midY, 0)
    .multiply(new THREE.Matrix4().makeRotationX(Math.PI))
    .multiply(new THREE.Matrix4().makeTranslation(0, -midY, 0));
  for (const part of interior) part.applyMatrix4(turnInterior);
  group.userData.usbInterior = { openFace: '+y', tongueFace: '-y', contactFace: '+y', midplaneY: midY };

  // A physical pair above/below the PCB. Their spill can illuminate a caller's
  // desk plane, while the continuous cap stays white between softer highlights.
  // Keep a lamp's spill near its own face and the desk. A long-range,
  // unshadowed point light would shine through the PCB into the opposite cap.
  const spillDistance = Math.max(.75, shellSize.y * .6);
  const upperLight = new THREE.PointLight(0xffffff, 0, spillDistance, 2);
  const lowerLight = new THREE.PointLight(0xffffff, 0, spillDistance, 2);
  upperLight.name = 'LED 1 — top'; lowerLight.name = 'LED 2 — bottom';
  upperLight.position.set(0, frameBox.max.y + .18 * unit, 0);
  lowerLight.position.set(0, frameBox.min.y - .18 * unit, 0);
  group.add(upperLight, lowerLight);
  group.userData.ledPlacement = 'independent top/bottom PCB faces; not side-by-side bulbs';
  group.userData.ledFaces = {
    1: { axis: '+y', wordmark: hasCAD ? 'blink(1)' : null },
    2: { axis: '-y', wordmark: hasCAD ? 'ThingM' : null },
  };
  group.userData.faceMappingSource = 'Project MK2 wordmark mapping; manufacturer references supply top/bottom numbering. See model-assets/source.json.';
  group.userData.bodySize = { x: shellSize.x, y: shellSize.y, z: shellSize.z };
  group.userData.upAxis = 'y';

  function canonical(value) {
    return typeof value === 'string' && /^#[0-9a-f]{6}$/i.test(value) ? value.toLowerCase() : null;
  }
  function rgb(value) { return value ? [1, 3, 5].map(start => parseInt(value.slice(start, start + 2), 16)) : [0, 0, 0]; }
  function setColors(one, two, source = 'none') {
    if (disposed) return;
    const first = canonical(one), second = canonical(two);
    const active = source !== 'none' && Boolean(first || second);
    const key = `${active}:${source}:${first}:${second}`;
    if (key === lastColors) return;
    lastColors = key;
    // THREE.Color decodes the canonical sRGB strings to the working linear
    // color space once. Do not average the sources or clip their HDR sum.
    lightField.uniforms.nb1SourceOne.value.set(active && first ? first : '#000000');
    lightField.uniforms.nb1SourceTwo.value.set(active && second ? second : '#000000');
    for (const [light, color] of [[upperLight, first], [lowerLight, second]]) {
      const power = active && color ? Math.max(...rgb(color)) / 255 : 0;
      light.color.set(color || '#ffffff'); light.intensity = power * .48;
    }
    group.userData.colorSource = active ? source : 'none';
  }
  function dispose() {
    if (disposed) return;
    disposed = true;
    group.removeFromParent();
    for (const value of textures) value.dispose();
    for (const value of materials) value.dispose();
    for (const value of geometries) value.dispose();
    upperLight.dispose(); lowerLight.dispose();
    group.clear();
    textures.clear(); materials.clear(); geometries.clear();
  }
  setColors(null, null, 'none');
  return { group, setColors, dispose };
}
