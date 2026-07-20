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
function rows(el, items, render) { el.replaceChildren(); if (!items?.length) { el.innerHTML = '<div class="empty">No data yet.</div>'; return; } items.forEach(x => el.append(render(x))); }
function row(left, right, title = '') { const el = document.createElement('div'); el.className = 'row'; el.title = title; el.innerHTML = `<span>${left}</span><small>${right}</small>`; return el; }
function card(label, value) { const el = document.createElement('div'); el.className = 'card'; el.innerHTML = `<span>${label}</span><b>${value}</b>`; return el; }
function fmtRate(n) { return `${Math.round((n || 0) * 100)}%`; }
function fmtDate(v) { return v ? new Date(v).toLocaleString() : ''; }
async function loadProjects() {
  const data = await api('/api/projects'); const box = $('projects'); box.replaceChildren();
  data.projects.forEach(name => { const b = document.createElement('button'); b.className = `project ${name === state.project ? 'active':''}`; b.textContent = name; b.onclick = () => selectProject(name); box.append(b); });
  if (!state.project && data.projects.length) await selectProject(data.projects[0]);
}
async function selectProject(name) { state.project = name; await loadProjects(); const d = await api(`/api/projects/${encodeURIComponent(name)}/overview`); render(d); }
function render(d) {
  $('project-name').textContent = d.name; $('delete-project').disabled = d.name === 'default';
  const cache = d.cache || {}; const cards = $('cards'); cards.replaceChildren();
  [['Corpus words',d.corpus_words||0],['Hot / cold words',`${cache.hot_words||0} / ${cache.cold_words||0}`],['Hot cache entries',cache.hot_entries||0],['Cache hit rate',fmtRate(cache.hit_rate)]].forEach(x => cards.append(card(...x)));
  rows($('sources'), d.source_files, s => row(`${s.name} · ${s.mode} · ${s.column}`, `${s.values_read} values · ${s.version}`, fmtDate(s.imported_at)));
  rows($('hot'), d.hot_prefixes, h => row(`└ ${h.prefix}`, `${h.words} words · ${h.hits} hits`, `LRU rank ${h.rank}`));
  rows($('cold'), d.cold_prefixes, c => row(`└ ${c.prefix}`, `${c.hits} recent hits`, fmtDate(c.last_seen)));
}
async function refresh() { try { message(); await loadProjects(); if (state.project) await selectProject(state.project); } catch (e) { message(e.message); } }
$('refresh').onclick = refresh; $('token').onchange = refresh;
$('new-project').onclick = async () => { const name = prompt('Project name (lowercase letters, numbers, hyphens):'); if (!name) return; try { await api('/api/projects',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name})}); await selectProject(name); } catch(e) { message(e.message); } };
$('delete-project').onclick = async () => { if (!state.project || !confirm(`Delete ${state.project} and all its data?`)) return; try { await api(`/api/projects/${encodeURIComponent(state.project)}`,{method:'DELETE'}); state.project = null; await refresh(); } catch(e) { message(e.message); } };
$('reload').onclick = async () => { if (!state.project) return; try { await api(`/api/projects/${encodeURIComponent(state.project)}/reload`,{method:'POST'}); await selectProject(state.project); } catch(e) { message(e.message); } };
$('upload-form').onsubmit = async (e) => { e.preventDefault(); if (!state.project) return; try { const data = new FormData(e.target); await api(`/api/projects/${encodeURIComponent(state.project)}/upload`,{method:'POST',body:data}); e.target.reset(); await selectProject(state.project); } catch(err) { message(err.message); } };
refresh();
