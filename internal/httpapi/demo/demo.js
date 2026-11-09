"use strict";

(() => {
  const contract = "nb1-light-action/v1";
  const programContract = "nb1-program-effect/v1";
  const $ = id => document.getElementById(id);
  const names = {set:"常亮",fade:"渐变",off:"熄灭",blink:"闪烁",breathe:"呼吸",pulse:"脉冲",alternate:"红蓝交替",rainbow:"彩虹",program:"精确序列",pattern:"已存图案"};
  const descriptions = {
    set:"保持指定颜色，直到你再次操作或明确熄灭。", fade:"从当前颜色逐渐变到目标颜色，完成后保持。", off:"明确发送黑色，让所选输出熄灭。",
    blink:"直接亮、直接灭。亮灭时长与循环次数都明确可见。", breathe:"平滑、对称地渐亮渐暗。曲线由 nb1 核心的固定采样表编译。",
    pulse:"快速亮起，随后衰减，再留出一段静默。它与对称呼吸不同。", alternate:"LED 1 与 LED 2 交换两种颜色。两点轮转没有可区分的顺逆方向。",
    rainbow:"沿红、黄、绿、青、蓝、紫、红的固定颜色路径渐变。", program:"直接提供有限 Program，与艺术动作使用同一个设备执行器。", pattern:"播放明确载入的固定图案版本；不重新解释来源，也不会自动更新版本。"
  };
  const terminal = status => ["succeeded","failed","cancelled"].includes(status);
  const statusNames = {queued:"排队中",running:"运行中",cancelling:"正在停止",succeeded:"已完成",failed:"执行失败",cancelled:"已停止"};
  let kind = "set", serial = null, device = null, job = null, ticket = null, busy = false, lastPreview = null;
  let pollTimer = null, pollStarted = false, connectionRevision = 0, recoveryAttempts = 0;
  let intentRevision=0, libraryRequest=0, selectionRevision=0, libraryReady=false, libraryItems=[], loadedPattern=null, pendingSave=null, saveOperation=null, checkOperation=null, loadOperation=null, saveFormRevision=0;

  // A small editing palette, not another CSS color parser. Other names stay
  // untouched and are resolved by the authoritative nb1 compiler on request.
  const palette = [
    {name:"red",label:"红",hex:"#ff0000"}, {name:"orange",label:"橙",hex:"#ffa500"},
    {name:"gold",label:"金",hex:"#ffd700"}, {name:"lime",label:"亮绿",hex:"#00ff00"},
    {name:"green",label:"绿",hex:"#008000"}, {name:"cyan",label:"青",hex:"#00ffff"},
    {name:"blue",label:"蓝",hex:"#0000ff"}, {name:"purple",label:"紫",hex:"#800080"},
    {name:"magenta",label:"品红",hex:"#ff00ff"}, {name:"white",label:"白",hex:"#ffffff"},
    {name:"gray",label:"灰",hex:"#808080"}, {name:"black",label:"黑",hex:"#000000"}
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
        hint.textContent = hex ? `${hex} · 仅编辑颜色，尚未发送。` : "保留原文字，由 nb1 解析；取色器显示上次可识别的颜色。";
      }
      function choose(hex) {
        input.value = hex;
        sync();
        showIntent();
      }
      for (const color of palette) {
        const button = document.createElement("button");
        button.type = "button"; button.className = "color-swatch"; button.dataset.hex = color.hex;
        button.setAttribute("aria-label",`${color.label}色 ${color.name} ${color.hex}`);
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
    if (!exactKeys(program,["steps","repeat","on_finish"]) || !Array.isArray(program.steps) || program.steps.length<1 || program.steps.length>1024 || !Number.isSafeInteger(program.repeat) || program.repeat<1 || program.repeat>10000 || !["off","hold"].includes(program.on_finish)) throw new Error("图案的冻结 Program 格式不受支持。");
    let duration=0;
    for (const step of program.steps) {
      if (!exactKeys(step,["led","color","fade_ms","hold_ms"]) || ![0,1,2].includes(step.led) || typeof step.color!=="string" || !/^#[0-9a-f]{6}$/.test(step.color) || !Number.isSafeInteger(step.fade_ms) || step.fade_ms<0 || step.fade_ms>600000 || step.fade_ms%10 || !Number.isSafeInteger(step.hold_ms) || step.hold_ms<0 || step.hold_ms>3600000) throw new Error("图案的步骤不是规范 Program。");
      duration+=step.fade_ms+step.hold_ms+10;
    }
    duration*=program.repeat;
    if (!Number.isSafeInteger(duration) || duration>86400000) throw new Error("图案超过有限时长限制。");
    return {totalSteps:program.steps.length*program.repeat,duration};
  }
  function strictLibraryJSON(text) {
    const value=JSON.parse(text); let i=0;
    const ws=()=>{ while (/\s/.test(text[i]||"")) i++; };
    function string() { const start=i++; while(i<text.length) { if(text[i]==="\\") { i+=2; continue; } if(text[i++]==='"') break; } const s=JSON.parse(text.slice(start,i)); if (s.length && !scalarText(s,2*1024*1024)) throw new Error("图案库响应含无效 Unicode。"); return s; }
    function walk(depth) {
      if(depth>32) throw new Error("图案库响应嵌套过深。"); ws(); const c=text[i];
      if(c==='{') { i++; ws(); const keys=new Set(); if(text[i]==='}') { i++; return; } while(true) { ws(); const key=string(); if(keys.has(key)) throw new Error("图案库响应含重复字段。"); keys.add(key); ws(); i++; walk(depth+1); ws(); if(text[i++]==='}') break; } }
      else if(c==='[') { i++; ws(); if(text[i]===']') { i++; return; } while(true) { walk(depth+1); ws(); if(text[i++]===']') break; } }
      else if(c==='"') string();
      else if(c==='t') i+=4;
      else if(c==='f') i+=5;
      else if(c==='n') i+=4;
      else { const match=/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(text.slice(i)); if(!match || !/^(?:0|[1-9]\d*)$/.test(match[0])) throw new Error("图案库响应整数格式不规范。"); i+=match[0].length; }
    }
    walk(0); ws(); if(i!==text.length) throw new Error("图案库响应有尾随数据。"); return value;
  }
  function canonicalText(value) {
    const hex="0123456789abcdef";
    return '"'+value.replace(/[\u0000-\u001f"\\]/g,c=>c==='"'?'\\"':c==='\\'?'\\\\':'\\u00'+hex[c.charCodeAt(0)>>4]+hex[c.charCodeAt(0)&15])+'"';
  }
  function canonicalProgram(value) {
    return '{"steps":['+value.steps.map(s=>`{"led":${s.led},"color":${canonicalText(s.color)},"fade_ms":${s.fade_ms},"hold_ms":${s.hold_ms}}`).join(',')+`],"repeat":${value.repeat},"on_finish":${canonicalText(value.on_finish)}}`;
  }
  async function verifiedRecord(value, expected) {
    if (!exactKeys(value,["id","title","source","compiled_program","compile_contract","revision"]) || !isID(value.id) || !scalarText(value.title) || value.compile_contract!==programContract || !isRevision(value.revision) || expected && (value.id!==expected.id || value.revision!==expected.revision)) throw new Error("图案记录与所选 ID / revision 不一致。");
    programFacts(value.compiled_program);
    let source;
    if (exactKeys(value.source,["program"])) {
      programFacts(value.source.program);
      if(!same(value.source.program,value.compiled_program)) throw new Error("图案来源 Program 与冻结结果不一致。");
      source='{"program":'+canonicalProgram(value.source.program)+'}';
    } else if (exactKeys(value.source,["effect"])) {
      // The Store validates legacy Effect expansion. The page binds its
      // declared source/hash and frozen Program, never recompiles the Effect.
      // Execution still resolves this exact ref and compares daemon preview.
      const effect=value.source.effect;
      const fields=effect?.kind==='steady'?["kind","color","led","fade_ms"]:["kind","color","led","cycles","period_ms","on_finish"];
      if(!exactKeys(effect,fields) || !["steady","pulse"].includes(effect.kind) || !/^#[0-9a-f]{6}$/.test(effect.color) || ![0,1,2].includes(effect.led)) throw new Error("图案来源 Effect 版本不受支持。");
      if(effect.kind==='steady') {
        if(!Number.isSafeInteger(effect.fade_ms) || effect.fade_ms<0 || effect.fade_ms>600000 || effect.fade_ms%10) throw new Error("图案来源 Effect 参数不规范。");
        source=`{"effect":{"kind":"steady","color":${canonicalText(effect.color)},"led":${effect.led},"fade_ms":${effect.fade_ms}}}`;
      } else {
        if(!Number.isSafeInteger(effect.cycles) || effect.cycles<1 || effect.cycles>10000 || !Number.isSafeInteger(effect.period_ms) || effect.period_ms<60 || effect.period_ms>1200000 || effect.period_ms%10 || !["off","hold"].includes(effect.on_finish)) throw new Error("图案来源 Effect 参数不规范。");
        source=`{"effect":{"kind":"pulse","color":${canonicalText(effect.color)},"led":${effect.led},"cycles":${effect.cycles},"period_ms":${effect.period_ms},"on_finish":${canonicalText(effect.on_finish)}}}`;
      }
    } else throw new Error("图案必须有且仅有一个受支持的来源。");
    if(!globalThis.crypto?.subtle) throw new Error("浏览器无法核对图案 SHA-256；保留普通灯光控制。");
    const canonicalRecord=`{"id":${canonicalText(value.id)},"title":${canonicalText(value.title)},"source":${source},"compiled_program":${canonicalProgram(value.compiled_program)},"compile_contract":${canonicalText(value.compile_contract)}}`;
    const bytes=await crypto.subtle.digest("SHA-256",new TextEncoder().encode(recordDomain+"\n"+canonicalRecord));
    const revision="sha256:"+Array.from(new Uint8Array(bytes),x=>x.toString(16).padStart(2,"0")).join("");
    if(revision!==value.revision) throw new Error("图案内容摘要与 revision 不一致。");
    return clone(value);
  }
  function validateLibrary(value) {
    if(!exactKeys(value,["schema_version","contract","library","patterns"]) || value.schema_version!==1 || value.contract!==libraryContract || !exactKeys(value.library,["path","state","create_only","max_records","max_package_bytes","record_contract","compile_contract","snapshot_kind"]) || (typeof value.library.path!=="string" || value.library.path.length>4096) || value.library.state!=="ready" || value.library.create_only!==true || value.library.max_records!==128 || value.library.max_package_bytes!==maxPackageBytes || value.library.record_contract!==recordDomain || value.library.compile_contract!==programContract || value.library.snapshot_kind!=="nb1-pattern-snapshot" || !Array.isArray(value.patterns) || value.patterns.length>128) throw new Error("图案库合同或容量范围不受支持。");
    const ids=new Set();
    for(const item of value.patterns) {
      if(!exactKeys(item,["id","title","revision","compile_contract","total_steps","nominal_duration_ms","on_finish","required_capabilities"]) || !isID(item.id) || ids.has(item.id) || !scalarText(item.title) || !isRevision(item.revision) || item.compile_contract!==programContract || !Number.isSafeInteger(item.total_steps) || item.total_steps<1 || item.total_steps>10240000 || !Number.isSafeInteger(item.nominal_duration_ms) || item.nominal_duration_ms<10 || item.nominal_duration_ms>86400000 || !["off","hold"].includes(item.on_finish) || !Array.isArray(item.required_capabilities) || item.required_capabilities.length>64 || !item.required_capabilities.every(x=>typeof x==="string" && /^[A-Za-z0-9_-]{1,64}$/.test(x))) throw new Error("图案列表包含不受支持的摘要。");
      ids.add(item.id);
    }
    return clone(value.patterns);
  }

  function integer(id) {
    const field = $(id), value = field.value;
    if (!/^\d+$/.test(value) || !field.checkValidity()) throw new Error("请填写范围内的整数时间和次数。");
    return Number(value);
  }
  const transitionDescriptions = {
    cut:"直接交换两种颜色，保留原有交替方式。",
    linear:"颜色按线性比例往返混合，形成连续交替。",
    sine:"颜色按正弦曲线平滑往返，变化在两端放缓。",
    orbit:"颜色混合与亮度相差 1/4 周期，形成环绕相位感；不是实际三维旋转。"
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
    const limits = flowing ? "每周期 66 步；本页周期 1300–10000ms，最多 15 次。两颗 LED 依次写入，不是原子同步。" : "本页周期 400–10000ms，最多 10 次。";
    $("transition-help").textContent = `${transitionDescriptions[mode] || "请选择有效的交替方式。"} ${limits}${invalidPeriod || invalidCycles ? " 当前数值超出范围，请修改后再预览或播放；不会自动替换你的输入。" : ""}`;
    $("transition-help").classList.toggle("invalid",invalidPeriod || invalidCycles);
  }
  function intent(forcedKind) {
    const selected = forcedKind || kind;
    if (selected === "pattern") {
      if(!loadedPattern) throw new Error("请先明确载入一个固定图案版本。");
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
      if (!Object.prototype.hasOwnProperty.call(transitionDescriptions,transition)) throw new Error("请选择有效的交替方式。");
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
    if(value==="pattern" && !loadedPattern) return;
    kind = value;
    if(value!=="pattern") {loadedPattern=null;$("pattern-loaded").textContent="尚未载入图案。";}
    document.querySelectorAll("[data-action]").forEach(b => { const active = b.dataset.action === kind; b.classList.toggle("selected",active); b.setAttribute("aria-pressed",String(active)); });
    $("kind-label").textContent = `${["set","fade","off","program","pattern"].includes(kind) ? "基础" : "艺术"} · ${kind === "program" ? "run" : kind}`;
    $("action-description").textContent = descriptions[kind];
    $("pattern-row").hidden=kind!=="pattern";
    const rows = {"single-color-row":!["off","alternate","rainbow","program","pattern"].includes(kind),"alternate-row":kind==="alternate","led-row":!["alternate","program","pattern"].includes(kind),"duration-row":kind==="fade","period-row":["breathe","alternate","rainbow"].includes(kind),"transition-row":kind==="alternate","blink-row":kind==="blink","pulse-row":kind==="pulse","cycles-row":["blink","breathe","pulse","alternate","rainbow"].includes(kind),"finish-row":["blink","breathe","pulse","alternate","rainbow"].includes(kind),"program-row":kind==="program"};
    for (const [id, visible] of Object.entries(rows)) $(id).hidden = !visible;
    syncTransitionControls();
    showIntent();
    updateLibraryButtons();
  }

  async function request(path, method = "GET", body, strict = false) {
    if (!path.startsWith("/") || path.startsWith("//")) throw new Error("仅允许同源 API 路径。");
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const response = await fetch(path,{method,credentials:"omit",cache:"no-store",redirect:"error",signal:controller.signal,headers:{Accept:"application/json",...(body === undefined ? {} : {"Content-Type":"application/json"})},...(body === undefined ? {} : {body:typeof body === "string" ? body : JSON.stringify(body)})});
      let text;
      if(strict) {
        if(!/^application\/json(?:\s*;\s*charset=(?:utf-8|"utf-8"))?$/i.test(response.headers.get("Content-Type")||"")) throw new Error("图案库响应不是唯一 JSON 类型。");
        const buffer=await response.arrayBuffer(); if(buffer.byteLength>2*1024*1024) throw new Error("图案库响应超过本页接收上限。");
        text=new TextDecoder("utf-8",{fatal:true}).decode(buffer);
      } else text=await response.text();
      if (text.length > 2 * 1024 * 1024) throw new Error("API 响应超过本页接收上限。");
      let value;
      try { value = strict ? strictLibraryJSON(text) : JSON.parse(text); } catch (_) { throw new Error("本地服务没有返回有效、唯一字段的 JSON。"); }
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
      if (!Array.isArray(value)) throw new Error("设备列表格式不正确。");
      $("device-select").replaceChildren();
      for (const item of value) {
        if (!item || !/^[A-Za-z0-9_-]{1,128}$/.test(item.serial)) continue;
        const option = document.createElement("option");
        option.value = item.serial; option.textContent = `${item.serial} · ${item.connection === "online" ? "在线" : "离线"}`;
        $("device-select").append(option);
      }
      if (serial) $("device-select").value = serial;
      if (!$("device-select").options.length) {
        const option = document.createElement("option"); option.value = ""; option.textContent = "服务未配置设备"; $("device-select").append(option);
      }
      if (!serial) note(value.length ? "请选择设备并连接；页面不会自动播放。" : "本地服务没有已配置的设备。");
    } catch (error) { note(`无法读取本地设备：${error.message}`,true); }
    updateButtons();
  }
  function showDevice(value) {
    if (!value || value.serial !== serial) throw new Error("设备状态与所选序列号不一致。");
    device = value;
    $("serial").textContent = serial;
    $("product").textContent = `${value.product || "blink(1)"}${value.firmware ? ` / ${value.firmware}` : ""}`;
    $("connection").textContent = value.connection === "online" ? "在线" : "离线";
    const observed = value.observed;
    for (const [field,color,label] of [["led-one",observed?.led1,"rgb-one"],["led-two",observed?.led2,"rgb-two"]]) {
      const valid = typeof color === "string" && /^#[0-9a-fA-F]{6}$/.test(color);
      $(field).style.backgroundColor = valid ? color : "#000000";
      $(label).textContent = valid ? color.toLowerCase() : "—";
    }
    $("sample-time").textContent = observed?.sampled_at ? `采样 ${new Date(observed.sampled_at).toLocaleTimeString()}` : "尚无 RGB 读回";
    updateButtons();
  }
  function verifyJob(value, expected) {
    if (!value || typeof value.id !== "string" || !/^[A-Za-z0-9_-]{1,160}$/.test(value.id) || (expected.jobId && value.id !== expected.jobId) || value.device !== expected.serial || !same(value.program,expected.program) || value.total_steps !== expected.totalSteps || !Number.isSafeInteger(value.completed_steps) || value.completed_steps < 0 || value.completed_steps > value.total_steps || !["queued","running","cancelling","succeeded","failed","cancelled"].includes(value.status)) throw new Error("Job 响应与本次设备、原任务或编译结果不一致；仅查询原回执确认。");
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
    if (!preview || preview.schema_version !== 1 || preview.compile_contract !== programContract || !preview.program || preview.validation?.program !== "valid" || !Number.isSafeInteger(preview.total_steps) || preview.total_steps < 1 || !Number.isSafeInteger(preview.nominal_duration_ms) || preview.nominal_duration_ms < 1) throw new Error("不支持的编译预览响应。");
    const facts=programFacts(preview.program);
    if(facts.totalSteps!==preview.total_steps || facts.duration!==preview.nominal_duration_ms) throw new Error("编译响应的步骤或时间不一致。");
    if(value.kind==="pattern") {
      if(!same(output.pattern_ref,value.ref) || !same(preview.program,value.program)) throw new Error("图案预览没有绑定原固定版本和 Program。");
    } else if (value.kind !== "program" && (output.result?.schema_version !== 1 || output.result?.contract !== contract || output.result?.action?.kind !== value.kind || output.result?.program_compile_contract !== programContract || !same(output.result.program,preview.program) || output.result.total_steps !== preview.total_steps || output.result.nominal_duration_ms !== preview.nominal_duration_ms)) throw new Error("动作与 Program 的共同编译结果不一致。");
    const prepared={value,preview,output,serial:targetSerial,stale:revision!==intentRevision || targetSerial!==serial};
    if(!prepared.stale && options.render!==false) {
      $("compiled-json").textContent = JSON.stringify(output,null,2);
      $("compile-summary").textContent = `${preview.total_steps} 步 · ${(preview.nominal_duration_ms / 1000).toFixed(2)} 秒 · 完成后 ${preview.program.on_finish}`;
      lastPreview=prepared;
    }
    return prepared;
  }
  function randomSuffix() {
    if (!globalThis.crypto?.getRandomValues) throw new Error("浏览器不支持安全的请求标识生成。");
    return Array.from(crypto.getRandomValues(new Uint8Array(16)),v => v.toString(16).padStart(2,"0")).join("");
  }
  async function submit(value) {
    if (!serial) throw new Error("请先明确连接一个设备。");
    const currentIntent=intentRevision,targetSerial=serial;
    const prepared = await compile(value);
    if(prepared.stale || currentIntent!==intentRevision || targetSerial!==serial) throw new Error("输入已改变，本次未提交灯光任务。");
    if (prepared.preview.device?.serial !== serial || prepared.preview.validation.device !== "supported") throw new Error("当前设备未通过此动作的能力检查。");
    const {value:instance} = await request("/v1/instance");
    if (typeof instance.daemon_instance_id !== "string" || !instance.daemon_instance_id || instance.daemon_instance_id.length > 256 || instance.idempotency?.supported !== true || typeof instance.idempotency.request_id_prefix !== "string" || !instance.idempotency.request_id_prefix || instance.idempotency.request_id_prefix.length > 256) throw new Error("当前本地服务不支持完整请求回执。");
    if(currentIntent!==intentRevision || targetSerial!==serial) throw new Error("输入已改变，本次未提交灯光任务。");
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
      if (result.response.status !== 202 || result.response.headers.get("X-NB1-Daemon-Instance-ID") !== submittedTicket.instance || !["true","false"].includes(result.response.headers.get("X-NB1-Replayed"))) throw new Error("执行响应的回执绑定不一致。");
      const accepted = verifyJob(result.value,submittedTicket);
      submittedTicket.confirmed = true; submittedTicket.jobId = accepted.id;
      showJob(accepted);
      note(`${names[value.kind]}已提交到 ${serial}。${accepted.program.on_finish === "hold" ? "完成后保留最后状态；需要熄灭请点击停止按钮。" : "任务完成后会熄灭。"}`);
      return accepted;
    } catch (error) {
      if (ticket !== submittedTicket) return null;
      if (error.status >= 400 && error.status < 500) ticket = null;
      else note("提交结果尚不确定：正在查询原请求回执，不会自动重发。",true);
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
      note("已通过原回执找回任务；没有重新执行。");
      return found;
    } catch (error) {
      if (ticket !== expected) return null;
      if (expected.confirmed) return job;
      if (!quiet) note(`原回执暂未确认：${error.message}。保留原标识，不自动重发。`,true);
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
      if (serial === expectedSerial && connectionRevision === expectedConnection && ticket === expectedTicket && job === expectedJob) note(`状态读取暂不可用：${error.message}`,true);
    }
    pollTimer = setTimeout(poll,document.hidden ? 2000 : 600);
  }
  function startPolling() {
    if (pollStarted) return;
    pollStarted = true;
    void poll();
  }
  async function stopAndOff() {
    if (ticket && !ticket.confirmed && !await recover()) throw new Error("原请求结果尚未确认，无法安全取消；请继续查询原回执。");
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
      if (!terminal(job.status)) throw new Error("停止仍在等待设备确认；尚未发送新的熄灭任务。");
    }
    await light.off({led:0,on_finish:"hold"});
    note("已提交明确熄灭。若有其他客户端的任务，reject 策略会保留它并报告忙碌。");
  }
  async function operate(fn) {
    if (busy) return;
    busy = true; updateButtons();
    try { await fn(); }
    catch (error) { note(`${error.message}${ticket && !ticket.confirmed ? " 请查询原请求回执。" : ""}`,true); }
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
    $("pattern-action").disabled=!loadedPattern;
    $("pattern-edit-copy").disabled=busy || kind!=="pattern" || !loadedPattern;
  }
  async function loadLibrary(preserveSelection=false) {
    const requestId=++libraryRequest;
    if(!preserveSelection) {selectionRevision++;loadOperation=null;}
    libraryReady=false; updateLibraryButtons();
    libraryNote("正在读取本地图案库…");
    try {
      const {value}=await request("/v1/patterns","GET",undefined,true);
      const items=validateLibrary(value);
      if(requestId!==libraryRequest) return;
      const selected=$("library-select").value;
      libraryItems=items; libraryReady=true; $("library-select").replaceChildren();
      for(const item of items) {
        const option=document.createElement("option"); option.value=item.id; option.textContent=`${item.title} · ${item.id} · ${item.total_steps} 步`; $("library-select").append(option);
      }
      if(!items.length) { const option=document.createElement("option");option.value="";option.textContent="图案库为空；可以保存当前效果。";$("library-select").append(option); }
      if(items.some(item=>item.id===selected)) $("library-select").value=selected;
      $("library-json").textContent=JSON.stringify(value,null,2);
      libraryNote(items.length?`已读取 ${items.length} / 128 条图案。选择后点击「载入固定版本」，不会自动播放。`:"图案库为空。读取不会创建目录；保存才写入。");
    } catch(error) {
      if(requestId!==libraryRequest) return;
      libraryReady=false;libraryNote(`图案库不可用：${error.message}。保留普通灯光控制；旧列表不代表当前库为空或有效。`,true);
    } finally { if(requestId===libraryRequest) updateLibraryButtons(); }
  }
  async function loadPattern() {
    const summary=libraryItems.find(item=>item.id===$("library-select").value);
    if(!libraryReady || !summary) return;
    const operation={selection:selectionRevision,intent:intentRevision,ref:{id:summary.id,revision:summary.revision}};
    loadOperation=operation;updateLibraryButtons();libraryNote(`正在读取 ${summary.id} 的固定版本…`);
    try {
      const {value}=await request(`/v1/patterns/${encodeURIComponent(summary.id)}`,"GET",undefined,true);
      const record=await verifiedRecord(value,operation.ref);
      const facts=programFacts(record.compiled_program);
      if(record.title!==summary.title || facts.totalSteps!==summary.total_steps || facts.duration!==summary.nominal_duration_ms || record.compiled_program.on_finish!==summary.on_finish) throw new Error("完整记录与列表摘要不一致；请刷新后重新选择。");
      if(loadOperation!==operation || operation.selection!==selectionRevision || operation.intent!==intentRevision) return;
      loadedPattern={ref:clone(operation.ref),title:record.title,program:clone(record.compiled_program)};
      $("pattern-loaded").textContent=`${record.title} · ${record.id} · ${facts.totalSteps} 步 · ${(facts.duration/1000).toFixed(2)} 秒 · ${record.revision.slice(0,20)}…`;
      $("pattern-loaded").title=record.revision;$("library-json").textContent=JSON.stringify(record,null,2);
      selectAction("pattern");libraryNote(`已载入 ${record.id} 的固定版本，尚未播放。`);
    } catch(error) {
      if(loadOperation===operation && operation.selection===selectionRevision && operation.intent===intentRevision) libraryNote(`未载入图案：${error.message}`,true);
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
    pendingSave=null;libraryNote(`已确认保存「${record.title}」(${record.id})，没有播放。固定版本 ${record.revision.slice(0,20)}…`,false,true);updateLibraryButtons();void loadLibrary(true);
  }
  async function saveCurrent() {
    if(pendingSave || saveOperation || !libraryReady) return;
    const id=$("pattern-id").value,title=$("pattern-title").value;
    if(!isID(id) || !scalarText(title)) { libraryNote("请填写有效 ID 和 1–256 字符的显示名称。",true,true);return; }
    let value;
    try { value=intent(); } catch(error) { libraryNote(error.message,true,true);return; }
    const operation={intent:intentRevision,form:saveFormRevision};saveOperation=operation;updateLibraryButtons();
    libraryNote("正在由 nb1 编译当前效果；尚未写入图案库。",false,true);
    try {
      const prepared=await compile(value,{render:false});
      if(saveOperation!==operation || operation.intent!==intentRevision || operation.form!==saveFormRevision || prepared.stale) { libraryNote("输入已改变，本次没有保存。请检查当前效果后再次点击保存。",false,true);return; }
      const program=clone(prepared.preview.program);programFacts(program);
      const body=JSON.stringify({schema_version:1,patterns:[{id,title,program}]});
      if(new TextEncoder().encode(body).length>maxPackageBytes) throw new Error("作者包超过 1 MiB，未发送保存请求。");
      const pending={id,title,program,state:"submitting"};pendingSave=pending;
      libraryNote(`正在保存 ${id}；不会创建灯光任务。`,false,true);
      $("library-json").textContent=`POST /v1/patterns/import\n\n${body}`;
      const {value:result}=await request("/v1/patterns/import","POST",body,true);
      if(pendingSave!==pending) return;
      if(!exactKeys(result,["records","created","existing","total"]) || !Array.isArray(result.records) || result.records.length!==1 || !Array.isArray(result.created) || !Array.isArray(result.existing) || !Number.isSafeInteger(result.total) || result.total<1 || result.total>128 || !same([...result.created,...result.existing],[id])) throw new Error("保存响应不完整；需读取原 ID 核对。");
      const record=await verifiedRecord(result.records[0]);
      if(!matchesSave(record,pending)) throw new Error("保存响应与原 ID、名称或冻结 Program 不一致。");
      $("library-json").textContent=JSON.stringify(result,null,2);confirmSaved(record,pending);
    } catch(error) {
      if(pendingSave && definiteImportRejection(error)) { const rejected=pendingSave;pendingSave=null;libraryNote(`保存被明确拒绝（${rejected.id}）：${error.message}。没有覆盖原记录；可修改输入后再次明确保存。`,true,true); }
      else if(pendingSave) { pendingSave.state="unknown";libraryNote(`保存结果尚未确认（${pendingSave.id}）：${error.message}。已保留原名称和 Program，禁止重复保存；请点击「核对保存结果」。`,true,true); }
      else libraryNote(`没有保存：${error.message}`,true,true);
    } finally { if(saveOperation===operation) { saveOperation=null;updateLibraryButtons(); } }
  }
  async function checkSaved() {
    const pending=pendingSave;if(!pending || checkOperation) return;
    const operation={pending};checkOperation=operation;updateLibraryButtons();
    libraryNote(`只读取 ${pending.id}，核对原名称和冻结 Program…`,false,true);
    try {
      const {value}=await request(`/v1/patterns/${encodeURIComponent(pending.id)}`,"GET",undefined,true);
      const record=await verifiedRecord(value);
      if(pendingSave!==pending || checkOperation!==operation) return;
      if(!matchesSave(record,pending)) throw new Error("同 ID 记录的名称或 Program 不同；不会覆盖，也不能确认本次保存。");
      $("library-json").textContent=JSON.stringify(record,null,2);confirmSaved(record,pending);
    } catch(error) {
      if(pendingSave===pending && checkOperation===operation) libraryNote(`保存仍未确认（${pending.id}）：${error.status===404?"尚未读到记录；404 不能证明原保存没有发生。":error.message} 保留原请求内容，不自动重试。`,true,true);
    } finally { if(checkOperation===operation) {checkOperation=null;updateLibraryButtons();} }
  }
  function editPatternCopy() {
    if(!loadedPattern || kind!=="pattern") return;
    $("program").value=JSON.stringify(loadedPattern.program,null,2);selectAction("program");
    libraryNote("已创建可编辑的 Program 副本；原图案记录和 revision 没有改变。");updateLibraryButtons();
  }

  document.querySelectorAll("[data-action]").forEach(button => button.addEventListener("click",() => selectAction(button.dataset.action)));
  $("parameters").addEventListener("submit",event => event.preventDefault());
  $("parameters").addEventListener("input",() => { syncTransitionControls(); showIntent(); });
  $("transition").addEventListener("change",() => { syncTransitionControls(); showIntent(); });
  $("device-select").addEventListener("change",updateButtons);
  $("refresh").addEventListener("click",() => operate(loadDevices));
  $("connect").addEventListener("click",() => operate(async () => {
    const chosen = $("device-select").value;
    if (!/^[A-Za-z0-9_-]{1,128}$/.test(chosen)) throw new Error("请选择有效设备。");
    const {value} = await request(`/v1/devices/${encodeURIComponent(chosen)}`);
    if (value.serial !== chosen) throw new Error("设备响应与选择不一致。");
    serial = chosen; connectionRevision++; job = null; ticket = null; showDevice(value);
    note(`已连接 ${serial}。选择动作后可仅预览，或明确播放到设备。`);
    startPolling();
  }));
  $("preview").addEventListener("click",() => operate(async () => { const result = await compile(intent()); if(result.stale) return; note(`编译完成：${result.preview.total_steps} 步。没有创建任务或发送灯光指令。`); }));
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
