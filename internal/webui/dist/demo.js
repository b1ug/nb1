"use strict";

(() => {
  const contract = "nb1-light-action/v1";
  const programContract = "nb1-program-effect/v1";
  const $ = id => document.getElementById(id);
  const names = {set:"Steady",fade:"Fade",off:"Off",blink:"Blink",breathe:"Breathe",pulse:"Pulse",alternate:"Alternate",orbit:"Orbit",rainbow:"Rainbow",program:"Exact sequence",pattern:"Saved Patterns"};
  const descriptions = {
    set:"Hold this color until another action or an explicit off command.", fade:"Fade from the current color to the target, then hold it.", off:"Set the selected output to black.",
    blink:"Switch on and off with explicit durations and a finite cycle count.", breathe:"Fade up and down symmetrically. The nb1 core compiles a fixed curve.",
    pulse:"Rise, decay, then rest. This pulse differs from a symmetric breath.", alternate:"Exchange two colors between LED 1 and LED 2. Two points do not define clockwise or counterclockwise motion.",
    rainbow:"Fade through a fixed red, yellow, green, cyan, blue, magenta, red path.", program:"Provide a finite Program, using the same device worker as the artistic actions.", pattern:"Play the loaded fixed Pattern revision without reinterpreting its source or updating it."
  };
  const terminal = status => ["succeeded","failed","cancelled"].includes(status);
  const statusNames = {queued:"Queued",running:"Running",cancelling:"Stopping",succeeded:"Completed",failed:"Failed",cancelled:"Cancelled"};
  let kind = "set", serial = null, device = null, job = null, ticket = null, busy = false, lastPreview = null;
  let pollTimer = null, pollStarted = false, connectionRevision = 0, recoveryAttempts = 0;
  let intentRevision=0, libraryRequest=0, selectionRevision=0, libraryReady=false, libraryItems=[], loadedPattern=null, pendingSave=null, saveOperation=null, checkOperation=null, loadOperation=null, saveFormRevision=0;

  // A small editing palette, not another CSS color parser. Other names stay
  // untouched and are resolved by the authoritative nb1 compiler on request.
  const palette = [
    {name:"red",label:"Red",hex:"#ff0000"}, {name:"orange",label:"Orange",hex:"#ffa500"},
    {name:"gold",label:"Gold",hex:"#ffd700"}, {name:"lime",label:"Lime",hex:"#00ff00"},
    {name:"green",label:"Green",hex:"#008000"}, {name:"cyan",label:"Cyan",hex:"#00ffff"},
    {name:"blue",label:"Blue",hex:"#0000ff"}, {name:"purple",label:"Purple",hex:"#800080"},
    {name:"magenta",label:"Magenta",hex:"#ff00ff"}, {name:"white",label:"White",hex:"#ffffff"},
    {name:"gray",label:"Gray",hex:"#808080"}, {name:"black",label:"Black",hex:"#000000"}
  ];
  const paletteNames = Object.fromEntries(palette.map(color => [color.name,color.hex]));
  Object.assign(paletteNames,{aqua:paletteNames.cyan,fuchsia:paletteNames.magenta,grey:paletteNames.gray});
  function paletteHex(text) {
    const value = text.trim().toLowerCase();
    if (/^#[0-9a-f]{6}$/.test(value)) return value;
    if (/^#[0-9a-f]{3}$/.test(value)) return "#"+Array.from(value.slice(1),digit => digit+digit).join("");
    return Object.prototype.hasOwnProperty.call(paletteNames,value) ? paletteNames[value] : null;
  }
  function setupColorControls() {
    for (const id of ["color","color-one","color-two"]) {
      const input = $(id), picker = $(id+"-picker"), hint = $(id+"-hint");
      const group = document.querySelector(`[data-color-for="${id}"]`);
      function sync() {
        const hex = paletteHex(input.value);
        if (hex) picker.value = hex;
        for (const button of group.children) {
          const selected = button.dataset.hex === hex;
          button.classList.toggle("selected",selected);
          button.setAttribute("aria-pressed",String(selected));
        }
        hint.textContent = hex ? `${hex} · Color edited locally; nothing sent.` : "Keep the original text for nb1 to resolve; the picker shows the last recognized color.";
      }
      function choose(hex) {
        input.value = hex;
        sync();
        showIntent();
      }
      for (const color of palette) {
        const button = document.createElement("button");
        button.type = "button"; button.className = "color-swatch"; button.dataset.hex = color.hex;
        button.setAttribute("aria-label",`${color.label} ${color.name} ${color.hex}`);
        button.title = `${color.name} · ${color.hex}`;
        const dot = document.createElement("span"); dot.className = "swatch-dot"; dot.style.backgroundColor = color.hex;
        const name = document.createElement("span"); name.className = "swatch-name"; name.textContent = color.label;
        button.append(dot,name); button.addEventListener("click",() => choose(color.hex)); group.append(button);
      }
      input.addEventListener("input",sync);
      input.addEventListener("change",sync);
      picker.addEventListener("input",() => choose(picker.value.toLowerCase()));
      picker.addEventListener("change",() => choose(picker.value.toLowerCase()));
      sync();
    }
  }

  function note(message, error = false) {
    $("notice").textContent = String(message).slice(0, 700);
    $("notice").classList.toggle("error", error);
  }
  function updateButtons() {
    const running = job && !terminal(job.status);
    const unknown = ticket && !ticket.confirmed;
    $("play").disabled = busy || !serial || !device || device.connection !== "online" || Boolean(running || unknown);
    $("preview").disabled = busy;
    $("stop").disabled = busy || !serial;
    $("connect").disabled = busy || !$("device-select").value || Boolean(running || unknown);
    $("device-select").disabled = busy || Boolean(running || unknown);
    $("recover").hidden = !unknown;
    $("recover").disabled = busy;
    updateLibraryButtons();
  }
  function canonical(value) {
    if (Array.isArray(value)) return value.map(canonical);
    if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(k => [k, canonical(value[k])]));
    return value;
  }
  const same = (a, b) => JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
  const libraryContract = "nb1-pattern-library/v1", recordDomain = "nb1-pattern-record/v1";
  const maxPackageBytes = 1024 * 1024;
  const clone = value => JSON.parse(JSON.stringify(value));
  const isID = value => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(value);
  const isRevision = value => typeof value === "string" && /^sha256:[0-9a-f]{64}$/.test(value);
  function exactKeys(value, expected) {
    return value !== null && typeof value === "object" && !Array.isArray(value) && same(Object.keys(value).sort(),expected.slice().sort());
  }
  function scalarText(value, max = 256) {
    if (typeof value !== "string" || !value.length || Array.from(value).length > max) return false;
    for (let i=0; i<value.length; i++) {
      const c=value.charCodeAt(i);
      if (c>=0xd800 && c<=0xdbff) { const low=value.charCodeAt(++i); if (!(low>=0xdc00 && low<=0xdfff)) return false; }
      else if (c>=0xdc00 && c<=0xdfff) return false;
    }
    return true;
  }
  function programFacts(program) {
    if (!exactKeys(program,["steps","repeat","on_finish"]) || !Array.isArray(program.steps) || program.steps.length<1 || program.steps.length>1024 || !Number.isSafeInteger(program.repeat) || program.repeat<1 || program.repeat>10000 || !["off","hold"].includes(program.on_finish)) throw new Error("Unsupported frozen Pattern Program.");
    let duration=0;
    for (const step of program.steps) {
      if (!exactKeys(step,["led","color","fade_ms","hold_ms"]) || ![0,1,2].includes(step.led) || typeof step.color!=="string" || !/^#[0-9a-f]{6}$/.test(step.color) || !Number.isSafeInteger(step.fade_ms) || step.fade_ms<0 || step.fade_ms>600000 || step.fade_ms%10 || !Number.isSafeInteger(step.hold_ms) || step.hold_ms<0 || step.hold_ms>3600000) throw new Error("Pattern steps are not a canonical Program.");
      duration+=step.fade_ms+step.hold_ms+10;
    }
    duration*=program.repeat;
    if (!Number.isSafeInteger(duration) || duration>86400000) throw new Error("Pattern exceeds the finite duration budget.");
    return {totalSteps:program.steps.length*program.repeat,duration};
  }
  function strictLibraryJSON(text) {
    const value=JSON.parse(text); let i=0;
    const ws=()=>{ while (/\s/.test(text[i]||"")) i++; };
    function string() { const start=i++; while(i<text.length) { if(text[i]==="\\") { i+=2; continue; } if(text[i++]==='"') break; } const s=JSON.parse(text.slice(start,i)); if (s.length && !scalarText(s,2*1024*1024)) throw new Error("Pattern library response contains invalid Unicode."); return s; }
    function walk(depth) {
      if(depth>32) throw new Error("Pattern library response is nested too deeply."); ws(); const c=text[i];
      if(c==='{') { i++; ws(); const keys=new Set(); if(text[i]==='}') { i++; return; } while(true) { ws(); const key=string(); if(keys.has(key)) throw new Error("Pattern library response contains duplicate fields."); keys.add(key); ws(); i++; walk(depth+1); ws(); if(text[i++]==='}') break; } }
      else if(c==='[') { i++; ws(); if(text[i]===']') { i++; return; } while(true) { walk(depth+1); ws(); if(text[i++]===']') break; } }
      else if(c==='"') string();
      else if(c==='t') i+=4;
      else if(c==='f') i+=5;
      else if(c==='n') i+=4;
      else { const match=/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(text.slice(i)); if(!match || !/^(?:0|[1-9]\d*)$/.test(match[0])) throw new Error("Pattern library response uses a noncanonical integer."); i+=match[0].length; }
    }
    walk(0); ws(); if(i!==text.length) throw new Error("Pattern library response has trailing data."); return value;
  }
  function canonicalText(value) {
    const hex="0123456789abcdef";
    return '"'+value.replace(/[\u0000-\u001f"\\]/g,c=>c==='"'?'\\"':c==='\\'?'\\\\':'\\u00'+hex[c.charCodeAt(0)>>4]+hex[c.charCodeAt(0)&15])+'"';
  }
  function canonicalProgram(value) {
    return '{"steps":['+value.steps.map(s=>`{"led":${s.led},"color":${canonicalText(s.color)},"fade_ms":${s.fade_ms},"hold_ms":${s.hold_ms}}`).join(',')+`],"repeat":${value.repeat},"on_finish":${canonicalText(value.on_finish)}}`;
  }
  async function verifiedRecord(value, expected) {
    if (!exactKeys(value,["id","title","source","compiled_program","compile_contract","revision"]) || !isID(value.id) || !scalarText(value.title) || value.compile_contract!==programContract || !isRevision(value.revision) || expected && (value.id!==expected.id || value.revision!==expected.revision)) throw new Error("Pattern record does not match the selected ID and revision.");
    programFacts(value.compiled_program);
    let source;
    if (exactKeys(value.source,["program"])) {
      programFacts(value.source.program);
      if(!same(value.source.program,value.compiled_program)) throw new Error("Pattern source Program differs from its frozen result.");
      source='{"program":'+canonicalProgram(value.source.program)+'}';
    } else if (exactKeys(value.source,["effect"])) {
      // The Store validates legacy Effect expansion. The page binds its
      // declared source/hash and frozen Program, never recompiles the Effect.
      // Execution still resolves this exact ref and compares daemon preview.
      const effect=value.source.effect;
      const fields=effect?.kind==='steady'?["kind","color","led","fade_ms"]:["kind","color","led","cycles","period_ms","on_finish"];
      if(!exactKeys(effect,fields) || !["steady","pulse"].includes(effect.kind) || !/^#[0-9a-f]{6}$/.test(effect.color) || ![0,1,2].includes(effect.led)) throw new Error("Unsupported Pattern source Effect version.");
      if(effect.kind==='steady') {
        if(!Number.isSafeInteger(effect.fade_ms) || effect.fade_ms<0 || effect.fade_ms>600000 || effect.fade_ms%10) throw new Error("Pattern source Effect parameters are not canonical.");
        source=`{"effect":{"kind":"steady","color":${canonicalText(effect.color)},"led":${effect.led},"fade_ms":${effect.fade_ms}}}`;
      } else {
        if(!Number.isSafeInteger(effect.cycles) || effect.cycles<1 || effect.cycles>10000 || !Number.isSafeInteger(effect.period_ms) || effect.period_ms<60 || effect.period_ms>1200000 || effect.period_ms%10 || !["off","hold"].includes(effect.on_finish)) throw new Error("Pattern source Effect parameters are not canonical.");
        source=`{"effect":{"kind":"pulse","color":${canonicalText(effect.color)},"led":${effect.led},"cycles":${effect.cycles},"period_ms":${effect.period_ms},"on_finish":${canonicalText(effect.on_finish)}}}`;
      }
    } else throw new Error("A Pattern must contain exactly one supported source.");
    if(!globalThis.crypto?.subtle) throw new Error("This browser cannot verify Pattern SHA-256. Basic light controls remain available.");
    const canonicalRecord=`{"id":${canonicalText(value.id)},"title":${canonicalText(value.title)},"source":${source},"compiled_program":${canonicalProgram(value.compiled_program)},"compile_contract":${canonicalText(value.compile_contract)}}`;
    const bytes=await crypto.subtle.digest("SHA-256",new TextEncoder().encode(recordDomain+"\n"+canonicalRecord));
    const revision="sha256:"+Array.from(new Uint8Array(bytes),x=>x.toString(16).padStart(2,"0")).join("");
    if(revision!==value.revision) throw new Error("Pattern content hash does not match its revision.");
    return clone(value);
  }
  function validateLibrary(value) {
    if(!exactKeys(value,["schema_version","contract","library","patterns"]) || value.schema_version!==1 || value.contract!==libraryContract || !exactKeys(value.library,["path","state","create_only","max_records","max_package_bytes","record_contract","compile_contract","snapshot_kind"]) || !scalarText(value.library.path,4096) || value.library.state!=="ready" || value.library.create_only!==true || value.library.max_records!==128 || value.library.max_package_bytes!==maxPackageBytes || value.library.record_contract!==recordDomain || value.library.compile_contract!==programContract || value.library.snapshot_kind!=="nb1-pattern-snapshot" || !Array.isArray(value.patterns) || value.patterns.length>128) throw new Error("Unsupported Pattern library contract or capacity.");
    const ids=new Set();
    for(const item of value.patterns) {
      if(!exactKeys(item,["id","title","revision","compile_contract","total_steps","nominal_duration_ms","on_finish","required_capabilities"]) || !isID(item.id) || ids.has(item.id) || !scalarText(item.title) || !isRevision(item.revision) || item.compile_contract!==programContract || !Number.isSafeInteger(item.total_steps) || item.total_steps<1 || item.total_steps>10240000 || !Number.isSafeInteger(item.nominal_duration_ms) || item.nominal_duration_ms<item.total_steps*10 || item.nominal_duration_ms>86400000 || !["off","hold"].includes(item.on_finish) || !Array.isArray(item.required_capabilities) || item.required_capabilities.length>64 || !item.required_capabilities.every(x=>typeof x==="string" && /^[A-Za-z0-9_-]{1,64}$/.test(x))) throw new Error("Pattern list contains an unsupported summary.");
      ids.add(item.id);
    }
    return clone(value.patterns);
  }

  function integer(id) {
    const field = $(id), value = field.value;
    if (!/^\d+$/.test(value) || !field.checkValidity()) throw new Error("Enter integer durations and counts within the displayed ranges.");
    return Number(value);
  }
  const transitionDescriptions = {
    cut:"Swap the two colors directly, using the original alternating behavior.",
    linear:"Blend the colors back and forth with a linear weight.",
    sine:"Blend back and forth with a sine curve that slows near each end.",
    orbit:"Offset color and brightness by a quarter cycle for an orbit illusion, not physical 3D rotation."
  };
  function syncTransitionControls() {
    const mode = $("transition").value;
    const flowing = kind === "alternate" && mode !== "cut";
    $("period").min = flowing ? "1300" : "400";
    $("cycles").max = flowing ? "15" : "10";
    const invalidPeriod = kind === "alternate" && !$("period").checkValidity();
    const invalidCycles = kind === "alternate" && !$("cycles").checkValidity();
    $("period").setAttribute("aria-invalid",String(invalidPeriod));
    $("cycles").setAttribute("aria-invalid",String(invalidCycles));
    const limits = flowing ? "66 steps per cycle; this page accepts 1300–10000ms and up to 15 cycles. LED writes are sequential, not atomic." : "This page accepts 400–10000ms and up to 10 cycles.";
    $("transition-help").textContent = `${transitionDescriptions[mode] || "Choose a valid alternating transition."} ${limits}${invalidPeriod || invalidCycles ? " The value is out of range. Edit it before previewing or playing; inputs are never silently replaced." : ""}`;
    $("transition-help").classList.toggle("invalid",invalidPeriod || invalidCycles);
    showActionSelection();
  }
  function showActionSelection() {
    const selected = kind === "alternate" && $("transition").value === "orbit" ? "orbit" : kind;
    document.querySelectorAll("[data-action]").forEach(button => {
      const active = button.dataset.action === selected;
      button.classList.toggle("selected",active);
      button.setAttribute("aria-pressed",String(active));
    });
    $("kind-label").textContent = `${["set","fade","off","program","pattern"].includes(kind) ? "Basic" : "Expressive"} · ${kind === "program" ? "run" : selected}`;
    $("action-description").textContent = selected === "orbit"
      ? "Offset color mixing and brightness to suggest flow. Two LEDs cannot establish a physical rotation direction."
      : descriptions[kind];
  }
  function intent(forcedKind) {
    const selected = forcedKind || kind;
    if (selected === "pattern") {
      if(!loadedPattern) throw new Error("Load a fixed Pattern revision first.");
      return {kind:selected,ref:clone(loadedPattern.ref),program:clone(loadedPattern.program)};
    }
    if (selected === "program") {
      const raw = $("program").value;
      JSON.parse(raw); // Validate syntax, but preserve original wire spelling.
      return {kind:selected, raw};
    }
    const action = {kind:selected};
    if (!["alternate"].includes(selected)) action.led = forcedKind ? 0 : integer("led");
    if (!["off","alternate","rainbow"].includes(selected)) action.color = $("color").value;
    if (selected === "fade") action.duration_ms = integer("duration");
    if (["breathe","alternate","rainbow"].includes(selected)) action.period_ms = integer("period");
    if (selected === "blink") { action.on_ms = integer("on-ms"); action.off_ms = integer("off-ms"); }
    if (selected === "pulse") { action.attack_ms = integer("attack"); action.decay_ms = integer("decay"); action.rest_ms = integer("rest"); }
    if (selected === "alternate") {
      action.colors = [$("color-one").value,$("color-two").value]; action.leds = [1,2];
      const transition = $("transition").value;
      if (!Object.prototype.hasOwnProperty.call(transitionDescriptions,transition)) throw new Error("Choose a valid alternating transition.");
      if (transition !== "cut") action.transition = transition;
    }
    if (!["set","fade","off"].includes(selected)) { action.cycles = integer("cycles"); action.on_finish = $("finish").value; }
    else action.on_finish = "hold";
    return {kind:selected, action};
  }
  function showIntent() {
    intentRevision++; lastPreview=null;
    if(loadOperation) {loadOperation=null;updateLibraryButtons();}
    try {
      const value = intent();
      const {kind: ignoredKind, ...options} = value.action || {};
      $("surface-call").textContent = value.kind === "pattern" ? `light.pattern(${JSON.stringify(value.ref)})` : value.kind === "program" ? "light.run(program)" : `light.${value.kind}(${JSON.stringify(options)})`;
    } catch (_) { $("surface-call").textContent = `light.${kind === "program" ? "run" : kind}(…)`; }
  }
  function selectAction(value) {
    if(value==="pattern" && !loadedPattern) {
      // This tile is also a navigation entry. No loaded record means that
      // choosing it must leave the current intent and any receipt untouched.
      $("library-heading").closest("section").scrollIntoView({behavior:"auto",block:"start"});
      $("library-select").focus({preventScroll:true});
      if(libraryReady) libraryNote(libraryItems.length
        ? "Choose a Pattern, then select Load fixed revision. Loading does not play it."
        : "No saved Patterns yet. Set an effect, enter its ID and title, and save it on this computer before loading it.");
      return;
    }
    // Orbit is a visible shortcut for the existing alternate transition.
    // Keep the actual API kind and the user's timing/color inputs unchanged.
    if(value === "orbit") { $("transition").value="orbit"; value="alternate"; }
    else if(value === "alternate") $("transition").value="cut";
    kind = value;
    if(value!=="pattern") {loadedPattern=null;$("pattern-loaded").textContent="No Pattern loaded.";}
    $("pattern-row").hidden=kind!=="pattern";
    const rows = {"single-color-row":!["off","alternate","rainbow","program","pattern"].includes(kind),"alternate-row":kind==="alternate","led-row":!["alternate","program","pattern"].includes(kind),"duration-row":kind==="fade","period-row":["breathe","alternate","rainbow"].includes(kind),"transition-row":kind==="alternate","blink-row":kind==="blink","pulse-row":kind==="pulse","cycles-row":["blink","breathe","pulse","alternate","rainbow"].includes(kind),"finish-row":["blink","breathe","pulse","alternate","rainbow"].includes(kind),"program-row":kind==="program"};
    for (const [id, visible] of Object.entries(rows)) $(id).hidden = !visible;
    syncTransitionControls();
    showIntent();
    updateLibraryButtons();
  }

  async function request(path, method = "GET", body, strict = false) {
    if (!path.startsWith("/") || path.startsWith("//")) throw new Error("Only same-origin API paths are allowed.");
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const response = await fetch(path,{method,credentials:"omit",cache:"no-store",redirect:"error",signal:controller.signal,headers:{Accept:"application/json",...(body === undefined ? {} : {"Content-Type":"application/json"})},...(body === undefined ? {} : {body:typeof body === "string" ? body : JSON.stringify(body)})});
      let text;
      if(strict) {
        if(!/^application\/json(?:\s*;\s*charset=(?:utf-8|"utf-8"))?$/i.test(response.headers.get("Content-Type")||"")) throw new Error("Pattern library response must have one JSON content type.");
        const buffer=await response.arrayBuffer(); if(buffer.byteLength>2*1024*1024) throw new Error("Pattern library response exceeds this page’s receive limit.");
        text=new TextDecoder("utf-8",{fatal:true}).decode(buffer);
      } else text=await response.text();
      if (text.length > 2 * 1024 * 1024) throw new Error("API response exceeds this page’s receive limit.");
      let value;
      try { value = strict ? strictLibraryJSON(text) : JSON.parse(text); } catch (_) { throw new Error("The local service did not return valid JSON with unique fields."); }
      if (!response.ok) {
        const error = new Error(value.error?.message || `HTTP ${response.status}`);
        error.status = response.status; error.code = value.error?.code;
        error.closedLibraryError = strict && exactKeys(value,["error"]) && exactKeys(value.error,["code","message"]) && typeof value.error.code==="string" && /^[a-z_]{1,64}$/.test(value.error.code) && scalarText(value.error.message,2*1024*1024);
        throw error;
      }
      return {value,response};
    } finally { clearTimeout(timeout); }
  }

  async function loadDevices() {
    try {
      const {value} = await request("/v1/devices");
      if (!Array.isArray(value)) throw new Error("Invalid device list format.");
      $("device-select").replaceChildren();
      for (const item of value) {
        if (!item || !/^[A-Za-z0-9_-]{1,128}$/.test(item.serial)) continue;
        const option = document.createElement("option");
        option.value = item.serial; option.textContent = `${item.serial} · ${item.connection === "online" ? "Online" : "Offline"}`;
        $("device-select").append(option);
      }
      if (serial) $("device-select").value = serial;
      if (!$("device-select").options.length) {
        const option = document.createElement("option"); option.value = ""; option.textContent = "No devices configured"; $("device-select").append(option);
      }
      if (!serial) note(value.length ? "Select and connect a device. This page never plays automatically." : "The local service has no configured devices.");
    } catch (error) { note(`Cannot read local devices: ${error.message}`,true); }
    updateButtons();
  }
  function showDevice(value) {
    if (!value || value.serial !== serial) throw new Error("Device state does not match the selected serial.");
    device = value;
    $("serial").textContent = serial;
    $("product").textContent = `${value.product || "blink(1)"}${value.firmware ? ` / ${value.firmware}` : ""}`;
    $("connection").textContent = value.connection === "online" ? "Online" : "Offline";
    const observed = value.observed;
    for (const [field,color,label] of [["led-one",observed?.led1,"rgb-one"],["led-two",observed?.led2,"rgb-two"]]) {
      const valid = typeof color === "string" && /^#[0-9a-fA-F]{6}$/.test(color);
      $(field).style.backgroundColor = valid ? color : "#000000";
      $(label).textContent = valid ? color.toLowerCase() : "—";
    }
    $("sample-time").textContent = observed?.sampled_at ? `Sampled ${new Date(observed.sampled_at).toLocaleTimeString()}` : "No RGB readback yet";
    updateButtons();
  }
  function verifyJob(value, expected) {
    if (!value || typeof value.id !== "string" || !/^[A-Za-z0-9_-]{1,160}$/.test(value.id) || (expected.jobId && value.id !== expected.jobId) || value.device !== expected.serial || !same(value.program,expected.program) || value.total_steps !== expected.totalSteps || !Number.isSafeInteger(value.completed_steps) || value.completed_steps < 0 || value.completed_steps > value.total_steps || !["queued","running","cancelling","succeeded","failed","cancelled"].includes(value.status)) throw new Error("Job response does not match the device, original job, or compiled result. Query only the original receipt.");
    return value;
  }
  function showJob(value) {
    // Terminal engine records are immutable. A late poll/cancel response for
    // that same Job must not move the page back to a nonterminal state.
    if (job?.id === value.id && terminal(job.status)) return;
    job = value;
    $("job-status").textContent = statusNames[value.status] || value.status;
    $("job-progress").textContent = `${value.completed_steps} / ${value.total_steps}`;
    $("job-id").textContent = value.id;
    const progress = value.total_steps > 0 ? Math.max(0,Math.min(100,value.completed_steps/value.total_steps*100)) : 0;
    $("job-progress-bar").style.width = `${progress}%`;
    $("job-json").textContent = JSON.stringify(value,null,2);
    updateButtons();
  }
  function previewBody(value,targetSerial=serial) {
    const target = targetSerial ? `,"serial":${JSON.stringify(targetSerial)}` : "";
    if(value.kind==="pattern") return JSON.stringify({pattern_ref:value.ref,...(targetSerial?{serial:targetSerial}:{})});
    return value.kind === "program" ? `{"program":${value.raw}${target}}` : JSON.stringify({contract,action:value.action,...(targetSerial ? {serial:targetSerial} : {})});
  }
  async function compile(value,options={}) {
    const revision=intentRevision,targetSerial=serial;
    const raw=value.kind==="program" || value.kind==="pattern";
    const path = raw ? "/v1/programs/preview" : "/v1/light/preview";
    const body = previewBody(value,targetSerial);
    if(options.render!==false) $("request-json").textContent = `POST ${path}\n\n${body}`;
    const {value:output} = await request(path,"POST",body,value.kind==="pattern");
    const preview = value.kind === "program" ? output : output.preview;
    if (!preview || preview.schema_version !== 1 || preview.compile_contract !== programContract || !preview.program || preview.validation?.program !== "valid" || !Number.isSafeInteger(preview.total_steps) || preview.total_steps < 1 || !Number.isSafeInteger(preview.nominal_duration_ms) || preview.nominal_duration_ms < 1) throw new Error("Unsupported compile preview response.");
    const facts=programFacts(preview.program);
    if(facts.totalSteps!==preview.total_steps || facts.duration!==preview.nominal_duration_ms) throw new Error("Compile response has inconsistent steps or timing.");
    if(value.kind==="pattern") {
      if(!same(output.pattern_ref,value.ref) || !same(preview.program,value.program)) throw new Error("Pattern preview does not bind the fixed revision and Program.");
    } else if (value.kind !== "program" && (output.result?.schema_version !== 1 || output.result?.contract !== contract || output.result?.action?.kind !== value.kind || output.result?.program_compile_contract !== programContract || !same(output.result.program,preview.program) || output.result.total_steps !== preview.total_steps || output.result.nominal_duration_ms !== preview.nominal_duration_ms)) throw new Error("Action and Program compilation results disagree.");
    const prepared={value,preview,output,serial:targetSerial,stale:revision!==intentRevision || targetSerial!==serial};
    if(!prepared.stale && options.render!==false) {
      $("compiled-json").textContent = JSON.stringify(output,null,2);
      $("compile-summary").textContent = `${preview.total_steps} steps · ${(preview.nominal_duration_ms / 1000).toFixed(2)} s · finish: ${preview.program.on_finish}`;
      lastPreview=prepared;
    }
    return prepared;
  }
  function randomSuffix() {
    if (!globalThis.crypto?.getRandomValues) throw new Error("This browser cannot generate secure request IDs.");
    return Array.from(crypto.getRandomValues(new Uint8Array(16)),v => v.toString(16).padStart(2,"0")).join("");
  }
  async function submit(value) {
    if (!serial) throw new Error("Connect a device first.");
    const currentIntent=intentRevision,targetSerial=serial;
    const prepared = await compile(value);
    if(prepared.stale || currentIntent!==intentRevision || targetSerial!==serial) throw new Error("Inputs changed; no light task was submitted.");
    if (prepared.preview.device?.serial !== serial || prepared.preview.validation.device !== "supported") throw new Error("The device did not pass this action’s capability check.");
    const {value:instance} = await request("/v1/instance");
    if (typeof instance.daemon_instance_id !== "string" || !instance.daemon_instance_id || instance.daemon_instance_id.length > 256 || instance.idempotency?.supported !== true || typeof instance.idempotency.request_id_prefix !== "string" || !instance.idempotency.request_id_prefix || instance.idempotency.request_id_prefix.length > 256) throw new Error("The local service does not support the required request receipts.");
    if(currentIntent!==intentRevision || targetSerial!==serial) throw new Error("Inputs changed; no light task was submitted.");
    const binding = {policy:"reject",request_id:instance.idempotency.request_id_prefix+randomSuffix(),expected_daemon_instance_id:instance.daemon_instance_id};
    const path = `/v1/devices/${encodeURIComponent(serial)}/${["program","pattern"].includes(value.kind) ? "jobs" : "light"}`;
    const body = value.kind === "program" ? `${JSON.stringify(binding).slice(0,-1)},"program":${value.raw}}` : value.kind==="pattern" ? JSON.stringify({pattern_ref:value.ref,...binding}) : JSON.stringify({contract,action:value.action,...binding});
    const submittedTicket = {serial,requestId:binding.request_id,instance:binding.expected_daemon_instance_id,program:prepared.preview.program,totalSteps:prepared.preview.total_steps,confirmed:false};
    ticket = submittedTicket;
    recoveryAttempts = 0;
    $("request-json").textContent = `POST ${path}\n\n${body}`;
    updateButtons();
    try {
      const result = await request(path,"POST",body);
      if (ticket !== submittedTicket) return null;
      if (result.response.status !== 202 || result.response.headers.get("X-NB1-Daemon-Instance-ID") !== submittedTicket.instance || !["true","false"].includes(result.response.headers.get("X-NB1-Replayed"))) throw new Error("Execution response has inconsistent receipt binding.");
      const accepted = verifyJob(result.value,submittedTicket);
      submittedTicket.confirmed = true; submittedTicket.jobId = accepted.id;
      showJob(accepted);
      const name = value.kind === "alternate" && value.action.transition === "orbit" ? names.orbit : names[value.kind];
      note(`${name} submitted to ${serial}. ${accepted.program.on_finish === "hold" ? "The final state will be held. Use Stop to turn it off." : "The light will turn off when the task finishes."}`);
      return accepted;
    } catch (error) {
      if (ticket !== submittedTicket) return null;
      if (error.status >= 400 && error.status < 500) ticket = null;
      else note("Submission is uncertain. Querying the original receipt; no automatic resubmission.",true);
      updateButtons();
      throw error;
    }
  }
  async function recover(quiet = false) {
    const expected = ticket;
    if (!expected || expected.confirmed) return job;
    try {
      const path = `/v1/devices/${encodeURIComponent(expected.serial)}/requests/${encodeURIComponent(expected.requestId)}?expected_daemon_instance_id=${encodeURIComponent(expected.instance)}`;
      const {value} = await request(path);
      if (ticket !== expected) return null;
      if (expected.confirmed) return job;
      const found = verifyJob(value,expected);
      expected.confirmed = true; expected.jobId = found.id;
      showJob(found);
      note("Recovered the job from its original receipt without replaying it.");
      return found;
    } catch (error) {
      if (ticket !== expected) return null;
      if (expected.confirmed) return job;
      if (!quiet) note(`Original receipt not confirmed: ${error.message}. Original IDs are retained; no automatic resubmission.`,true);
      return null;
    }
  }
  // One local UI namespace. Every artistic method sends an Action to the Go
  // compiler; run sends an explicit Program through its existing API. This is
  // a page-local bridge, not an installed or published JavaScript SDK.
  const light = Object.freeze({
    ...Object.fromEntries(["set","off","fade","blink","breathe","pulse","alternate","rainbow"].map(name => [name, options => submit({kind:name,action:{...options,kind:name}})])),
    run: raw => submit({kind:"program",raw}),
    pattern: value => submit(value)
  });
  async function poll() {
    const expectedSerial = serial, expectedConnection = connectionRevision;
    const expectedJob = job, expectedTicket = ticket;
    try {
      if (expectedSerial) {
        const {value} = await request(`/v1/devices/${encodeURIComponent(expectedSerial)}`);
        if (serial === expectedSerial && connectionRevision === expectedConnection) showDevice(value);
      }
      if (expectedJob && !terminal(expectedJob.status) && expectedTicket?.confirmed) {
        const {value} = await request(`/v1/jobs/${encodeURIComponent(expectedJob.id)}`);
        if (ticket === expectedTicket && job === expectedJob && !terminal(job.status)) showJob(verifyJob(value,expectedTicket));
      }
      if (ticket === expectedTicket && expectedTicket && !expectedTicket.confirmed && !busy && recoveryAttempts++ < 10) await recover(true);
    } catch (error) {
      if (serial === expectedSerial && connectionRevision === expectedConnection && ticket === expectedTicket && job === expectedJob) note(`State read temporarily unavailable: ${error.message}`,true);
    }
    pollTimer = setTimeout(poll,document.hidden ? 2000 : 600);
  }
  function startPolling() {
    if (pollStarted) return;
    pollStarted = true;
    void poll();
  }
  async function stopAndOff() {
    if (ticket && !ticket.confirmed && !await recover()) throw new Error("The original request is not confirmed. Continue querying its receipt before cancelling.");
    if (job && !terminal(job.status)) {
      const ownJob = job.id;
      const {value} = await request(`/v1/jobs/${encodeURIComponent(ownJob)}/cancel`,"POST",{});
      showJob(verifyJob(value,ticket));
      const deadline = Date.now()+12000;
      while (!terminal(job.status) && Date.now()<deadline) {
        await new Promise(resolve => setTimeout(resolve,180));
        const {value:next} = await request(`/v1/jobs/${encodeURIComponent(ownJob)}`);
        showJob(verifyJob(next,ticket));
      }
      if (!terminal(job.status)) throw new Error("Stop is awaiting device confirmation. No new off task has been sent.");
    }
    await light.off({led:0,on_finish:"hold"});
    note("An explicit off task was submitted. The reject policy preserves other clients' jobs and reports busy.");
  }
  async function operate(fn) {
    if (busy) return;
    busy = true; updateButtons();
    try { await fn(); }
    catch (error) { note(`${error.message}${ticket && !ticket.confirmed ? " Query the original receipt." : ""}`,true); }
    finally { busy = false; updateButtons(); }
  }

  function libraryNote(message,error=false,save=false) {
    const field=$(save?"pattern-save-status":"library-status"); field.textContent=message; field.classList.toggle("error",error);
  }
  function updateLibraryButtons() {
    $("library-refresh").disabled=false;
    $("library-load").disabled=busy || !libraryReady || !$("library-select").value || Boolean(loadOperation);
    $("pattern-save").disabled=busy || !libraryReady || Boolean(pendingSave || saveOperation);
    $("pattern-check-save").hidden=!pendingSave;
    $("pattern-check-save").disabled=Boolean(saveOperation || checkOperation);
    $("pattern-action").title=loadedPattern
      ? "Inspect the loaded fixed revision; only Play executes it"
      : "Load a Pattern from this computer without playing it";
    $("pattern-edit-copy").disabled=busy || kind!=="pattern" || !loadedPattern;
  }
  async function loadLibrary(preserveSelection=false) {
    const requestId=++libraryRequest;
    if(!preserveSelection) {selectionRevision++;loadOperation=null;}
    libraryReady=false; updateLibraryButtons();
    libraryNote("Reading the local Pattern library…");
    try {
      const {value}=await request("/v1/patterns","GET",undefined,true);
      const items=validateLibrary(value);
      if(requestId!==libraryRequest) return;
      const selected=$("library-select").value;
      libraryItems=items; libraryReady=true; $("library-select").replaceChildren();
      for(const item of items) {
        const option=document.createElement("option"); option.value=item.id; option.textContent=`${item.title} · ${item.id} · ${item.total_steps} steps`; $("library-select").append(option);
      }
      if(!items.length) { const option=document.createElement("option");option.value="";option.textContent="The library is empty. You can save the current effect.";$("library-select").append(option); }
      if(items.some(item=>item.id===selected)) $("library-select").value=selected;
      $("library-json").textContent=JSON.stringify(value,null,2);
      libraryNote(items.length?`Loaded ${items.length} / 128 Patterns. Select one and choose Load fixed revision; no automatic playback.`:"The library is empty. Reading creates no directory; saving writes it.");
    } catch(error) {
      if(requestId!==libraryRequest) return;
      libraryReady=false;libraryNote(`Pattern library unavailable: ${error.message}. Basic light controls remain available. An old list does not prove the current library is empty or valid.`,true);
    } finally { if(requestId===libraryRequest) updateLibraryButtons(); }
  }
  async function loadPattern() {
    const summary=libraryItems.find(item=>item.id===$("library-select").value);
    if(!libraryReady || !summary) return;
    const operation={selection:selectionRevision,intent:intentRevision,ref:{id:summary.id,revision:summary.revision}};
    loadOperation=operation;updateLibraryButtons();libraryNote(`Reading ${summary.id} fixed revision…`);
    try {
      const {value}=await request(`/v1/patterns/${encodeURIComponent(summary.id)}`,"GET",undefined,true);
      const record=await verifiedRecord(value,operation.ref);
      const facts=programFacts(record.compiled_program);
      if(record.title!==summary.title || facts.totalSteps!==summary.total_steps || facts.duration!==summary.nominal_duration_ms || record.compiled_program.on_finish!==summary.on_finish) throw new Error("The full record differs from its list summary. Refresh and select it again.");
      if(loadOperation!==operation || operation.selection!==selectionRevision || operation.intent!==intentRevision) return;
      loadedPattern={ref:clone(operation.ref),title:record.title,program:clone(record.compiled_program)};
      $("pattern-loaded").textContent=`${record.title} · ${record.id} · ${facts.totalSteps} steps · ${(facts.duration/1000).toFixed(2)} s · ${record.revision.slice(0,20)}…`;
      $("pattern-loaded").title=record.revision;$("library-json").textContent=JSON.stringify(record,null,2);
      selectAction("pattern");libraryNote(`Loaded the fixed revision of ${record.id}; not playing.`);
    } catch(error) {
      if(loadOperation===operation && operation.selection===selectionRevision && operation.intent===intentRevision) libraryNote(`Pattern not loaded: ${error.message}`,true);
    } finally { if(loadOperation===operation) { loadOperation=null;updateLibraryButtons(); } }
  }
  function definiteImportRejection(error) {
    const codes={invalid_pattern_request:400,pattern_conflict:409,pattern_library_limit:409,pattern_library_busy:409,pattern_package_too_large:413,json_required:415};
    return error.closedLibraryError===true && Object.prototype.hasOwnProperty.call(codes,error.code) && codes[error.code]===error.status;
  }
  function matchesSave(record,pending) {
    return record.id===pending.id && record.title===pending.title && exactKeys(record.source,["program"]) && same(record.source.program,pending.program) && same(record.compiled_program,pending.program);
  }
  function confirmSaved(record,pending) {
    if(pendingSave!==pending) return;
    pendingSave=null;libraryNote(`Saved ${record.title} (${record.id}) without playing. Fixed revision ${record.revision.slice(0,20)}…`,false,true);updateLibraryButtons();void loadLibrary(true);
  }
  async function saveCurrent() {
    if(pendingSave || saveOperation || !libraryReady) return;
    const id=$("pattern-id").value,title=$("pattern-title").value;
    if(!isID(id) || !scalarText(title)) { libraryNote("Enter a valid ID and a title of 1–256 characters.",true,true);return; }
    let value;
    try { value=intent(); } catch(error) { libraryNote(error.message,true,true);return; }
    const operation={intent:intentRevision,form:saveFormRevision};saveOperation=operation;updateLibraryButtons();
    libraryNote("nb1 is compiling the current effect; nothing has been written to the library.",false,true);
    try {
      const prepared=await compile(value,{render:false});
      if(saveOperation!==operation || operation.intent!==intentRevision || operation.form!==saveFormRevision || prepared.stale) { libraryNote("Inputs changed; nothing was saved. Review the effect before choosing Save again.",false,true);return; }
      const program=clone(prepared.preview.program);programFacts(program);
      const body=JSON.stringify({schema_version:1,patterns:[{id,title,program}]});
      if(new TextEncoder().encode(body).length>maxPackageBytes) throw new Error("The author package exceeds 1 MiB. No save request was sent.");
      const pending={id,title,program,state:"submitting"};pendingSave=pending;
      libraryNote(`Saving ${id} without creating a light task.`,false,true);
      $("library-json").textContent=`POST /v1/patterns/import\n\n${body}`;
      const {value:result}=await request("/v1/patterns/import","POST",body,true);
      if(pendingSave!==pending) return;
      if(!exactKeys(result,["records","created","existing","total"]) || !Array.isArray(result.records) || result.records.length!==1 || !Array.isArray(result.created) || !Array.isArray(result.existing) || !Number.isSafeInteger(result.total) || result.total<1 || result.total>128 || !same([...result.created,...result.existing],[id])) throw new Error("Save response is incomplete. Read the original ID to verify it.");
      const record=await verifiedRecord(result.records[0]);
      if(!matchesSave(record,pending)) throw new Error("Save response differs from the original ID, title, or frozen Program.");
      $("library-json").textContent=JSON.stringify(result,null,2);confirmSaved(record,pending);
    } catch(error) {
      if(pendingSave && definiteImportRejection(error)) { const rejected=pendingSave;pendingSave=null;libraryNote(`Save explicitly rejected (${rejected.id}): ${error.message}. The original record was not overwritten; edit inputs before an explicit new save.`,true,true); }
      else if(pendingSave) { pendingSave.state="unknown";libraryNote(`Save not confirmed (${pendingSave.id}): ${error.message}. The original title and Program are retained. Use Verify save; do not resubmit.`,true,true); }
      else libraryNote(`Not saved: ${error.message}`,true,true);
    } finally { if(saveOperation===operation) { saveOperation=null;updateLibraryButtons(); } }
  }
  async function checkSaved() {
    const pending=pendingSave;if(!pending || checkOperation) return;
    const operation={pending};checkOperation=operation;updateLibraryButtons();
    libraryNote(`Reading ${pending.id} to verify the original title and frozen Program…`,false,true);
    try {
      const {value}=await request(`/v1/patterns/${encodeURIComponent(pending.id)}`,"GET",undefined,true);
      const record=await verifiedRecord(value);
      if(pendingSave!==pending || checkOperation!==operation) return;
      if(!matchesSave(record,pending)) throw new Error("The record has a different title or Program. It will not be overwritten, and this save remains unconfirmed.");
      $("library-json").textContent=JSON.stringify(record,null,2);confirmSaved(record,pending);
    } catch(error) {
      if(pendingSave===pending && checkOperation===operation) libraryNote(`Save still unconfirmed (${pending.id}): ${error.status===404?"No record was read. A 404 does not prove the original save never happened.":error.message} Original request content is retained; no automatic retry.`,true,true);
    } finally { if(checkOperation===operation) {checkOperation=null;updateLibraryButtons();} }
  }
  function editPatternCopy() {
    if(!loadedPattern || kind!=="pattern") return;
    $("program").value=JSON.stringify(loadedPattern.program,null,2);selectAction("program");
    libraryNote("Created an editable Program copy. The original record and revision are unchanged.");updateLibraryButtons();
  }

  document.querySelectorAll("[data-action]").forEach(button => button.addEventListener("click",() => selectAction(button.dataset.action)));
  $("parameters").addEventListener("submit",event => event.preventDefault());
  $("parameters").addEventListener("input",() => { syncTransitionControls(); showIntent(); });
  $("transition").addEventListener("change",() => { syncTransitionControls(); showIntent(); });
  $("device-select").addEventListener("change",updateButtons);
  $("refresh").addEventListener("click",() => operate(loadDevices));
  $("connect").addEventListener("click",() => operate(async () => {
    const chosen = $("device-select").value;
    if (!/^[A-Za-z0-9_-]{1,128}$/.test(chosen)) throw new Error("Select a valid device.");
    const {value} = await request(`/v1/devices/${encodeURIComponent(chosen)}`);
    if (value.serial !== chosen) throw new Error("Device response does not match the selection.");
    serial = chosen; connectionRevision++; job = null; ticket = null; showDevice(value);
    note(`Connected to ${serial}. Choose an action to preview or explicitly play.`);
    startPolling();
  }));
  $("preview").addEventListener("click",() => operate(async () => { const result = await compile(intent()); if(result.stale) return; note(`Compiled ${result.preview.total_steps} steps. No task or light command was sent.`); }));
  $("play").addEventListener("click",() => operate(() => {
    const value = intent();
    return value.kind === "program" ? light.run(value.raw) : value.kind==="pattern" ? light.pattern(value) : light[value.kind](value.action);
  }));
  $("stop").addEventListener("click",() => operate(stopAndOff));
  $("recover").addEventListener("click",() => operate(() => recover()));
  $("library-refresh").addEventListener("click",() => {void loadLibrary();});
  $("library-select").addEventListener("change",() => {selectionRevision++;loadOperation=null;updateLibraryButtons();});
  $("library-load").addEventListener("click",() => {void loadPattern();});
  $("pattern-id").addEventListener("input",() => {saveFormRevision++;});
  $("pattern-title").addEventListener("input",() => {saveFormRevision++;});
  $("pattern-save").addEventListener("click",() => {void saveCurrent();});
  $("pattern-check-save").addEventListener("click",() => {void checkSaved();});
  $("pattern-edit-copy").addEventListener("click",editPatternCopy);
  setupColorControls();
  selectAction("set");
  loadDevices();
  void loadLibrary();
})();
