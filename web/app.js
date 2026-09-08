const $ = (selector) => document.querySelector(selector);
const state = { torrents: [], filter: 'all' };

function formatBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const power = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** power).toFixed(power ? 1 : 0)} ${units[power]}`;
}

function formatSearchSize(value) {
  const numeric = Number(value);
  return Number.isFinite(numeric) && numeric > 0 ? formatBytes(numeric) : (value || 'Size unknown');
}

function loadFooterStorage() {
  fetch('/api/storage').then((response) => response.json()).then((storage) => {
    $('#footer-disk').textContent = storage.free ? `${formatBytes(storage.free)} free` : 'Storage unavailable';
  }).catch(() => { $('#footer-disk').textContent = 'Storage unavailable'; });
}

function renderTorrents() {
  const list = $('#torrent-list');
  const visible = state.torrents.filter((torrent) => state.filter === 'all' || (state.filter === 'complete' ? torrent.progress >= 100 : torrent.progress < 100));
  $('#library-count').textContent = state.torrents.length;
  if (!visible.length) {
    list.innerHTML = `<div class="empty-state"><div class="empty-icon">+</div><h3>${state.torrents.length ? 'Nothing in this view' : 'Your shelf is clear'}</h3><p>${state.torrents.length ? 'Try another library view.' : 'Add a magnet link above and your next download will appear here.'}</p></div>`;
    return;
  }
  list.innerHTML = visible.map((torrent) => `<article class="torrent-row">
    <div class="file-badge">${torrent.progress >= 100 ? 'OK' : 'DL'}</div>
    <div class="torrent-main"><div class="torrent-title">${escapeHTML(torrent.name)}</div><div class="torrent-meta">${formatBytes(torrent.downloaded)} of ${formatBytes(torrent.size)} <span class="meta-separator">/</span> ${torrent.peers} peers <span class="meta-separator">/</span> ↓ ${formatBytes(torrent.downloadSpeed || 0)}/s <span class="meta-separator">/</span> ↑ ${formatBytes(torrent.uploadSpeed || 0)}/s</div><div class="progress-track"><span style="width:${Math.min(torrent.progress, 100)}%"></span></div><details class="file-details"><summary>View files (${(torrent.files || []).length})</summary><div class="file-list">${(torrent.files || []).map((file) => `<div class="file-item"><span>${escapeHTML(file.path)}</span><small>${formatBytes(file.size)}</small>${file.complete ? `<a class="download-button" href="/download/${torrent.hash}/${encodeURIComponent(file.path)}">Download</a>` : '<small class="file-pending">Downloading</small>'}</div>`).join('')}</div></details></div>
    <div class="torrent-stat"><strong>${Math.round(torrent.progress)}%</strong><small>${torrent.status}</small></div>
    <div class="torrent-controls">${torrent.progress < 100 ? `<button class="control-button" data-action="${torrent.status === 'Paused' ? 'start' : 'stop'}" data-hash="${torrent.hash}">${torrent.status === 'Paused' ? 'Resume' : 'Pause'}</button>` : `<span class="seed-state">${torrent.status === 'Seeding' ? 'Seeding' : 'Complete'}</span>`}<button class="row-action" data-delete="${torrent.hash}" title="Remove torrent and files" aria-label="Remove torrent and files">×</button></div>
  </article>`).join('');
}

function renderResults(results) {
  const target = $('#search-results');
  if (!results.length) { target.innerHTML = '<p class="search-empty">No results from the selected providers.</p>'; return; }
  target.innerHTML = results.map((result) => `<article class="result-row"><div class="result-source">${escapeHTML(result.source || 'Index')}</div><div class="result-title"><a href="${escapeAttribute(result.link || '#')}" target="_blank" rel="noreferrer">${escapeHTML(result.title)}</a></div><div class="result-meta">${escapeHTML(formatSearchSize(result.size))} ${result.seeds ? `<span class="seed-count">${result.seeds} seeds</span>` : ''}</div><a class="provider-link" href="${escapeAttribute(result.link || '#')}" target="_blank" rel="noreferrer" ${result.link ? '' : 'aria-disabled="true"'}>Open</a><button class="add-result" data-magnet="${escapeAttribute(result.magnet || '')}" ${result.magnet ? '' : 'disabled'}>Add</button></article>`).join('');
}

function escapeHTML(value) { return String(value || '').replace(/[&<>'"]/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[char])); }
function escapeAttribute(value) { return escapeHTML(value); }

async function loadTorrents() {
  const response = await fetch('/api/torrents');
  if (!response.ok) throw new Error('Could not load library');
  state.torrents = await response.json();
  renderTorrents();
  $('#last-updated').textContent = `Updated ${new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
}

