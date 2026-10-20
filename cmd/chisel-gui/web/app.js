'use strict';
const $ = id => document.getElementById(id);
const form = $('editor');
const fields = ['name','server','auth','fingerprint','proxy','headers','keepAlive','minRetry','maxRetry','retries','ca','cert','key','serverName','skipVerify','verbose'];
const tokenKey = 'chisel-token:' + location.host;
let token = location.hash.slice(1) || sessionStorage.getItem(tokenKey) || '';
if (token) sessionStorage.setItem(tokenKey, token);
history.replaceState(null, '', '/');
let state = {profiles: [], sessions: {}}, current = null, dirty = false, busy = false, online = true;

function notice(message, error = false) {
  $('notice').textContent = message; $('notice').className = error ? 'error' : ''; $('notice').hidden = false;
}
async function api(path, data) {
  const response = await fetch('/api/' + path, {method:data ? 'POST' : 'GET', headers:{'X-Chisel-Token':token,'Content-Type':'application/json'}, ...(data ? {body:JSON.stringify(data)} : {})});
  if (!response.ok) throw new Error((await response.text()).trim());
  return response.json();
}
function collect() {
  const p = {id: current};
  for (const name of fields) { const f = form.elements[name]; p[name] = f.type === 'checkbox' ? f.checked : f.value; }
  p.retries = Number(p.retries); p.remotes = form.elements.remotes.value.split('\n').map(s => s.trim()).filter(Boolean);
  return p;
}
function select(p) {
  current = p.id; form.reset();
  for (const name of fields) { const f = form.elements[name]; if (f.type === 'checkbox') f.checked = !!p[name]; else f.value = p[name] ?? (name === 'retries' ? -1 : ''); }
  form.elements.remotes.value = (p.remotes || []).join('\n');
  dirty = false; $('notice').hidden = true; render();
}
function fresh() { select({id:crypto.randomUUID(),name:'',server:'',remotes:[],retries:-1}); form.elements.name.focus(); }
async function confirmAction(title, message) {
  $('confirm-title').textContent = title; $('confirm-message').textContent = message;
  const dialog = $('confirm'); dialog.showModal();
  return new Promise(resolve => {
    const finish = result => { dialog.close(); dialog.oncancel = null; resolve(result); };
    $('cancel-confirm').onclick = () => finish(false); $('accept-confirm').onclick = () => finish(true);
    dialog.oncancel = event => { event.preventDefault(); finish(false); };
  });
}
async function mayLeave() { return !dirty || await confirmAction('Discard unsaved changes?', 'Your edits to this profile have not been saved.'); }
function render() {
  $('profiles').replaceChildren();
  for (const p of state.profiles) {
    const button = document.createElement('button'); button.type = 'button'; button.className = p.id === current ? 'selected' : '';
    const status = state.sessions[p.id]?.status || 'disconnected';
    const text = document.createElement('span'); text.textContent = (status === 'connected' ? '●  ' : '○  ') + p.name;
    const sub = document.createElement('small'); sub.textContent = p.server; button.append(text, sub);
    button.onclick = async () => { if (p.id !== current && await mayLeave()) select(p); }; $('profiles').append(button);
  }
  if (!state.profiles.length) { const empty = document.createElement('p'); empty.textContent = 'Add your first connection using + above.'; empty.className = 'hint'; $('profiles').append(empty); }
  const s = state.sessions[current], status = s?.status || 'disconnected';
  const active = ['connected','connecting','reconnecting'].includes(status);
  $('title').textContent = form.elements.name.value || 'New connection';
  $('status').textContent = '● ' + status[0].toUpperCase() + status.slice(1); $('status').className = 'badge ' + status;
  $('connect').textContent = active ? '■ Disconnect' : '↗ Connect'; $('connect').disabled = busy || !online;
  $('save').disabled = busy || !online; $('delete').disabled = busy || active || !state.profiles.some(p => p.id === current);
  $('tunnel-count').textContent = collect().remotes.length + ' TUNNELS';
  $('save-state').textContent = dirty ? 'Unsaved changes' : 'Changes apply on your next connection.';
  const log = $('logs'), atEnd = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  const content = s?.logs?.length ? s.logs.join('\n') : 'Your next connection starts here.\nSave a profile and click Connect to open a tunnel.';
  if (log.textContent !== content) { log.textContent = content; if (atEnd) log.scrollTop = log.scrollHeight; }
  $('activity-caption').textContent = active ? 'Session started ' + new Date(s.started).toLocaleTimeString() + ' · Live connection events' : 'Live connection events for this profile.';
}
async function refresh() { state = await api('state'); online = true; render(); }
async function save() {
  if (!form.reportValidity()) return false;
  await api('save', collect()); dirty = false; await refresh(); return true;
}
async function action(fn) {
  if (busy) return; busy = true; render();
  try { await fn(); } catch (err) { notice(err.message, true); }
  finally { busy = false; render(); }
}
form.addEventListener('input', () => { dirty = true; render(); });
form.addEventListener('submit', event => { event.preventDefault(); action(async () => { if (await save()) notice('Profile saved.'); }); });
$('new').onclick = async () => { if (await mayLeave()) fresh(); };
document.querySelectorAll('[data-preset]').forEach(button => button.onclick = () => { const f = form.elements.remotes; f.value = f.value.trim() ? f.value.trim() + '\n' + button.dataset.preset : button.dataset.preset; dirty = true; render(); });
$('connect').onclick = () => action(async () => {
  const active = ['connected','connecting','reconnecting'].includes(state.sessions[current]?.status);
  if (active) { await api('disconnect', {id:current}); notice('Disconnect requested.'); }
  else { if (!await save()) return; await api('connect', {id:current}); notice('Connecting. Chisel will retry according to your settings.'); }
  await refresh();
});
$('delete').onclick = async () => { if (await confirmAction('Delete this profile?', 'This removes the saved connection settings from this device.')) action(async () => { await api('delete', {id:current}); await refresh(); if (state.profiles.length) select(state.profiles[0]); else fresh(); }); };
$('quit').onclick = async () => { if (await confirmAction('Quit Chisel GUI?', 'All running tunnels will disconnect. Closing just the browser tab keeps them running.')) action(async () => { await api('quit', {}); clearInterval(poll); online = false; notice('Chisel GUI has stopped. You can close this tab.'); }); };
$('download').onclick = () => {
  const data = new Blob([(state.sessions[current]?.logs || []).join('\n')], {type:'text/plain'}), url = URL.createObjectURL(data);
  const a = document.createElement('a'); a.href = url; a.download = 'chisel-connection.log'; a.click(); setTimeout(() => URL.revokeObjectURL(url),1000);
};
window.addEventListener('beforeunload', event => { if (dirty) { event.preventDefault(); event.returnValue = ''; } });
let polling = false;
const poll = setInterval(async () => { if (polling || busy || document.hidden) return; polling = true; try { await refresh(); } catch { online = false; notice('Cannot reach Chisel GUI. Reopen the application if it has stopped.', true); render(); } finally { polling = false; } }, 1500);
(async () => { try { await refresh(); if (state.profiles.length) select(state.profiles[0]); else fresh(); } catch (err) { online = false; fresh(); notice('Cannot open this session. Launch Chisel GUI and use the browser window it opens. ' + err.message, true); } })();
