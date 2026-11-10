"use strict";

(() => {
  const contract = "nb1-light-action/experimental-v2";
  const frameContract = "nb1-frame-program/experimental-v1";
  const $ = id => document.getElementById(id);
  const isInt = (v, min, max) => Number.isSafeInteger(v) && v >= min && v <= max;
  const isSerial = v => typeof v === "string" && /^[0-9a-fA-F]{8}$/.test(v);
  const isColor = v => typeof v === "string" && /^#[0-9a-f]{6}$/.test(v);
  const isInstance = v => typeof v === "string" && /^daemon_[0-9a-f]{32}$/.test(v);
  const terminal = v => ["succeeded", "failed", "cancelled"].includes(v);
  const statusNames = {queued:"Queued", running:"Playing", cancelling:"Stopping", succeeded:"Completed", failed:"Failed", cancelled:"Cancelled"};
  const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === "object" ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
  const same = (a, b) => JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
  const clone = value => JSON.parse(JSON.stringify(value));
  const exact = (value, keys) => value !== null && typeof value === "object" && !Array.isArray(value) && same(Object.keys(value).sort(), keys.slice().sort());
  const hasOwn = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
  const actionArguments = ({kind, ...args}) => args;
  let phase = "idle", softwareSupported = false, devices = [];
  let lastPreview = null, pending = null, job = null, pollTimer = null;
  const exchanges = [];

  function note(text, error = false) {
    $("notice").textContent = String(text).slice(0, 900);
    $("notice").classList.toggle("error", error);
  }
  function render() {
    const busy = !["idle", "running", "unknown"].includes(phase);
    const active = Boolean(job && !terminal(job.status));
    const locked = busy || active || phase === "unknown";
    $("configuration").disabled = locked;
    $("preview").disabled = locked;
    $("play").disabled = locked || !softwareSupported || !isSerial($("device").value);
    $("refresh").disabled = locked;
    $("cancel").disabled = busy || !pending?.confirmed || !active;
    $("recover").hidden = phase !== "unknown";
    $("recover").disabled = busy;
    $("copy").disabled = !lastPreview;
    if (job) {
      $("job-status").textContent = statusNames[job.status];
      $("job-progress").max = job.total_frames;
      $("job-progress").value = job.completed_frames;
      $("job-detail").textContent = `${job.completed_frames} / ${job.total_frames} frames · ${job.completed_target_writes} / ${job.planned_target_writes} color writes${job.error ? " · " + job.error : ""}`;
      if (job.started_at) {
        const elapsed = ((Date.parse(job.finished_at) || Date.now()) - Date.parse(job.started_at)) / 1000;
        $("job-detail").textContent += ` · ${job.finished_at ? "Run time" : "Elapsed"} ${elapsed.toFixed(3)} s`;
      }
    } else {
      $("job-status").textContent = pending ? "Unconfirmed" : "Not playing";
      $("job-progress").max = 1;
      $("job-progress").value = 0;
      $("job-detail").textContent = pending ? "Waiting for the daemon to confirm the original request." : "Only Preview and play sends a control request.";
    }
    $("wire").textContent = JSON.stringify({phase, ticket: pending ? {request_id:pending.requestID, expected_daemon_instance_id:pending.instance, receipt_path:pending.location, confirmed:pending.confirmed} : null, exchanges}, null, 2);
  }
  function changePhase(value) { phase = value; render(); }
  function scalar(value) {
    if (typeof value !== "string") return false;
    for (let i = 0; i < value.length; i++) {
      const c = value.charCodeAt(i);
      if (c >= 0xd800 && c <= 0xdbff) {
        const low = value.charCodeAt(++i);
        if (!(low >= 0xdc00 && low <= 0xdfff)) return false;
      } else if (c >= 0xdc00 && c <= 0xdfff) return false;
    }
    return true;
  }
  // Parse once for syntax, then inspect original spelling so duplicate keys,
  // broken Unicode and non-integer numeric tokens cannot disappear in parsing.
  function strictJSON(text) {
    const value = JSON.parse(text);
    let i = 0;
    const ws = () => { while (/\s/.test(text[i] || "")) i++; };
    function string() {
      const start = i++;
      while (i < text.length) {
        if (text[i] === "\\") { i += 2; continue; }
        if (text[i++] === '"') break;
      }
      const result = JSON.parse(text.slice(start, i));
      if (!scalar(result)) throw new Error("Response contains invalid Unicode.");
      return result;
    }
    function walk(depth) {
      if (depth > 32) throw new Error("Response is nested too deeply.");
      ws(); const c = text[i];
      if (c === "{") {
        i++; ws(); const keys = new Set();
        if (text[i] === "}") { i++; return; }
        while (true) {
          ws(); const key = string();
          if (keys.has(key)) throw new Error("Response contains duplicate fields.");
          keys.add(key); ws(); i++; walk(depth + 1); ws();
          if (text[i++] === "}") break;
        }
      } else if (c === "[") {
        i++; ws(); if (text[i] === "]") { i++; return; }
        while (true) { walk(depth + 1); ws(); if (text[i++] === "]") break; }
      } else if (c === '"') string();
      else if (c === "t") i += 4;
      else if (c === "f") i += 5;
      else if (c === "n") i += 4;
      else {
        const match = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(text.slice(i));
        if (!match || !/^(?:0|[1-9]\d*)$/.test(match[0]) || !Number.isSafeInteger(Number(match[0]))) throw new Error("Response uses a noncanonical integer.");
        i += match[0].length;
      }
    }
    walk(0); ws(); if (i !== text.length) throw new Error("Response contains trailing data.");
    return value;
  }
  async function responseText(response) {
    const limit = 1024 * 1024;
    const header = response.headers.get("Content-Length");
    if (header && /^\d+$/.test(header) && Number(header) > limit) throw new Error("Response exceeds 1 MiB.");
    const reader = response.body?.getReader();
    if (!reader) {
      const bytes = await response.arrayBuffer();
      if (bytes.byteLength > limit) throw new Error("Response exceeds 1 MiB.");
      return new TextDecoder("utf-8", {fatal:true}).decode(bytes);
    }
    const chunks = []; let size = 0;
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > limit) { await reader.cancel(); throw new Error("Response exceeds 1 MiB."); }
      chunks.push(value);
    }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    return new TextDecoder("utf-8", {fatal:true}).decode(bytes);
  }
  async function request(path, method = "GET", body) {
    const url = new URL(path, location.href);
    if (url.origin !== location.origin || !path.startsWith("/") || path.startsWith("//")) throw new Error("Only requests to this local service’s origin are allowed.");
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 10000);
    const exchange = {method, path, body:body ?? null};
    exchanges.push(exchange); if (exchanges.length > 16) exchanges.shift(); render();
    try {
      const response = await fetch(url, {method, body:body === undefined ? undefined : JSON.stringify(body), headers:body === undefined ? {} : {"Content-Type":"application/json"}, credentials:"omit", redirect:"error", cache:"no-store", signal:controller.signal});
      exchange.status = response.status;
      if ((response.headers.get("Content-Type") || "").split(";", 1)[0].trim().toLowerCase() !== "application/json") throw new Error("The service did not return JSON.");
      const data = strictJSON(await responseText(response));
      exchange.response = data; render();
      return {status:response.status, headers:response.headers, data};
    } catch (error) {
      exchange.error = String(error.message || error); render(); throw error;
    } finally { clearTimeout(timeout); }
  }
  function requireStatus(response, code) {
    if (response.status === code) return;
    const message = response.data?.error?.message;
    throw new Error(typeof message === "string" ? message.slice(0, 500) : `The service returned HTTP ${response.status}.`);
  }
  function definiteAdmissionRejection(response) {
    const codes = {400:["invalid_json", "invalid_request", "invalid_light_action"], 403:["local_only"], 404:["not_found"], 405:["method_not_allowed"], 409:["busy", "queue_full"], 413:["body_too_large"], 415:["json_required"], 429:["idempotency_capacity"], 501:["frame_unsupported"], 503:["unavailable"]};
    return exact(response.data, ["error"]) && exact(response.data.error, ["code", "message"]) && typeof response.data.error.message === "string" && Boolean(codes[response.status]?.includes(response.data.error.code));
  }
  function programFacts(program) {
    if (!exact(program, ["contract", "initial", "frames", "repeat", "on_finish"]) || program.contract !== frameContract || !Array.isArray(program.initial) || program.initial.length !== 2 || !Array.isArray(program.frames) || program.frames.length < 1 || program.frames.length > 1024 || !isInt(program.repeat, 1, 10000) || !["off", "hold"].includes(program.on_finish)) throw new Error("The service returned an unsupported FrameProgram.");
    const targets = (value, expected) => {
      if (!Array.isArray(value) || value.length !== 2) throw new Error("Each frame must explicitly target both LEDs.");
      for (let i = 0; i < value.length; i++) {
        if (!exact(value[i], ["led", "color"]) || ![1, 2].includes(value[i].led) || !isColor(value[i].color) || expected && value[i].led !== expected[i].led) throw new Error("Frame targets or order are not canonical.");
      }
      if (value[0].led === value[1].led) throw new Error("Duplicate LED target.");
    };
    targets(program.initial);
    let cycle = 0, storedWrites = program.initial.length;
    for (const frame of program.frames) {
      if (!exact(frame, ["targets", "fade_ms", "interval_ms"]) || !isInt(frame.interval_ms, 50, 600000) || !isInt(frame.fade_ms, 0, 600000) || frame.fade_ms % 10 || frame.fade_ms > Math.ceil(frame.interval_ms / 10) * 10) throw new Error("Frame timing exceeds the contract.");
      targets(frame.targets, program.initial); cycle += frame.interval_ms; storedWrites += frame.targets.length;
    }
    const totalFrames = program.frames.length * program.repeat;
    const writes = program.initial.length + (storedWrites - program.initial.length) * program.repeat;
    const last = program.frames[program.frames.length - 1];
    const initial = program.initial.length * 10;
    const tail = Math.max(0, (last.targets.length - 1) * 10 + last.fade_ms + 10 - last.interval_ms);
    const duration = initial + cycle * program.repeat + tail;
    if (!isInt(totalFrames, 1, 1024) || !isInt(writes, 1, 2050) || !isInt(duration, 1, 86400000)) throw new Error("Frame program exceeds its finite budget.");
    return {totalFrames, writes, cycle, initial, tail, duration, storedWrites};
  }
  function validatePreview(value, action, serial) {
    if (!exact(value, ["result", "preview"])) throw new Error("Unsupported preview envelope.");
    const r = value.result, p = value.preview;
    const resultKeys = ["schema_version", "contract", "program_compile_contract", "recipe_id", "action", "program", "nominal_duration_ms", "total_frames", "planned_target_writes", "required_capabilities"];
    const previewKeys = ["contract", "program", "total_frames", "stored_target_writes", "planned_target_writes", "cycle_duration_ms", "nominal_initial_ms", "nominal_tail_ms", "nominal_duration_ms", "timing_basis", "required_capabilities", "execution"];
    if (serial) previewKeys.push("device");
    if (!exact(r, resultKeys) || !exact(p, previewKeys) || r.schema_version !== 1 || r.contract !== contract || r.program_compile_contract !== frameContract || p.contract !== frameContract || r.recipe_id !== `orbit-paired-linear-${action.segments}/experimental-v1` || !same(r.action, action) || !same(r.program, p.program)) throw new Error("Preview does not bind this action and frame contract.");
    const facts = programFacts(r.program);
    if (r.program.repeat !== action.cycles || r.program.frames.length !== action.segments || !same(r.program.initial.map(t => t.led), action.leds) || facts.cycle !== action.period_ms || r.program.on_finish !== action.on_finish || r.total_frames !== facts.totalFrames || p.total_frames !== facts.totalFrames || r.planned_target_writes !== facts.writes || p.planned_target_writes !== facts.writes || r.nominal_duration_ms !== facts.duration || p.nominal_duration_ms !== facts.duration || p.cycle_duration_ms !== facts.cycle || p.nominal_initial_ms !== facts.initial || p.nominal_tail_ms !== facts.tail || p.stored_target_writes !== facts.storedWrites || p.timing_basis !== "host-nominal-excludes-native-io") throw new Error("Preview counts or duration are inconsistent.");
    if (!Array.isArray(r.required_capabilities) || !same(r.required_capabilities, p.required_capabilities) || !["set", "raw-rgb", "frame-program-experimental-v1"].every(cap => r.required_capabilities.includes(cap)) || r.required_capabilities.some(cap => typeof cap !== "string" || !/^[a-z0-9-]{1,64}$/.test(cap)) || new Set(r.required_capabilities).size !== r.required_capabilities.length) throw new Error("Preview execution capabilities are inconsistent.");
    if (!exact(p.execution, ["available", "reason"]) || typeof p.execution.available !== "boolean" || !scalar(p.execution.reason) || !p.execution.reason.length) throw new Error("Missing explicit device execution check.");
    if (serial && (!exact(p.device, ["serial", "connection"]) || p.device.serial !== serial || typeof p.device.connection !== "string" || p.execution.available && p.device.connection !== "online")) throw new Error("Preview does not bind the selected device.");
    return clone(value);
  }
  function timestamp(value) { return typeof value === "string" && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) && Number.isFinite(Date.parse(value)); }
  function validateJob(value, original) {
    const required = ["id", "device", "status", "program", "completed_frames", "total_frames", "completed_target_writes", "planned_target_writes", "created_at"];
    const optional = ["started_at", "finished_at", "error"];
    if (!value || !required.every(key => hasOwn(value, key)) || Object.keys(value).some(key => !required.includes(key) && !optional.includes(key)) || typeof value.id !== "string" || !/^framejob_[0-9a-f]{32}$/.test(value.id) || original.jobID && value.id !== original.jobID || value.device !== original.serial || !same(value.program, original.program) || !hasOwn(statusNames, value.status)) throw new Error("Job does not bind the original device, program, or ID.");
    const facts = programFacts(value.program);
    if (value.total_frames !== facts.totalFrames || value.planned_target_writes !== facts.writes || !isInt(value.completed_frames, 0, facts.totalFrames) || !isInt(value.completed_target_writes, 0, facts.writes) || !timestamp(value.created_at) || original.createdAt && value.created_at !== original.createdAt) throw new Error("Job counts or creation time are inconsistent.");
    if (value.started_at !== undefined && (!timestamp(value.started_at) || Date.parse(value.started_at) < Date.parse(value.created_at))) throw new Error("Invalid job start time.");
    if (terminal(value.status) !== (value.finished_at !== undefined) || value.finished_at !== undefined && (!timestamp(value.finished_at) || Date.parse(value.finished_at) < Date.parse(value.started_at || value.created_at))) throw new Error("Job terminal state and finish time disagree.");
    if (["running", "cancelling", "succeeded"].includes(value.status) && !value.started_at || value.error !== undefined && (!scalar(value.error) || value.error.length > 10000)) throw new Error("Job state fields are incomplete.");
    if (value.status === "succeeded" && (value.completed_frames !== facts.totalFrames || value.completed_target_writes !== facts.writes)) throw new Error("An incomplete job was marked successful.");
    return clone(value);
  }
  function validatedInstance(response) {
    requireStatus(response, 200);
    const data = response.data;
    if (!exact(data, ["daemon_instance_id", "idempotency"]) || !isInstance(data.daemon_instance_id) || response.headers.get("X-NB1-Daemon-Instance-ID") !== data.daemon_instance_id) throw new Error("Daemon identity was not confirmed.");
    const i = data.idempotency;
    if (!exact(i, ["supported", "request_id_prefix", "accept_until_uptime_ms", "max_accept_window_ms", "receipt_retention_ms", "max_entries", "max_retained_steps"]) || i.supported !== true || !isInt(i.accept_until_uptime_ms, 1, Number.MAX_SAFE_INTEGER) || i.request_id_prefix !== `req_${i.accept_until_uptime_ms}_` || !["max_accept_window_ms", "receipt_retention_ms", "max_entries", "max_retained_steps"].every(key => isInt(i[key], 1, Number.MAX_SAFE_INTEGER))) throw new Error("The service does not support the required receipt binding.");
    return data;
  }
  function inputAction() {
    const number = id => {
      const field = $(id), value = Number(field.value);
      if (!/^(0|[1-9]\d*)$/.test(field.value) || !field.checkValidity() || !Number.isSafeInteger(value)) throw new Error("Enter integer parameters within the displayed ranges.");
      return value;
    };
    const colors = [$("color-one").value.toLowerCase(), $("color-two").value.toLowerCase()];
    if (!colors.every(isColor)) throw new Error("Use #rrggbb for palette colors.");
    return {kind:"orbit", colors, leds:$("order").value.split(",").map(Number), period_ms:number("period"), cycles:number("cycles"), segments:number("segments"), on_finish:$("finish").value};
  }
  function fingerprint() { return JSON.stringify({action:inputAction(), serial:$("device").value}); }
  function showPreview(value) {
    lastPreview = value;
    const r = value.result;
    $("call").textContent = `light.orbit(${JSON.stringify(actionArguments(r.action))})`;
    $("frames").textContent = String(r.total_frames);
    $("writes").textContent = String(r.planned_target_writes);
    $("duration").textContent = `${(r.nominal_duration_ms / 1000).toFixed(3)} s`;
    $("program").value = JSON.stringify(r.program, null, 2);
    $("availability").textContent = value.preview.execution.available ? "Cached device capabilities support this frame contract. Preview did not play it." : `Playback unavailable: ${value.preview.execution.reason}. Preview did not play it.`;
    render();
  }
  function clearPreview() {
    lastPreview = null;
    $("frames").textContent = $("writes").textContent = $("duration").textContent = "—";
    $("program").value = "";
    $("availability").textContent = "Parameters changed. Preview again.";
    try { $("call").textContent = `light.orbit(${JSON.stringify(actionArguments(inputAction()))})`; } catch (_) { $("call").textContent = "light.orbit({…})"; }
    render();
  }
  async function getPreview() {
    const action = inputAction(), serial = $("device").value, stamp = fingerprint();
    if (serial && !isSerial(serial)) throw new Error("Invalid device serial.");
    const response = await request("/v2/light/preview", "POST", {contract, action, ...(serial ? {serial} : {})});
    requireStatus(response, 200);
    if (stamp !== fingerprint()) throw new Error("Parameters changed during preview. Preview again.");
    const result = validatePreview(response.data, action, serial);
    showPreview(result);
    return {value:result, serial, stamp};
  }
  async function previewOnly() {
    if (phase !== "idle") return;
    changePhase("previewing");
    try { await getPreview(); note("Preview complete. No hardware task was sent."); }
    catch (error) { note(error.message, true); }
    finally { changePhase("idle"); }
  }
  function stopPolling() { if (pollTimer !== null) clearTimeout(pollTimer); pollTimer = null; }
  function acceptJob(value) {
    const next = validateJob(value, pending);
    if (job && job.id === next.id) {
      if (terminal(job.status) && !terminal(next.status)) return;
      if (next.completed_frames < job.completed_frames || next.completed_target_writes < job.completed_target_writes) throw new Error("Job progress regressed. The original request is retained for verification.");
    }
    pending.jobID = next.id; pending.createdAt = next.created_at; pending.confirmed = true; job = next;
    stopPolling();
    if (terminal(job.status)) {
      changePhase("idle");
      note(job.status === "succeeded" ? "Job completed. The daemon confirmed the final fade wait and cleanup. Observe the physical light as well." : `Job ${statusNames[job.status]}${job.error ? ": " + job.error : "."}`, job.status === "failed");
    } else {
      changePhase("running");
      note(job.status === "cancelling" ? "Stopping. Waiting for the daemon to turn off and read back the LEDs." : "Job confirmed. This page only polls state; the daemon executes the fades.");
      pollTimer = setTimeout(pollJob, 650);
    }
  }
  function unknown(error) {
    stopPolling(); changePhase("unknown");
    note(`Result unconfirmed: ${error.message}. Original ticket and program retained. Verify the original request; it will not be resent automatically.`, true);
  }
  async function play() {
    if (phase !== "idle" || !softwareSupported) return;
    changePhase("preparing");
    let submitted = false;
    try {
      const snapshot = await getPreview();
      if (!snapshot.serial || !snapshot.value.preview.execution.available) throw new Error("The selected device has not passed the execution capability check.");
      const instance = validatedInstance(await request("/v1/instance"));
      if (snapshot.stamp !== fingerprint()) throw new Error("Parameters changed; no job was submitted.");
      if (!globalThis.crypto?.getRandomValues) throw new Error("This browser cannot generate secure request IDs.");
      const nonce = Array.from(crypto.getRandomValues(new Uint8Array(16)), v => v.toString(16).padStart(2, "0")).join("");
      const requestID = instance.idempotency.request_id_prefix + nonce;
      pending = {requestID, instance:instance.daemon_instance_id, serial:snapshot.serial, program:clone(snapshot.value.result.program), confirmed:false};
      pending.location = `/v1/devices/${encodeURIComponent(pending.serial)}/frame-requests/${encodeURIComponent(requestID)}?expected_daemon_instance_id=${encodeURIComponent(pending.instance)}`;
      job = null;
      const body = {contract, action:snapshot.value.result.action, policy:"reject", request_id:requestID, expected_daemon_instance_id:pending.instance};
      changePhase("submitting"); submitted = true;
      const response = await request(`/v2/devices/${encodeURIComponent(pending.serial)}/light`, "POST", body);
      if (definiteAdmissionRejection(response)) {
        const error = new Error(response.data.error.message);
        error.admissionRejected = true;
        throw error;
      }
      requireStatus(response, 202);
      if (!["true", "false"].includes(response.headers.get("X-NB1-Replayed")) || response.headers.get("X-NB1-Daemon-Instance-ID") !== pending.instance || response.headers.get("Location") !== pending.location) throw new Error("Submit response headers do not bind the original ticket.");
      acceptJob(response.data);
    } catch (error) {
      if (submitted && error.admissionRejected) {
        pending = null; job = null; changePhase("idle");
        note(`Request explicitly rejected; no new job was created: ${error.message}`, true);
      } else if (submitted) unknown(error);
      else { changePhase("idle"); note(error.message, true); }
    }
  }
  async function pollJob() {
    pollTimer = null;
    if (phase !== "running" || !job || !pending) return;
    const expected = pending;
    try {
      const response = await request(`/v1/frame-jobs/${encodeURIComponent(job.id)}`);
      if (pending !== expected || phase !== "running") return;
      requireStatus(response, 200); acceptJob(response.data);
    } catch (error) { if (pending === expected && phase === "running") unknown(error); }
  }
  async function recover() {
    if (phase !== "unknown" || !pending) return;
    changePhase("recovering");
    try {
      const response = await request(pending.location);
      requireStatus(response, 200);
      if (response.headers.get("X-NB1-Daemon-Instance-ID") !== pending.instance) throw new Error("The original request query returned a different daemon.");
      acceptJob(response.data);
    } catch (error) { unknown(error); }
  }
  async function cancel() {
    if (!["running", "unknown"].includes(phase) || !pending?.confirmed || !job || terminal(job.status)) return;
    stopPolling(); changePhase("cancelling");
    try {
      const response = await request(`/v1/frame-jobs/${encodeURIComponent(job.id)}/cancel`, "POST", {});
      requireStatus(response, 202); acceptJob(response.data);
    } catch (error) { unknown(error); }
  }
  async function refresh() {
    if (phase !== "idle") return;
    changePhase("loading");
    try {
      const [caps, list] = await Promise.all([request("/v1/frame-capabilities"), request("/v1/devices")]);
      requireStatus(caps, 200); requireStatus(list, 200);
      if (caps.data?.contract !== frameContract || caps.data.schema_version !== 1 || typeof caps.data.software_supported !== "boolean") throw new Error("This service does not support the required experimental frame contract.");
      softwareSupported = caps.data.software_supported;
      if (!Array.isArray(list.data) || list.data.length > 64 || list.data.some(d => !isSerial(d?.serial) || !scalar(d.product) || typeof d.connection !== "string") || new Set(list.data.map(d => d.serial)).size !== list.data.length) throw new Error("Device list is not canonical.");
      const prior = $("device").value;
      devices = list.data;
      $("device").replaceChildren();
      const empty = document.createElement("option"); empty.value = ""; empty.textContent = devices.length ? "Choose a local device" : "No configured devices"; $("device").append(empty);
      for (const device of devices) {
        const option = document.createElement("option"); option.value = device.serial; option.textContent = `${device.product} · ${device.serial} · ${device.connection}`; $("device").append(option);
      }
      $("device").value = devices.some(d => d.serial === prior) ? prior : devices.length === 1 ? devices[0].serial : "";
      clearPreview(); deviceLabel();
      note(softwareSupported ? "The local service supports experimental frames. Preview first, or explicitly choose Preview and play." : "Frame execution is not enabled. This page will not fall back to sequential LED playback.", !softwareSupported);
    } catch (error) {
      softwareSupported = false;
      if (!devices.length) {
        const missing = document.createElement("option"); missing.value = ""; missing.textContent = "Device state unconfirmed"; $("device").replaceChildren(missing);
        $("device-state").textContent = "Check that the local service supports the experimental frame contract.";
      }
      note(error.message, true);
    }
    finally { changePhase("idle"); }
  }
  function deviceLabel() {
    const device = devices.find(item => item.serial === $("device").value);
    $("device-state").textContent = device ? `Cached state: ${device.connection}. Capabilities will be previewed again before submission.` : "Without a selected device, only a pure preview is available.";
    if (device?.observed?.sampled_at && timestamp(device.observed.sampled_at)) $("device-state").textContent += ` Latest readback: ${device.observed.sampled_at}`;
  }
  const palette = [["Red","#ff0000"], ["Blue","#0000ff"], ["Orange","#ffa500"], ["Green","#00ff00"], ["Cyan","#00ffff"], ["Purple","#800080"], ["White","#ffffff"], ["Black","#000000"]];
  for (const suffix of ["one", "two"]) {
    const input = $("color-" + suffix), picker = $("color-" + suffix + "-picker"), group = $("palette-" + suffix);
    function sync() {
      const color = input.value.toLowerCase();
      if (isColor(color)) picker.value = color;
      for (const button of group.children) button.setAttribute("aria-pressed", String(button.dataset.color === color));
    }
    for (const [name, color] of palette) {
      const button = document.createElement("button"); button.type = "button"; button.dataset.color = color; button.style.setProperty("--swatch", color); button.title = `${name} ${color}`; button.setAttribute("aria-label", `Color ${suffix === "one" ? "A" : "B"}: ${name}`); button.addEventListener("click", () => { input.value = color; sync(); clearPreview(); }); group.append(button);
    }
    input.addEventListener("input", () => { sync(); clearPreview(); });
    picker.addEventListener("input", () => { input.value = picker.value.toLowerCase(); sync(); clearPreview(); });
    sync();
  }
  for (const id of ["device", "segments", "order", "period", "cycles", "finish"]) {
    $(id).addEventListener("change", () => {
      if (id === "segments") {
        const n = Number($("segments").value);
        $("period").min = String(n * 50); $("period").max = String(n * 600000); $("cycles").max = String(Math.floor(1024 / n));
      }
      deviceLabel(); clearPreview();
    });
  }
  $("preview").addEventListener("click", previewOnly);
  $("play").addEventListener("click", play);
  $("cancel").addEventListener("click", cancel);
  $("recover").addEventListener("click", recover);
  $("refresh").addEventListener("click", refresh);
  $("copy").addEventListener("click", async () => {
    if (!lastPreview) return;
    try { await navigator.clipboard.writeText($("program").value); note("Copied the frozen frame program. No hardware command was sent."); }
    catch (_) { $("program").focus(); $("program").select(); note("Frame program selected for manual copying."); }
  });
  window.addEventListener("beforeunload", event => {
    if (phase === "unknown" || job && !terminal(job.status)) { event.preventDefault(); event.returnValue = ""; }
  });
  clearPreview(); refresh();
})();
