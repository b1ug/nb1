import {formatSampleTime} from './time-format.js';

// The existing application validates device identity and cached RGB before it
// renders these nodes. This adapter never makes a request, acknowledges a USB
// command, edits an author input, or derives an observation from compilation.
const time = document.getElementById('sample-time');
if (time) {
  let displayed = '';
  const format = () => {
    const raw = time.textContent.trim(), value = formatSampleTime(raw);
    if (value) {displayed=value.text;time.dateTime=raw;time.title=value.title;time.textContent=value.text;}
    else if (raw!==displayed) {time.removeAttribute('datetime');time.removeAttribute('title');if(raw!=='—')time.textContent='—';}
  };
  new MutationObserver(format).observe(time,{childList:true});format();
  let armed = false, serial = '', previous = '';
  const publish = () => {
    if (!armed || document.getElementById('device-select')?.value!==serial) return;
    const colors=['one','two'].map(key=>document.getElementById('rgb-'+key)?.textContent?.trim()).map(value=>/^#[0-9a-f]{6}$/i.test(value||'')?value.toLowerCase():null);
    const sampledAt = time.dateTime || null;
    const detail = {colors,sampledAt,serial};
    const key=JSON.stringify(detail);if(key===previous)return;previous=key;
    window.dispatchEvent(new CustomEvent('nb1-cached-readback',{detail}));
  };
  const observer = new MutationObserver(publish);
  for(const id of ['rgb-one','rgb-two','sample-time'])observer.observe(document.getElementById(id),{childList:true,subtree:true});
  document.addEventListener('click',event=>{
    if(event.target.closest('[data-action]')?.getAttribute('aria-pressed')==='true')return;

    if(event.target.closest('[data-action], .palette, .editor-tabs, #use-sequence, #edit-sequence, #load-pattern'))armed=false;
  },true);
  window.addEventListener('nb1-playback-requested',event=>{armed=true;serial=event.detail?.serial||'';previous='';});
  const draft=event=>{if(event.target.closest('#author, #sequence-json, #device-select'))armed=false;};
  document.addEventListener('input',draft,true);document.addEventListener('change',draft,true);
}
