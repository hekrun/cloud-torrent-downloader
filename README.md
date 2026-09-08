# Cloud Torrent

A modern, local-first torrent dashboard inspired by the engine model of `jpillora/cloud-torrent`. It uses Go and `github.com/anacrolix/torrent` for the download engine, with a small provider layer for Pirate Bay's API and Nyaa search pages.

## Run

```bash
go mod tidy
go run .
```

Open http://localhost:8080. Downloads are written to `./downloads` by default. Set `DOWNLOAD_DIR` to choose another directory.

Use the `Settings` link in the top bar or open http://localhost:8080/settings.html to configure the server download path, peer uploads, and completed-torrent seeding. These engine settings are applied after restarting the server.

## Included

- Magnet ingestion and automatic torrent start
- `.torrent` file upload with automatic start
- Live library refresh with progress, peers, download/upload speed, and removal
- Start/stop controls for active torrents
- Persistent torrent library restored after server restart
- Per-torrent file list with completed-file browser downloads
- Removing a torrent also removes its server-side files and partial data
- Search adapters for Pirate Bay, Nyaa, Internet Archive public-domain items, and LibriVox audiobooks
- Provider result links, sizes, and source-page redirects
- Responsive, local-first web interface with no frontend build step
- Request timeouts and bounded provider response bodies

Search providers are external services and may be unavailable or change their markup. Use the application only for content you are authorized to download and follow local law and provider terms.

The app stores the torrent registry in `torrents.json`, uploaded metadata in `.torrent-metadata/`, and engine databases in `.cloud-torrent-data/`. The configured download directory is reserved for downloaded content. Torrents added before persistence was introduced must be added once again to create a registry entry.