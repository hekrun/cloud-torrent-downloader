const field = (selector) => document.querySelector(selector);

async function loadSettings() {
  const response = await fetch('/api/settings');
  if (!response.ok) throw new Error('Could not load settings');
  const data = await response.json();
  const settings = data.settings || data;
  const storage = data.storage || {};
  field('#download-path').value = settings.downloadPath || '';
  field('#upload-enabled').checked = Boolean(settings.upload);
  field('#seeding-enabled').checked = Boolean(settings.seeding);
  field('#disk-total').textContent = formatBytes(storage.total);
  field('#disk-used').textContent = formatBytes(storage.used);
  field('#disk-free').textContent = formatBytes(storage.free);
  const percent = storage.total ? Math.min(100, storage.used / storage.total * 100) : 0;
  field('#disk-bar').style.width = `${percent}%`;
}

function formatBytes(value) {
  if (!value) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const power = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** power).toFixed(power ? 1 : 0)} ${units[power]}`;
}

field('#settings-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const message = field('#settings-message');
  message.textContent = 'Saving settings...';
  try {
    const response = await fetch('/api/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        downloadPath: field('#download-path').value.trim(),
        upload: field('#upload-enabled').checked,
        seeding: field('#seeding-enabled').checked,
      }),
    });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || 'Could not save settings');
    message.textContent = data.restartRequired ? 'Saved. Restart the server to apply these engine settings.' : 'Settings saved.';
  } catch (error) {
    message.textContent = error.message;
  }
});

loadSettings().catch((error) => { field('#settings-message').textContent = error.message; });
