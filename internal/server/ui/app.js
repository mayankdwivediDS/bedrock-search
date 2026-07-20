const state = { project: null };
const $ = (id) => document.getElementById(id);
const message = (text = '') => $('message').textContent = text;
const token = () => $('token').value.trim();
async function api(path, options = {}) {
  const headers = options.headers || {};
  if (token()) headers['X-API-Key'] = token();
  const res = await fetch(path, {...options, headers});
  if (res.status === 204) return null;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `Request failed (${res.status})`);
  return body;
}
function text(el, value) { el.textContent = value; return el; }
function rows(el, items, render) {
  el.replaceChildren();
  if (!items?.length) {
    const empty = text(document.createElement('div'), 'No data yet.');
    empty.classList.add('empty');
    el.append(empty);
    return;
  }
  items.forEach(x => el.append(render(x)));
}
function row(left, right, title = '') { const el = document.createElement('div'); el.className = 'row'; el.title = title; el.append(text(document.createElement('span'), left), text(document.createElement('small'), right)); return el; }
function card(label, value) { const el = document.createElement('div'); el.className = 'card'; el.append(text(document.createElement('span'), label), text(document.createElement('b'), value)); return el; }
function fmtRate(n) { return `${Math.round((n || 0) * 100)}%`; }
function fmtDate(v) { return v ? new Date(v).toLocaleString() : ''; }
async function loadProjects() {
  const data = await api('/api/projects'); const box = $('projects'); box.replaceChildren();
  data.projects.forEach(name => { const b = document.createElement('button'); b.className = `project ${name === state.project ? 'active':''}`; b.textContent = name; b.title = `Open ${name} project`; b.onclick = () => selectProject(name); box.append(b); });
  if (!state.project && data.projects.length) state.project = data.projects[0];
}
async function selectProject(name) { state.project = name; await loadProjects(); const d = await api(`/api/projects/${encodeURIComponent(name)}/overview`); render(d); }
function render(d) {
  $('project-name').textContent = d.name; $('project-subtitle').textContent = `Corpus ${d.version || 'not ready'} • local node • independently managed`; $('delete-project').disabled = d.name === 'default';
  const cache = d.cache || {}; const cards = $('cards'); cards.replaceChildren();
  [['Corpus words', d.corpus_words || 0], ['Hot / cold words', `${cache.hot_words || 0} / ${cache.cold_words || 0}`], ['Hot cache entries', cache.hot_entries || 0], ['Cache hit rate', fmtRate(cache.hit_rate)]].forEach(x => cards.append(card(...x)));
  rows($('sources'), d.source_files, s => row(`${s.name} · ${s.mode} · ${s.column}`, `${s.values_read} values · ${s.version}`, fmtDate(s.imported_at)));
  rows($('hot'), d.hot_prefixes, h => row(`└ ${h.prefix}`, `${h.words} words · ${h.hits} hits`, `LRU rank ${h.rank}; loaded ${fmtDate(h.loaded_at)}`));
  rows($('cold'), d.cold_prefixes, c => row(`└ ${c.prefix}`, `${c.hits} recent hits`, `Last seen ${fmtDate(c.last_seen)}`));
}
async function refresh() { try { message(); await loadProjects(); if (state.project) await selectProject(state.project); } catch (e) { message(e.message); } }
$('refresh').onclick = refresh; $('token').onchange = refresh;
$('csv-file').onchange = (e) => { $('file-name').textContent = e.target.files[0]?.name || 'No file selected'; };
$('api-docs').onclick = () => { $('docs-panel').classList.add('open'); $('docs-panel').setAttribute('aria-hidden', 'false'); };
$('close-docs').onclick = () => { $('docs-panel').classList.remove('open'); $('docs-panel').setAttribute('aria-hidden', 'true'); };
$('docs-panel').onclick = (e) => { if (e.target === $('docs-panel')) $('close-docs').click(); };
document.onkeydown = (e) => { if (e.key === 'Escape') $('close-docs').click(); };
$('new-project').onclick = async () => { const name = prompt('Project name (lowercase letters, numbers, hyphens):'); if (!name) return; try { await api('/api/projects', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name})}); await selectProject(name); } catch(e) { message(e.message); } };
$('delete-project').onclick = async () => { if (!state.project || !confirm(`Delete ${state.project} and all its data?`)) return; try { await api(`/api/projects/${encodeURIComponent(state.project)}`, {method:'DELETE'}); state.project = null; await refresh(); } catch(e) { message(e.message); } };
$('reload').onclick = async () => { if (!state.project) return; try { await api(`/api/projects/${encodeURIComponent(state.project)}/reload`, {method:'POST'}); await selectProject(state.project); } catch(e) { message(e.message); } };
$('upload-form').onsubmit = async (e) => { e.preventDefault(); if (!state.project) return; try { const data = new FormData(e.target); await api(`/api/projects/${encodeURIComponent(state.project)}/upload`, {method:'POST', body:data}); e.target.reset(); $('file-name').textContent = 'No file selected'; await selectProject(state.project); } catch(err) { message(err.message); } };
refresh();
