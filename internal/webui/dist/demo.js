"use strict";

(() => {
  const $ = id => document.getElementById(id);
  const page = window.nb1Page || {action: "set", mode: "action"};
  const hex = /^#[0-9a-f]{6}$/;
  const identifier = /^[A-Za-z0-9_-]{1,64}$/;
  const serial = /^[A-Za-z0-9_-]{1,128}$/;
  const terminal = status => ["succeeded", "cancelled", "failed"].includes(status);
  const taskKey = "nb1.web.job.v1";
  const states = {queued: "Queued", running: "Running", cancelling: "Cancelling", succeeded: "Succeeded", cancelled: "Cancelled", failed: "Failed"};
  const actions = {
    set: ["Set", "Hold one color."], off: ["Off", "Turn the selected output black."],
    fade: ["Fade", "Move smoothly from the current color to your target."], blink: ["Blink", "A clear rhythm of light and dark."],
    breathe: ["Breathe", "Rise gently from black and return."], pulse: ["Pulse", "A quick rise, a fall, and a little rest."],
    alternate: ["Alternate", "Two colors take turns between the two outputs."], orbit: ["Orbit", "Four paired fades move the color and brightness between outputs."],
    rainbow: ["Rainbow", "Travel through seven colors and return to red."]
  };
  const pairs = kind => ["alternate", "orbit"].includes(kind);
  const loops = kind => !["set", "off", "fade"].includes(kind);
  let mode = page.mode, kind = page.action, version = 0, preview = null, task = null, loaded = null, polling = null, submitting = false;
  let deviceGeneration = 0, saveID = "";

  function notice(text, error = false, detail = "") {
    $("notice").textContent = text;
    $("notice").classList.toggle("error", error);
    if (detail) $("error-detail").textContent = detail;
  }
  function report(error) { notice("Request failed. Check Details below.", true, error.message); }
  function integer(id, min, max) {
    const value = $(id).value;
    if (!/^(0|[1-9][0-9]*)$/.test(value)) throw Error("Use an integer for " + id);
    const number = Number(value);
    if (!Number.isSafeInteger(number) || number < min || number > max) throw Error("Value outside range for " + id);
    return number;
  }
  function decimal(id, max) {
    const value = $(id).value;
    if (!/^(0|[1-9][0-9]*)(\.[0-9]{1,6})?$/.test(value) || Number(value) > max) throw Error("Invalid color channel " + id);
    return Number(value);
  }
  function assertSequence(value) {
    if (!value || !["serial", "interval"].includes(value.timing) || !Array.isArray(value.steps) || !value.steps.length || value.steps.length > 1024 || !Number.isInteger(value.repeat) || value.repeat < 1 || value.repeat * value.steps.length > 1024 || !["hold", "off"].includes(value.on_finish)) throw Error("Invalid Sequence response");
    const targets = list => {
      if (!Array.isArray(list) || !list.length || list.length > 2 || list.some(t => !t || !Number.isInteger(t.led) || t.led < 0 || t.led > 2 || !hex.test(t.color))) throw Error("Invalid Sequence targets");
    };
    if (value.timing === "interval") targets(value.initial);
    for (const step of value.steps) {
      targets(step.targets);
      if (!Number.isInteger(step.fade_ms) || step.fade_ms < 0 || step.fade_ms > 600000 || step.fade_ms % 10) throw Error("Invalid Sequence fade");
      if (value.timing === "serial") {
        if (step.targets.length !== 1 || !Number.isInteger(step.hold_ms) || step.hold_ms < 0 || step.hold_ms > 3600000 || "interval_ms" in step) throw Error("Invalid serial Step");
      } else if (!Number.isInteger(step.interval_ms) || step.interval_ms < 50 || step.interval_ms > 600000 || step.fade_ms > step.interval_ms || "hold_ms" in step || step.targets.length !== value.initial.length || step.targets.some((t, i) => t.led !== value.initial[i].led || ![1, 2].includes(t.led))) throw Error("Invalid interval Step");
    }
    return value;
  }
  async function request(path, method = "GET", body) {
    const controller = new AbortController(), timeout = setTimeout(() => controller.abort(), 8000);
    try {
      const response = await fetch(path, {method, body, headers: body === undefined ? {} : {"Content-Type": "application/json"}, cache: "no-store", credentials: "same-origin", redirect: "error", signal: controller.signal});
      if (!/^application\/json\b/i.test(response.headers.get("content-type") || "")) throw Error("Expected a JSON response");
      const reader = response.body.getReader(), chunks = []; let size = 0;
      for (;;) {
        const next = await reader.read(); if (next.done) break;
        size += next.value.length;
        if (size > 1048576) { await reader.cancel(); throw Error("Response exceeds 1 MiB"); }
        chunks.push(next.value);
      }
      const bytes = new Uint8Array(size); let offset = 0;
      for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
      const value = JSON.parse(new TextDecoder("utf-8", {fatal: true}).decode(bytes));
      if (!response.ok) {
        if (!value?.error || typeof value.error.code !== "string" || typeof value.error.message !== "string") throw Error("Invalid error response");
        const error = Error(response.status + " " + value.error.code + ": " + value.error.message);
        error.rejected = ({400: ["invalid_request"], 404: ["not_found"], 409: ["device_busy", "job_conflict"], 422: ["unsupported_capability"], 429: ["job_capacity"]})[response.status]?.includes(value.error.code) || false;
        throw error;
      }
      return {value, status: response.status, location: response.headers.get("location")};
    } finally { clearTimeout(timeout); }
  }
  function envelope(input, extra = {}) {
    const members = Object.entries(extra).map(([name, value]) => JSON.stringify(name) + ":" + JSON.stringify(value));
    members.push(JSON.stringify(input.key) + ":" + input.raw);
    return "{" + members.join(",") + "}";
  }
  function colorInput(suffix) {
    const format = $("color-mode-" + suffix).value;
    if (format === "text") return $("color-" + suffix).value.trim();
    if (format === "rgb8") return {space: format, r: integer("r-" + suffix, 0, 255), g: integer("g-" + suffix, 0, 255), b: integer("b-" + suffix, 0, 255)};
    return {space: "hsv", h_deg: decimal("h_deg-" + suffix, 360), s_pct: decimal("s_pct-" + suffix, 100), v_pct: decimal("v_pct-" + suffix, 100)};
  }
  function authorInput() {
    if (mode === "sequence") {
      const raw = $("sequence-json").value.trim(); JSON.parse(raw);
      return {key: "sequence", raw}; // Preserve duplicate keys and number spelling for core validation.
    }
    if (mode === "pattern") {
      const id = $("pattern-select").value;
      if (!identifier.test(id)) throw Error("Choose a saved Pattern first");
      return {key: "pattern_id", raw: JSON.stringify(id)};
    }
    const action = {kind};
    if (pairs(kind)) { action.colors = [colorInput("a"), colorInput("b")]; action.leds = $("param-order").value.split(",").map(Number); }
    else action.led = Number($("param-led").value);
    if (!["off", "rainbow"].includes(kind) && !pairs(kind)) action.color = colorInput("a");
    const names = {fade: ["duration_ms"], blink: ["on_ms", "off_ms"], breathe: ["period_ms"], pulse: ["attack_ms", "decay_ms", "rest_ms"], alternate: ["period_ms"], orbit: ["period_ms"], rainbow: ["period_ms"]}[kind] || [];
    for (const name of names) action[name] = integer("param-" + name, 0, 86400000);
    if (loops(kind)) { action.cycles = integer("param-cycles", 1, 1024); action.on_finish = $("param-finish").value; }
    return {key: "action", raw: JSON.stringify(action)};
  }
  function draftKey() { return envelope(authorInput(), {serial: $("device-select").value}); }
  function invalidate() {
    version++; preview = null; $("preview-summary").textContent = "Draft changed";
    for (const suffix of ["a", "b"]) {
      const selected = $("color-mode-" + suffix).value === "text" ? $("color-" + suffix).value.toLowerCase().trim() : "";
      for (const button of $("palette-" + suffix).children) button.setAttribute("aria-pressed", String(button.title === selected));
    }
    updateButtons();
  }
  function updateButtons() {
    const waiting = task && (task.unknown || !terminal(task.job?.status));
    $("play").disabled = submitting || waiting || !preview || preview.version !== version || preview.check !== "supported";
    $("query").disabled = !task || submitting;
    $("stop").disabled = !task || !task.job || task.unknown || terminal(task.job.status) || task.job.status === "cancelling" || submitting;
    $("new-run").hidden = !task || !(task.unknown || terminal(task.job?.status) || task.rejected);
    $("save-pattern").disabled = mode === "pattern";
    $("use-sequence").disabled = !preview || preview.version !== version;
  }
  function changeMode(next) {
    mode = next;
    for (const name of ["action", "sequence", "pattern"]) {
      $(name + "-pane").hidden = name !== mode;
      $("mode-" + name).setAttribute("aria-selected", String(name === mode));
      $("mode-" + name).tabIndex = name === mode ? 0 : -1;
    }
    invalidate();
  }
  function selectKind(next) {
    kind = next;
    for (const button of document.querySelectorAll("[data-action]")) {
      const selected = button.dataset.action === kind; button.classList.toggle("selected", selected); button.setAttribute("aria-pressed", String(selected));
    }
    $("action-description").textContent = actions[kind][1];
    $("color-card-a").hidden = ["off", "rainbow"].includes(kind);
    $("color-card-b").hidden = !pairs(kind);
    $("action-fields").replaceChildren();
    function field(id, label, options, value) {
      const wrap = document.createElement("label"), text = document.createElement("span"); text.textContent = label; wrap.append(text);
      const element = document.createElement(options ? "select" : "input"); element.id = "param-" + id;
      if (options) for (const [v, title] of options) { const option = new Option(title, v); element.append(option); }
      else { element.type = "number"; element.min = "0"; element.max = id === "cycles" ? "1024" : "86400000"; element.step = "1"; }
      element.value = String(value); wrap.append(element); $("action-fields").append(wrap);
    }
    if (pairs(kind)) field("order", "LED order", [["1,2", "LED 1 → LED 2"], ["2,1", "LED 2 → LED 1"]], "1,2");
    else field("led", "Output", [["0", "Both LEDs"], ["1", "LED 1"], ["2", "LED 2"]], "0");
    for (const [name, label, value] of ({fade: [["duration_ms", "Fade duration · ms", 600]], blink: [["on_ms", "On duration · ms", 250], ["off_ms", "Off duration · ms", 250]], breathe: [["period_ms", "Cycle duration · ms", 2000]], pulse: [["attack_ms", "Rise · ms", 60], ["decay_ms", "Fall · ms", 240], ["rest_ms", "Rest · ms", 700]], alternate: [["period_ms", "Cycle duration · ms", 2000]], orbit: [["period_ms", "Cycle duration · ms", 3000]], rainbow: [["period_ms", "Cycle duration · ms", 3000]]}[kind] || [])) field(name, label, null, value);
    if (loops(kind)) { field("cycles", "Cycles", null, 1); field("finish", "On finish", [["off", "Off"], ["hold", "Hold final state"]], "off"); }
    syncSelectPickers();
    invalidate();
  }
  function renderChannels(suffix) {
    const format = $("color-mode-" + suffix).value, parent = $("channels-" + suffix);
    syncSelectPickers($("color-mode-" + suffix));
    $("text-" + suffix).hidden = format !== "text"; parent.hidden = format === "text"; parent.replaceChildren();
    const channels = format === "rgb8" ? [["r", "R", 0, 255], ["g", "G", 160, 255], ["b", "B", 96, 255]] : [["h_deg", "H · °", 156, 360], ["s_pct", "S · %", 100, 100], ["v_pct", "B · %", 63, 100]];
    for (const [name, label, value, max] of channels) {
      const wrap = document.createElement("label"); wrap.textContent = label;
      const input = document.createElement("input"); input.id = name + "-" + suffix; input.type = "number"; input.min = "0"; input.max = String(max); input.step = format === "rgb8" ? "1" : "0.000001"; input.value = String(value); wrap.append(input); parent.append(wrap);
    }
  }
  async function previewDraft() {
    const input = authorInput(), draftVersion = version, device = $("device-select").value;
    const body = envelope(input, device ? {serial: device} : {});
    $("input-detail").textContent = body;
    const {value} = await request("/v1/preview", "POST", body);
    assertSequence(value.sequence);
    if (!Number.isInteger(value.total_steps) || value.total_steps < 1 || value.total_steps > 1024 || !Number.isInteger(value.nominal_duration_ms) || value.nominal_duration_ms < 0 || value.nominal_duration_ms > 86400000 || !Array.isArray(value.required_capabilities) || value.required_capabilities.some(v => typeof v !== "string") || !["not_requested", "supported", "unsupported", "unknown"].includes(value.device_check?.status)) throw Error("Invalid preview response");
    if (draftVersion !== version) { notice("Draft changed. Preview again."); return; }
    preview = {version, key: draftKey(), check: value.device_check.status, sequence: value.sequence};
    $("compiled").textContent = JSON.stringify(value.sequence, null, 2);
    $("preview-summary").textContent = "Preview ready";
    $("preview-check").textContent = "Steps: " + value.total_steps + " · " + value.nominal_duration_ms + " ms · " + value.device_check.status;
    $("request-details").open = true;
    notice("Preview ready. The light is unchanged."); updateButtons();
  }
  function saveTask() { try { sessionStorage.setItem(taskKey, JSON.stringify(task)); } catch (_) { /* The in-page ID remains available. */ } }
  function showTask() {
    $("job-id").textContent = task?.id || "—";
    $("job-status").textContent = task?.unknown ? "Outcome unknown" : task?.rejected ? "Submission rejected" : states[task?.job?.status] || "Not submitted";
    $("job-status").dataset.state = task?.unknown ? "unknown" : task?.rejected ? "failed" : task?.job?.status || "idle";
    $("job-progress").textContent = task?.job ? task.job.completed_steps + " / " + task.job.total_steps : "—";
    $("job-progress-bar").max = task?.job?.total_steps || 1; $("job-progress-bar").value = task?.job?.completed_steps || 0;
    $("job-message").textContent = task?.unknown ? "Keep this Job ID and query it. Decide any new run explicitly." : "A Job is complete when device cleanup is confirmed.";
    updateButtons();
  }
  function validateJob(value) {
    if (!value || value.id !== task.id || value.serial !== task.serial || !Object.hasOwn(states, value.status) || !Number.isInteger(value.total_steps) || value.total_steps < 1 || value.total_steps > 1024 || !Number.isInteger(value.completed_steps) || value.completed_steps < 0 || value.completed_steps > value.total_steps || !Number.isFinite(Date.parse(value.created_at))) throw Error("Job response identity or fields did not match");
    if (task.job && (value.created_at !== task.job.created_at || value.total_steps !== task.job.total_steps || value.completed_steps < task.job.completed_steps || terminal(task.job.status) && value.status !== task.job.status)) throw Error("Job response changed an established identity or outcome");
    if (terminal(value.status) !== (typeof value.finished_at === "string" && Number.isFinite(Date.parse(value.finished_at))) || value.status === "succeeded" && value.completed_steps !== value.total_steps) throw Error("Job terminal state is inconsistent");
    return value;
  }
  function scheduleQuery() { clearTimeout(polling); if (task?.job && !task.unknown && !terminal(task.job.status)) polling = setTimeout(() => queryJob().catch(uncertainJob), 500); }
  function acceptJob(value) { task.job = validateJob(value); task.unknown = false; task.rejected = false; saveTask(); showTask(); scheduleQuery(); }
  function uncertainJob(error) { clearTimeout(polling); if (task?.job && terminal(task.job.status)) { notice("Stored terminal result retained. Query failed.", true, error.message); return; } if (task) { task.unknown = true; saveTask(); showTask(); } notice("Outcome unknown. Keep the Job ID and query it.", true, error.message); }
  async function queryJob() { if (!task) return; const id = task.id; const reply = await request("/v1/jobs/" + encodeURIComponent(id)); if (task?.id !== id) return; acceptJob(reply.value); readDevice().catch(report); }
  async function play() {
    if (!preview || preview.version !== version || preview.key !== draftKey() || preview.check !== "supported") throw Error("Preview the current draft for this device first");
    const input = authorInput(), device = $("device-select").value;
    const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ", random = crypto.getRandomValues(new Uint8Array(10));
    task = {id: "j_" + [...random].map(v => alphabet[v & 31]).join(""), serial: device, unknown: true, job: null};
    saveTask(); showTask(); submitting = true; updateButtons();
    try {
      const reply = await request("/v1/jobs", "POST", envelope(input, {id: task.id, serial: device}));
      if (![200, 202].includes(reply.status) || reply.location !== "/v1/jobs/" + task.id) throw Error("Job acceptance response was not bound to the submitted ID");
      acceptJob(reply.value); notice("Job accepted. Follow its progress here.");
    } catch (error) {
      if (error.rejected) { task.unknown = false; task.rejected = true; saveTask(); showTask(); report(error); }
      else uncertainJob(error);
    } finally { submitting = false; updateButtons(); }
  }
  async function stopJob() {
    if (!task?.job || task.unknown || terminal(task.job.status)) return;
    clearTimeout(polling); submitting = true; updateButtons();
    try { const reply = await request("/v1/jobs/" + encodeURIComponent(task.id) + "/cancel", "POST"); if (![200, 202].includes(reply.status)) throw Error("Invalid cancellation response"); acceptJob(reply.value); }
    catch (error) { uncertainJob(error); }
    finally { submitting = false; updateButtons(); }
  }
  // One presentation layer for every single-select dropdown. The native select
  // remains authoritative; committing a choice forwards its existing change event.
  function installSelectPickers() {
    const entries = new Map();
    let opened = null;
    const eligible = select => !select.multiple && select.size <= 1;
    const labelFor = select => {
      if (select.hasAttribute("aria-label")) return {name: select.getAttribute("aria-label")};
      if (select.hasAttribute("aria-labelledby")) return {ids: select.getAttribute("aria-labelledby")};
      const labels = [...select.labels].map((label, index) => {
        // An implicit field label also contains the control. Reference only its
        // authored caption, never the generated popup and its complete option list.
        const caption = label.contains(select) ? label.querySelector(":scope > span:not(.select-picker)") : label;
        if (!caption) return "";
        if (!caption.id) caption.id = select.id + "-label-" + index;
        return caption.id;
      }).filter(Boolean);
      return labels.length ? {ids: labels.join(" ")} : {name: select.id};
    };
    function mount(select) {
      if (!select.id || !eligible(select) || entries.has(select)) return;
      let control = select.closest(".device-picker");
      const ownsControl = !control, wasHidden = select.hidden;
      if (!control) { control = document.createElement("span"); select.before(control); control.append(select); }
      control.classList.add("select-picker");
      const trigger = document.createElement("button"), current = document.createElement("span"), chevron = document.createElement("span");
      const listbox = document.createElement("span");
      trigger.type = "button"; trigger.id = select.id + "-trigger"; trigger.className = "select-trigger";
      trigger.setAttribute("aria-haspopup", "listbox"); trigger.setAttribute("aria-expanded", "false");
      trigger.setAttribute("aria-controls", select.id + "-options"); trigger.setAttribute("data-i18n-skip", "");
      current.id = select.id + "-current"; current.className = "select-current";
      chevron.className = "picker-chevron"; chevron.setAttribute("aria-hidden", "true"); trigger.append(current, chevron);
      listbox.id = select.id + "-options"; listbox.className = "select-options"; listbox.hidden = true;
      listbox.setAttribute("role", "listbox"); listbox.setAttribute("data-i18n-skip", ""); listbox.tabIndex = -1;
      let options = [], signature = "", activeIndex = 0, typeBuffer = "", typeAt = 0;
      const isDisabled = item => item.disabled || item.parentElement?.disabled === true;
      const available = () => [...select.options].map((item, index) => isDisabled(item) ? -1 : index).filter(index => index >= 0);
      const entry = {control, select, trigger, listbox, close, sync, destroy};
      entries.set(select, entry);
      function close(returnFocus = false) {
        listbox.hidden = true; trigger.setAttribute("aria-expanded", "false"); control.classList.remove("is-open");
        if (opened === entry) opened = null;
        if (returnFocus && trigger.isConnected && !trigger.disabled) trigger.focus({preventScroll: true});
      }
      function focusOption(index) {
        const enabled = available(); if (!enabled.length) return;
        activeIndex = enabled.includes(index) ? index : enabled[0];
        options[activeIndex].focus({preventScroll: true}); options[activeIndex].scrollIntoView({block: "nearest"});
      }
      function open(index = select.selectedIndex) {
        sync(); if (trigger.disabled) return;
        document.dispatchEvent(new CustomEvent("nb1-menu-open", {detail: control}));
        opened = entry; listbox.hidden = false; trigger.setAttribute("aria-expanded", "true"); control.classList.add("is-open");
        focusOption(index);
      }
      function choose(index) {
        if (!select.options[index] || isDisabled(select.options[index])) return;
        const before = select.value; select.selectedIndex = index;
        close(true); sync();
        if (select.value !== before) select.dispatchEvent(new Event("change", {bubbles: true}));
      }
      function sync() {
        const source = [...select.options], label = labelFor(select);
        const next = JSON.stringify([select.value, select.selectedIndex, select.disabled, label, source.map(item => [item.value, item.textContent, item.selected, isDisabled(item)])]);
        if (next === signature) return;
        signature = next;
        const heldFocus = listbox.contains(document.activeElement);
        if (opened === entry) close(heldFocus);
        listbox.replaceChildren(); options = [];
        for (const [index, item] of source.entries()) {
          const option = document.createElement("span"), title = document.createElement("span"), mark = document.createElement("span");
          option.id = select.id + "-option-" + index; option.className = "select-option"; option.setAttribute("role", "option"); option.tabIndex = -1;
          option.setAttribute("aria-selected", String(item.selected)); option.setAttribute("aria-disabled", String(isDisabled(item)));
          title.textContent = item.textContent; mark.className = "picker-check"; mark.setAttribute("aria-hidden", "true"); option.append(title, mark);
          option.addEventListener("click", () => choose(index)); option.addEventListener("focus", () => { activeIndex = index; });
          options.push(option); listbox.append(option);
        }
        const selected = select.selectedOptions[0]?.textContent || "—";
        current.textContent = selected; trigger.title = selected;
        trigger.disabled = select.disabled || !source.some(item => !isDisabled(item) && item.value !== "");
        for (const element of [trigger, listbox]) {
          element.removeAttribute("aria-label"); element.removeAttribute("aria-labelledby");
          if (label.ids) element.setAttribute("aria-labelledby", label.ids + (element === trigger ? " " + current.id : ""));
          else element.setAttribute("aria-label", label.name + (element === trigger ? ": " + selected : ""));
        }
      }
      trigger.addEventListener("click", () => opened === entry ? close(true) : open());
      trigger.addEventListener("keydown", event => {
        if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
          event.preventDefault(); const enabled = available(); open(event.key === "Home" ? enabled[0] : event.key === "End" ? enabled.at(-1) : select.selectedIndex);
        } else if (event.key === "Escape" && opened === entry) { event.preventDefault(); close(true); }
      });
      listbox.addEventListener("keydown", event => {
        const enabled = available();
        if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
          event.preventDefault(); const position = enabled.indexOf(activeIndex);
          focusOption(event.key === "Home" ? enabled[0] : event.key === "End" ? enabled.at(-1) : enabled[(position + (event.key === "ArrowDown" ? 1 : -1) + enabled.length) % enabled.length]);
        } else if (event.key === "Enter" || event.key === " ") { event.preventDefault(); choose(activeIndex); }
        else if (event.key === "Escape") { event.preventDefault(); close(true); }
        else if (event.key === "Tab") close(true);
        else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
          const now = Date.now(); typeBuffer = (now - typeAt < 700 ? typeBuffer : "") + event.key.toLowerCase(); typeAt = now;
          const index = enabled.find(index => sourceText(index).startsWith(typeBuffer));
          if (index !== undefined) { event.preventDefault(); focusOption(index); }
        }
      });
      function sourceText(index) { return select.options[index].textContent.trim().toLocaleLowerCase(); }
      function focusOut(event) { if (opened === entry && event.relatedTarget && !control.contains(event.relatedTarget)) close(); }
      function destroy() {
        close(false); select.removeEventListener("change", sync); control.removeEventListener("focusout", focusOut);
        trigger.remove(); listbox.remove();
        if (ownsControl && control.isConnected && select.parentElement === control) { control.before(select); control.remove(); }
        else control.classList.remove("select-picker");
        select.hidden = wasHidden;
      }
      control.addEventListener("focusout", focusOut);
      select.addEventListener("change", sync);
      control.append(trigger, listbox); select.hidden = true; sync();
    }
    function reconcile() {
      for (const [select, entry] of entries) {
        if (!select.isConnected || !eligible(select)) {
          entry.destroy(); entries.delete(select);
        }
      }
      for (const select of document.querySelectorAll("select")) mount(select);
    }
    function sync(select) { reconcile(); if (select) entries.get(select)?.sync(); else for (const entry of entries.values()) entry.sync(); }
    // Only authored select/option or label mutations matter. Ignore copied UI
    // and locale-menu writes, which must not feed back into the i18n observer.
    const observer = new MutationObserver(records => {
      let structural = false, labels = false; const dirty = new Set();
      for (const record of records) {
        const element = record.target.nodeType === Node.ELEMENT_NODE ? record.target : record.target.parentElement;
        if (!element || element.closest(".select-trigger, .select-options, .locale-control")) continue;
        const select = element.closest("select"); if (select) dirty.add(select);
        if (select && record.type === "attributes" && ["multiple", "size"].includes(record.attributeName)) structural = true;
        if (element.closest("label")) labels = true;
        if (record.type === "childList" && [...record.addedNodes, ...record.removedNodes].some(node => node.nodeType === Node.ELEMENT_NODE && (node.matches("select") || node.querySelector("select")))) structural = true;
      }
      if (structural) reconcile();
      if (labels) for (const select of entries.keys()) dirty.add(select);
      for (const select of dirty) entries.get(select)?.sync();
    });
    observer.observe(document.body, {subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ["disabled", "label", "value", "selected", "aria-label", "aria-labelledby", "multiple", "size"]});
    document.addEventListener("nb1-menu-open", event => { if (opened && opened.control !== event.detail) opened.close(false); });
    document.addEventListener("click", event => {
      if (opened && !opened.control.contains(event.target)) {
        const active = document.activeElement; opened.close(active === document.body || opened.listbox.contains(active));
      }
    });
    reconcile(); return sync;
  }
  const syncSelectPickers = installSelectPickers();
  async function refreshDevices() {
    const {value} = await request("/v1/devices");
    if (!Array.isArray(value) || value.length > 64 || value.some(d => !serial.test(d.serial) || typeof d.connection !== "string")) throw Error("Invalid device list");
    const before = $("device-select").value; $("device-select").replaceChildren();
    for (const device of value) $("device-select").append(new Option(device.serial, device.serial));
    if (!value.length) $("device-select").append(new Option("—", ""));
    if (value.some(d => d.serial === before)) $("device-select").value = before;
    syncSelectPickers($("device-select"));
    if (before !== $("device-select").value) invalidate();
    $("device-message").textContent = value.length ? "Selected from the local service." : "No devices configured.";
    await readDevice();
  }
  async function readDevice() {
    const id = $("device-select").value, generation = ++deviceGeneration;
    if (!id) {
      $("connection").textContent = "Disconnected"; $("connection").dataset.state = "disconnected";
      for (const [suffix, key] of [["one", "led1"], ["two", "led2"]]) {
        $("led-" + suffix).style.backgroundColor = "#e4e7d5"; $("led-" + suffix).dataset.available = "false"; $("rgb-" + suffix).textContent = "—";
      }
      $("sample-time").textContent = "—";
      return;
    }
    const {value} = await request("/v1/devices/" + id);
    if (generation !== deviceGeneration || id !== $("device-select").value) return;
    if (value.serial !== id || typeof value.connection !== "string" || value.observed !== null && (!value.observed || ![value.observed.led1, value.observed.led2].every(v => v === null || hex.test(v)))) throw Error("Invalid cached device state");
    $("connection").textContent = value.connection === "online" ? "Online" : "Offline";
    $("connection").dataset.state = value.connection === "online" ? "online" : "offline";
    for (const [suffix, key] of [["one", "led1"], ["two", "led2"]]) { const color = value.observed?.[key]; $("led-" + suffix).style.backgroundColor = color || "#e4e7d5"; $("led-" + suffix).dataset.available = String(Boolean(color)); $("rgb-" + suffix).textContent = color || "—"; }
    $("sample-time").textContent = value.observed?.sampled_at || "—";
  }
  async function refreshColors() {
    const {value} = await request("/v1/colors");
    if (!Array.isArray(value) || value.length > 256 || value.some(c => !/^[a-z][a-z0-9-]{0,31}$/.test(c.name) || !hex.test(c.color))) throw Error("Invalid local color list");
    $("color-list").replaceChildren(); for (const color of value) $("color-list").append(new Option("@" + color.name + " · " + color.color, color.name));
  }
  async function refreshPatterns() {
    const {value} = await request("/v1/patterns");
    if (!Array.isArray(value) || value.length > 128 || value.some(p => !identifier.test(p.id) || p.title !== undefined && (typeof p.title !== "string" || [...p.title].length > 256))) throw Error("Invalid Pattern summaries");
    const selected = $("pattern-select").value; $("pattern-select").replaceChildren();
    for (const pattern of value) $("pattern-select").append(new Option(pattern.title ? pattern.id + " · " + pattern.title : pattern.id, pattern.id));
    if (!value.length) $("pattern-select").append(new Option("—", ""));
    if (value.some(p => p.id === selected)) $("pattern-select").value = selected;
    syncSelectPickers($("pattern-select"));
  }
  async function readPattern(id = $("pattern-select").value) {
    if (!identifier.test(id)) throw Error("Choose a saved Pattern first");
    const {value} = await request("/v1/patterns/" + encodeURIComponent(id));
    if (value.id !== id || value.title !== undefined && (typeof value.title !== "string" || [...value.title].length > 256)) throw Error("Invalid Pattern identity");
    assertSequence(value.sequence); loaded = value;
    $("selected-pattern").textContent = value.title || value.id; $("compiled").textContent = JSON.stringify(value.sequence, null, 2);
    $("pattern-id").value = value.id; $("pattern-title").value = value.title || ""; $("edit-sequence").disabled = false;
    notice("Pattern read. The light is unchanged.");
  }
  async function savePattern() {
    const input = authorInput(); if (input.key === "pattern_id") throw Error("Choose an Action or Sequence to save");
    const id = $("pattern-id").value, title = $("pattern-title").value;
    if (!identifier.test(id) || [...title].length > 256) throw Error("Use a valid Pattern ID and a title of at most 256 characters");
    saveID = id; $("verify-save").hidden = false; $("save-pattern").disabled = true;
    try {
      const {value} = await request("/v1/patterns/" + encodeURIComponent(id), "PUT", envelope(input, {title}));
      if (value.id !== id) throw Error("Pattern save response identity did not match"); assertSequence(value.sequence);
      $("save-message").textContent = "Pattern saved. No Job was created."; await refreshPatterns();
    } catch (error) { $("save-message").textContent = "Save outcome unknown. Read the saved ID before deciding what to do next."; throw error; }
    finally { updateButtons(); }
  }
  function bind(id, fn, failure = report) { $(id).addEventListener("click", () => Promise.resolve().then(fn).catch(failure)); }

  $("sequence-json").value = JSON.stringify({timing: "serial", steps: [{targets: [{led: 0, color: "#ff0000"}], fade_ms: 100, hold_ms: 400}, {targets: [{led: 0, color: "#0000ff"}], fade_ms: 100, hold_ms: 400}], repeat: 1, on_finish: "off"}, null, 2);
  for (const [name, [label]] of Object.entries(actions)) {
    const button = document.createElement("button"); button.type = "button"; button.className = "action"; button.dataset.action = name;
    const icon = document.createElement("span"); icon.className = "action-icon-wrap"; icon.setAttribute("aria-hidden", "true"); icon.append(window.NB1ActionIcons.create(name));
    const text = document.createElement("strong"); text.textContent = label; button.append(icon, text); button.addEventListener("click", () => selectKind(name)); document.querySelector(".action-grid").append(button);
  }
  for (const name of ["action", "sequence", "pattern"]) {
    $("mode-" + name).addEventListener("click", () => changeMode(name));
    $("mode-" + name).addEventListener("keydown", event => { if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return; event.preventDefault(); const names = ["action", "sequence", "pattern"], index = (names.indexOf(mode) + (event.key === "ArrowRight" ? 1 : 2)) % 3; changeMode(names[index]); $("mode-" + mode).focus(); });
  }
  for (const suffix of ["a", "b"]) {
    renderChannels(suffix);
    $("color-mode-" + suffix).addEventListener("change", () => { renderChannels(suffix); invalidate(); });
    $("picker-" + suffix).addEventListener("input", () => { $("color-mode-" + suffix).value = "text"; renderChannels(suffix); $("color-" + suffix).value = $("picker-" + suffix).value; invalidate(); });
    for (const color of ["#ff0000", "#ffa500", "#ffff00", "#00ff00", "#00a060", "#00ffff", "#0000ff", "#8f00ff", "#ffffff"]) {
      const button = document.createElement("button"); button.type = "button"; button.style.backgroundColor = color; button.title = color; button.setAttribute("aria-label", color); button.setAttribute("aria-pressed", "false");
      button.addEventListener("click", () => { $("color-mode-" + suffix).value = "text"; renderChannels(suffix); $("color-" + suffix).value = color; $("picker-" + suffix).value = color; invalidate(); }); $("palette-" + suffix).append(button);
    }
    bind("resolve-" + suffix, async () => { const input = colorInput(suffix), captured = JSON.stringify(input); const {value} = await request("/v1/colors/resolve", "POST", JSON.stringify({color: input})); if (!hex.test(value.color)) throw Error("Invalid resolved color"); if (captured === JSON.stringify(colorInput(suffix))) { $("resolved-" + suffix).textContent = value.color; $("picker-" + suffix).value = value.color; } });
  }
  $("author").addEventListener("input", invalidate);
  $("author").addEventListener("change", event => { if (event.target.matches("input,select,textarea")) invalidate(); });
  $("device-select").addEventListener("change", () => { invalidate(); readDevice().catch(report); });
  $("pattern-select").addEventListener("change", () => { loaded = null; $("edit-sequence").disabled = true; });
  bind("use-sequence", () => { if (preview) { $("sequence-json").value = JSON.stringify(preview.sequence, null, 2); changeMode("sequence"); } });
  bind("preview", previewDraft); bind("play", play); bind("query", queryJob, uncertainJob); bind("stop", stopJob);
  bind("refresh", refreshDevices); bind("colors-refresh", refreshColors); bind("patterns-refresh", refreshPatterns); bind("load-pattern", () => readPattern());
  bind("edit-sequence", () => { if (loaded) { $("sequence-json").value = JSON.stringify(loaded.sequence, null, 2); changeMode("sequence"); } });
  bind("use-color", () => { const name = $("color-list").value; if (!name) return; $("color-mode-a").value = "text"; renderChannels("a"); $("color-a").value = "@" + name; changeMode("action"); });
  bind("save-color", async () => { const name = $("color-name").value; if (!/^[a-z][a-z0-9-]{0,31}$/.test(name)) throw Error("Use a lowercase color name, up to 32 characters"); const {value} = await request("/v1/colors/" + encodeURIComponent(name), "PUT", JSON.stringify({color: colorInput("a")})); if (value.name !== name || !hex.test(value.color)) throw Error("Invalid saved color"); await refreshColors(); notice("Color saved. The light is unchanged."); });
  bind("save-pattern", savePattern); bind("verify-save", () => readPattern(saveID));
  bind("new-run", () => { clearTimeout(polling); try { const history = JSON.parse(sessionStorage.getItem(taskKey + ".history") || "[]"); sessionStorage.setItem(taskKey + ".history", JSON.stringify([...history.slice(-7), task])); } catch (_) { /* Keep the ID visible until explicit new-run selection. */ } task = null; saveTask(); showTask(); notice("Ready for a separate run. Previous IDs stay in this tab's history."); });
  selectKind(kind); changeMode(mode);
  try { const saved = JSON.parse(sessionStorage.getItem(taskKey) || "null"); if (saved && identifier.test(saved.id) && serial.test(saved.serial)) { task = {id: saved.id, serial: saved.serial, unknown: true, job: null}; showTask(); } } catch (_) { /* Ignore invalid browser data. */ }
  Promise.allSettled([refreshDevices(), refreshColors(), refreshPatterns()]).then(results => { const failed = results.find(result => result.status === "rejected"); if (failed) report(failed.reason); });
  window.addEventListener("pagehide", () => clearTimeout(polling));
})();
