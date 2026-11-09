"use strict";

(() => {
  const contract = "nb1-light-action/v1";
  const programContract = "nb1-program-effect/v1";
  const $ = id => document.getElementById(id);
  const names = {set:"常亮",fade:"渐变",off:"熄灭",blink:"闪烁",breathe:"呼吸",pulse:"脉冲",alternate:"红蓝交替",rainbow:"彩虹",program:"精确序列"};
  const descriptions = {
    set:"保持指定颜色，直到你再次操作或明确熄灭。", fade:"从当前颜色逐渐变到目标颜色，完成后保持。", off:"明确发送黑色，让所选输出熄灭。",
    blink:"直接亮、直接灭。亮灭时长与循环次数都明确可见。", breathe:"平滑、对称地渐亮渐暗。曲线由 nb1 核心的固定采样表编译。",
    pulse:"快速亮起，随后衰减，再留出一段静默。它与对称呼吸不同。", alternate:"LED 1 与 LED 2 交换两种颜色。两点轮转没有可区分的顺逆方向。",
    rainbow:"沿红、黄、绿、青、蓝、紫、红的固定颜色路径渐变。", program:"直接提供有限 Program，与艺术动作使用同一个设备执行器。"
  };
  const terminal = status => ["succeeded","failed","cancelled"].includes(status);
  const statusNames = {queued:"排队中",running:"运行中",cancelling:"正在停止",succeeded:"已完成",failed:"执行失败",cancelled:"已停止"};
  let kind = "set", serial = null, device = null, job = null, ticket = null, busy = false, lastPreview = null;
  let pollTimer = null, pollStarted = false, connectionRevision = 0, recoveryAttempts = 0;

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
  }
  function canonical(value) {
    if (Array.isArray(value)) return value.map(canonical);
    if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(k => [k, canonical(value[k])]));
    return value;
  }
  const same = (a, b) => JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
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
    try {
      const value = intent();
      const {kind: ignoredKind, ...options} = value.action || {};
      $("surface-call").textContent = value.kind === "program" ? "light.run(program)" : `light.${value.kind}(${JSON.stringify(options)})`;
    } catch (_) { $("surface-call").textContent = `light.${kind === "program" ? "run" : kind}(…)`; }
  }
  function selectAction(value) {
    kind = value;
    document.querySelectorAll("[data-action]").forEach(b => { const active = b.dataset.action === kind; b.classList.toggle("selected",active); b.setAttribute("aria-pressed",String(active)); });
    $("kind-label").textContent = `${["set","fade","off","program"].includes(kind) ? "基础" : "艺术"} · ${kind === "program" ? "run" : kind}`;
    $("action-description").textContent = descriptions[kind];
    const rows = {"single-color-row":!["off","alternate","rainbow","program"].includes(kind),"alternate-row":kind==="alternate","led-row":!["alternate","program"].includes(kind),"duration-row":kind==="fade","period-row":["breathe","alternate","rainbow"].includes(kind),"transition-row":kind==="alternate","blink-row":kind==="blink","pulse-row":kind==="pulse","cycles-row":["blink","breathe","pulse","alternate","rainbow"].includes(kind),"finish-row":["blink","breathe","pulse","alternate","rainbow"].includes(kind),"program-row":kind==="program"};
    for (const [id, visible] of Object.entries(rows)) $(id).hidden = !visible;
    syncTransitionControls();
    showIntent();
  }

  async function request(path, method = "GET", body) {
    if (!path.startsWith("/") || path.startsWith("//")) throw new Error("仅允许同源 API 路径。");
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const response = await fetch(path,{method,credentials:"omit",cache:"no-store",redirect:"error",signal:controller.signal,headers:{Accept:"application/json",...(body === undefined ? {} : {"Content-Type":"application/json"})},...(body === undefined ? {} : {body:typeof body === "string" ? body : JSON.stringify(body)})});
      const text = await response.text();
      if (text.length > 2 * 1024 * 1024) throw new Error("API 响应超过本页接收上限。");
      let value;
      try { value = JSON.parse(text); } catch (_) { throw new Error("本地服务没有返回有效 JSON。"); }
      if (!response.ok) {
        const error = new Error(value.error?.message || `HTTP ${response.status}`);
        error.status = response.status; error.code = value.error?.code;
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
  function previewBody(value) {
    const target = serial ? `,"serial":${JSON.stringify(serial)}` : "";
    return value.kind === "program" ? `{"program":${value.raw}${target}}` : JSON.stringify({contract,action:value.action,...(serial ? {serial} : {})});
  }
  async function compile(value) {
    const path = value.kind === "program" ? "/v1/programs/preview" : "/v1/light/preview";
    const body = previewBody(value);
    $("request-json").textContent = `POST ${path}\n\n${body}`;
    const {value:output} = await request(path,"POST",body);
    const preview = value.kind === "program" ? output : output.preview;
    if (!preview || preview.schema_version !== 1 || preview.compile_contract !== programContract || !preview.program || preview.validation?.program !== "valid" || !Number.isSafeInteger(preview.total_steps) || preview.total_steps < 1 || !Number.isSafeInteger(preview.nominal_duration_ms) || preview.nominal_duration_ms < 1) throw new Error("不支持的编译预览响应。");
    if (value.kind !== "program" && (output.result?.schema_version !== 1 || output.result?.contract !== contract || output.result?.action?.kind !== value.kind || output.result?.program_compile_contract !== programContract || !same(output.result.program,preview.program) || output.result.total_steps !== preview.total_steps || output.result.nominal_duration_ms !== preview.nominal_duration_ms)) throw new Error("动作与 Program 的共同编译结果不一致。");
    $("compiled-json").textContent = JSON.stringify(output,null,2);
    $("compile-summary").textContent = `${preview.total_steps} 步 · ${(preview.nominal_duration_ms / 1000).toFixed(2)} 秒 · 完成后 ${preview.program.on_finish}`;
    lastPreview = {value,preview,output,serial};
    return lastPreview;
  }
  function randomSuffix() {
    if (!globalThis.crypto?.getRandomValues) throw new Error("浏览器不支持安全的请求标识生成。");
    return Array.from(crypto.getRandomValues(new Uint8Array(16)),v => v.toString(16).padStart(2,"0")).join("");
  }
  async function submit(value) {
    if (!serial) throw new Error("请先明确连接一个设备。");
    const prepared = await compile(value);
    if (prepared.preview.device?.serial !== serial || prepared.preview.validation.device !== "supported") throw new Error("当前设备未通过此动作的能力检查。");
    const {value:instance} = await request("/v1/instance");
    if (typeof instance.daemon_instance_id !== "string" || !instance.daemon_instance_id || instance.daemon_instance_id.length > 256 || instance.idempotency?.supported !== true || typeof instance.idempotency.request_id_prefix !== "string" || !instance.idempotency.request_id_prefix || instance.idempotency.request_id_prefix.length > 256) throw new Error("当前本地服务不支持完整请求回执。");
    const binding = {policy:"reject",request_id:instance.idempotency.request_id_prefix+randomSuffix(),expected_daemon_instance_id:instance.daemon_instance_id};
    const path = `/v1/devices/${encodeURIComponent(serial)}/${value.kind === "program" ? "jobs" : "light"}`;
    const body = value.kind === "program" ? `${JSON.stringify(binding).slice(0,-1)},"program":${value.raw}}` : JSON.stringify({contract,action:value.action,...binding});
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
    run: raw => submit({kind:"program",raw})
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
  $("preview").addEventListener("click",() => operate(async () => { const result = await compile(intent()); note(`编译完成：${result.preview.total_steps} 步。没有创建任务或发送灯光指令。`); }));
  $("play").addEventListener("click",() => operate(() => {
    const value = intent();
    return value.kind === "program" ? light.run(value.raw) : light[value.kind](value.action);
  }));
  $("stop").addEventListener("click",() => operate(stopAndOff));
  $("recover").addEventListener("click",() => operate(() => recover()));
  setupColorControls();
  selectAction("set");
  loadDevices();
})();
