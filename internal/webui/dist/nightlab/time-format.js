// Display formatting only. The original RFC3339 value stays in dateTime/title.
const formatter = new Intl.DateTimeFormat('en-CA', {year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hourCycle:'h23',timeZoneName:'longOffset'});
export function formatSampleTime(raw) {
  if (typeof raw !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(raw)) return null;
  const date = new Date(raw);
  if (!Number.isFinite(date.getTime())) return null;
  const parts = Object.fromEntries(formatter.formatToParts(date).map(part=>[part.type,part.value]));
  return {text:`${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}:${parts.second} ${parts.timeZoneName.replace('GMT','UTC')}`,title:`${raw} · ${formatter.resolvedOptions().timeZone}`};
}