async function addMagnet(magnet) {
  const response = await fetch('/api/torrent', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ magnet }) });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || 'Could not add torrent');
  $('#magnet').value = '';
  $('#add-message').textContent = 'Torrent added to your library.';
  await loadTorrents();
}

$('#add').addEventListener('click', async () => {
  const magnet = $('#magnet').value.trim();
  if (!magnet) return;
  $('#add-message').textContent = 'Adding torrent...';
  try { await addMagnet(magnet); } catch (error) { $('#add-message').textContent = error.message; }
});
$('#magnet').addEventListener('keydown', (event) => { if (event.key === 'Enter') $('#add').click(); });
$('#torrent-file').addEventListener('change', async (event) => {
  const file = event.target.files[0];
  if (!file) return;
  const form = new FormData();
  form.append('file', file);
  $('#upload-message').textContent = 'Adding .torrent file...';
  try {
    const response = await fetch('/api/torrent-file', { method: 'POST', body: form });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || 'Could not add torrent file');
    $('#upload-message').textContent = 'Torrent file added.';
    await loadTorrents();
  } catch (error) { $('#upload-message').textContent = error.message; }
  event.target.value = '';
});
$('#refresh').addEventListener('click', () => loadTorrents().catch(() => {}));
document.querySelectorAll('.tab').forEach((tab) => tab.addEventListener('click', () => { document.querySelector('.tab.active').classList.remove('active'); tab.classList.add('active'); state.filter = tab.dataset.filter; renderTorrents(); }));
$('#torrent-list').addEventListener('click', async (event) => {
  const action = event.target.dataset.action;
  const hash = event.target.dataset.hash;
  if (action && hash) {
    event.target.disabled = true;
    await fetch(`/api/torrent/${hash}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ action }) });
    await loadTorrents();
    return;
  }
  const deleteHash = event.target.dataset.delete;
  if (!deleteHash || !confirm('Remove this torrent and delete its server files?')) return;
  await fetch(`/api/torrent/${deleteHash}`, { method: 'DELETE' });
  await loadTorrents();
});
$('#search-form').addEventListener('submit', async (event) => { event.preventDefault(); const query = $('#query').value.trim(); if (!query) return; $('#search-results').innerHTML = '<p class="search-empty">Searching providers...</p>'; try { const response = await fetch(`/api/search?q=${encodeURIComponent(query)}&provider=${$('#provider').value}`); const data = await response.json(); if (!response.ok) throw new Error(data.error); renderResults(data); } catch (error) { $('#search-results').innerHTML = `<p class="search-empty">${escapeHTML(error.message)}</p>`; } });
$('#search-results').addEventListener('click', async (event) => { const magnet = event.target.dataset.magnet; if (!magnet) return; event.target.textContent = 'Adding...'; try { await addMagnet(magnet); event.target.textContent = 'Added'; } catch (error) { event.target.textContent = 'Retry'; } });
loadTorrents().catch(() => { $('#last-updated').textContent = 'Engine unavailable'; });
loadFooterStorage();
setInterval(() => loadTorrents().catch(() => {}), 4000);
