package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

type app struct {
	client  *torrent.Client
	root    string
	config  settings
	mu      sync.RWMutex
	paused  map[string]bool
	samples map[string]speedSample
	records map[string]torrentRecord
}

type settings struct {
	DownloadPath string `json:"downloadPath"`
	Seeding      bool   `json:"seeding"`
	Upload       bool   `json:"upload"`
}

type speedSample struct {
	at         time.Time
	downloaded int64
	uploaded   int64
}

type torrentRecord struct {
	Hash     string `json:"hash"`
	Magnet   string `json:"magnet,omitempty"`
	MetaFile string `json:"metaFile,omitempty"`
	Paused   bool   `json:"paused,omitempty"`
}

type torrentView struct {
	Hash          string     `json:"hash"`
	Name          string     `json:"name"`
	Size          int64      `json:"size"`
	Downloaded    int64      `json:"downloaded"`
	Progress      float64    `json:"progress"`
	Rate          int64      `json:"rate"`
	Peers         int        `json:"peers"`
	Status        string     `json:"status"`
	AddedAt       string     `json:"addedAt"`
	Files         []fileView `json:"files"`
	DownloadSpeed int64      `json:"downloadSpeed"`
	UploadSpeed   int64      `json:"uploadSpeed"`
}

type fileView struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Downloaded int64  `json:"downloaded"`
	Complete   bool   `json:"complete"`
}

type searchResult struct {
	Title  string `json:"title"`
	Size   string `json:"size"`
	Seeds  int    `json:"seeds"`
	Peers  int    `json:"peers"`
	Date   string `json:"date"`
	Magnet string `json:"magnet"`
	Link   string `json:"link"`
	Source string `json:"source"`
}

func main() {
	config := loadSettings()
	root := os.Getenv("DOWNLOAD_DIR")
	if root == "" {
		root = config.DownloadPath
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	engineDir := ".cloud-torrent-data"
	if err := os.MkdirAll(engineDir, 0o755); err != nil {
		log.Fatal(err)
	}
	moveLegacyEngineFiles(root, engineDir)
	cfg := torrent.NewDefaultClientConfig()
	pieceCompletion, err := storage.NewDefaultPieceCompletionForDir(engineDir)
	if err != nil {
		log.Fatal(err)
	}
	fileStorage := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: root, PieceCompletion: pieceCompletion})
	defer fileStorage.Close()
	cfg.DataDir = engineDir
	cfg.DefaultStorage = fileStorage
	cfg.NoUpload = !config.Upload
	cfg.Seed = config.Seeding
	client, err := torrent.NewClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	a := &app{client: client, root: root, config: config, paused: make(map[string]bool), samples: make(map[string]speedSample), records: loadTorrentRecords()}
	a.restoreTorrents()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/torrents", a.handleTorrents)
	mux.HandleFunc("/api/torrent", a.handleTorrent)
	mux.HandleFunc("/api/torrent/", a.handleTorrentRoute)
	mux.HandleFunc("/api/torrent-file", a.handleTorrentFile)
	mux.HandleFunc("/download/", a.handleDownload)
	mux.HandleFunc("/api/search", a.handleSearch)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]bool{"ok": true}) })
	mux.HandleFunc("/api/settings", a.handleSettings)
	mux.HandleFunc("/api/storage", a.handleStorage)
	mux.Handle("/", http.FileServer(http.Dir("./web")))
	server := &http.Server{Addr: ":8080", Handler: logging(mux), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("cloud torrent listening on http://localhost%s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func moveLegacyEngineFiles(downloadPath, enginePath string) {
	for _, name := range []string{".torrent.db", ".torrent.db-shm", ".torrent.db-wal"} {
		source := filepath.Join(downloadPath, name)
		target := filepath.Join(enginePath, name)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := os.Rename(source, target); err != nil {
			log.Printf("could not move engine file %s: %v", source, err)
		}
	}
}

func loadSettings() settings {
	config := settings{DownloadPath: "./downloads", Seeding: false, Upload: false}
	data, err := os.ReadFile("cloud-torrent.json")
	if err == nil {
		_ = json.Unmarshal(data, &config)
	}
	return config
}

func loadTorrentRecords() map[string]torrentRecord {
	records := make(map[string]torrentRecord)
	data, err := os.ReadFile("torrents.json")
	if err == nil {
		_ = json.Unmarshal(data, &records)
	}
	return records
}

func (a *app) saveTorrentRecords() {
	a.mu.RLock()
	data, err := json.MarshalIndent(a.records, "", "  ")
	a.mu.RUnlock()
	if err == nil {
		_ = os.WriteFile("torrents.json", data, 0o644)
	}
}

func (a *app) rememberTorrent(record torrentRecord) {
	a.mu.Lock()
	a.records[record.Hash] = record
	a.mu.Unlock()
	a.saveTorrentRecords()
}

func (a *app) restoreTorrents() {
	a.mu.RLock()
	records := make([]torrentRecord, 0, len(a.records))
	for _, record := range a.records {
		records = append(records, record)
	}
	a.mu.RUnlock()
	for _, record := range records {
		var t *torrent.Torrent
		var err error
		if record.MetaFile != "" {
			t, err = a.client.AddTorrentFromFile(record.MetaFile)
		} else if record.Magnet != "" {
			t, err = a.client.AddMagnet(record.Magnet)
		}
		if err != nil || t == nil {
			log.Printf("could not restore torrent %s: %v", record.Hash, err)
			continue
		}
		if record.Paused {
			a.pause(t)
		} else {
			a.startWhenReady(t)
		}
	}
}

func (a *app) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		config := loadSettings()
		writeJSON(w, map[string]any{"settings": config, "storage": storageInfo(config.DownloadPath)})
		return
	}
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var next settings
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&next); err != nil || strings.TrimSpace(next.DownloadPath) == "" {
		writeError(w, http.StatusBadRequest, "download path is required")
		return
	}
	path, err := filepath.Abs(next.DownloadPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid download path")
		return
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		writeError(w, http.StatusBadRequest, "download path is not writable")
		return
	}
	next.DownloadPath = path
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil || os.WriteFile("cloud-torrent.json", data, 0o644) != nil {
		writeError(w, http.StatusInternalServerError, "could not save settings")
		return
	}
	writeJSON(w, map[string]any{"settings": next, "restartRequired": true})
}

func (a *app) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, storageInfo(a.root))
}

func storageInfo(path string) map[string]uint64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return map[string]uint64{"total": 0, "used": 0, "free": 0}
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - (stat.Bfree * uint64(stat.Bsize))
	return map[string]uint64{"total": total, "used": used, "free": free}
}

func (a *app) handleTorrents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items := make([]torrentView, 0)
	for _, t := range a.client.Torrents() {
		items = append(items, a.view(t))
	}
	writeJSON(w, items)
}

func (a *app) handleTorrent(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Magnet string `json:"magnet"`
			URL    string `json:"url"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		var t *torrent.Torrent
		var err error
		if strings.TrimSpace(body.Magnet) != "" {
			t, err = a.client.AddMagnet(strings.TrimSpace(body.Magnet))
		} else if body.URL != "" {
			err = errors.New("remote torrent URLs are disabled; use a magnet link")
		} else {
			err = errors.New("magnet is required")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.rememberTorrent(torrentRecord{Hash: t.InfoHash().HexString(), Magnet: strings.TrimSpace(body.Magnet)})
		a.startWhenReady(t)
		writeJSON(w, a.view(t))
	case http.MethodDelete:
		hash := strings.TrimPrefix(r.URL.Path, "/api/torrent/")
		t, ok := a.find(hash)
		if !ok {
			writeError(w, http.StatusNotFound, "torrent not found")
			return
		}
		a.removeTorrent(t)
		writeJSON(w, map[string]bool{"ok": true})
	case http.MethodPatch:
		hash := strings.TrimPrefix(r.URL.Path, "/api/torrent/")
		t, ok := a.find(hash)
		if !ok {
			writeError(w, http.StatusNotFound, "torrent not found")
			return
		}
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		if body.Action == "start" {
			a.startWhenReady(t)
		} else if body.Action == "stop" {
			a.pause(t)
		} else {
			writeError(w, http.StatusBadRequest, "action must be start or stop")
			return
		}
		writeJSON(w, a.view(t))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *app) handleTorrentRoute(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/files") {
		hash := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/torrent/"), "/files")
		t, ok := a.find(hash)
		if !ok {
			writeError(w, http.StatusNotFound, "torrent not found")
			return
		}
		writeJSON(w, a.files(t))
		return
	}
	a.handleTorrent(w, r)
}

func (a *app) handleTorrentFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "torrent file is too large or invalid")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "torrent file is required")
		return
	}
	defer file.Close()
	meta, err := metainfo.Load(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid torrent file")
		return
	}
	t, err := a.client.AddTorrent(meta)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash := t.InfoHash().HexString()
	metaDir := ".torrent-metadata"
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save torrent metadata")
		return
	}
	metaFile := filepath.Join(metaDir, hash+".torrent")
	metadata, err := os.Create(metaFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save torrent metadata")
		return
	}
	writeErr := meta.Write(metadata)
	_ = metadata.Close()
	if writeErr != nil {
		writeError(w, http.StatusInternalServerError, "could not save torrent metadata")
		return
	}
	a.rememberTorrent(torrentRecord{Hash: hash, MetaFile: metaFile})
	a.startWhenReady(t)
	writeJSON(w, a.view(t))
}

func (a *app) handleDownload(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/download/"), "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "file path is required")
		return
	}
	t, ok := a.find(parts[0])
	if !ok || t.Info() == nil {
		writeError(w, http.StatusNotFound, "torrent or metadata not found")
		return
	}
	filePath, err := url.PathUnescape(parts[1])
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file path")
		return
	}
	file := a.fileByPath(t, filePath)
	if file == nil || file.BytesCompleted() < file.Length() {
		writeError(w, http.StatusConflict, "file is not complete yet")
		return
	}
	root, err := filepath.Abs(a.root)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid download directory")
		return
	}
	path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(file.Path())))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid file path")
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		writeError(w, http.StatusForbidden, "file path is outside download directory")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(path)+"\"")
	http.ServeFile(w, r, path)
}

func (a *app) startWhenReady(t *torrent.Torrent) {
	a.mu.Lock()
	delete(a.paused, t.InfoHash().HexString())
	if record, ok := a.records[t.InfoHash().HexString()]; ok {
		record.Paused = false
		a.records[t.InfoHash().HexString()] = record
	}
	a.mu.Unlock()
	a.saveTorrentRecords()
	if t.Info() != nil {
		t.DownloadAll()
		return
	}
	go func() {
		select {
		case <-t.GotInfo():
			a.mu.RLock()
			paused := a.paused[t.InfoHash().HexString()]
			a.mu.RUnlock()
			if !paused {
				t.DownloadAll()
			}
		case <-time.After(10 * time.Minute):
			log.Printf("torrent %s did not receive metadata within 10 minutes", t.InfoHash().HexString())
		}
	}()
}

func (a *app) pause(t *torrent.Torrent) {
	a.mu.Lock()
	a.paused[t.InfoHash().HexString()] = true
	if record, ok := a.records[t.InfoHash().HexString()]; ok {
		record.Paused = true
		a.records[t.InfoHash().HexString()] = record
	}
	a.mu.Unlock()
	a.saveTorrentRecords()
	if t.Info() != nil {
		for _, file := range t.Files() {
			file.SetPriority(torrent.PiecePriorityNone)
		}
	}
}

func (a *app) removeTorrent(t *torrent.Torrent) {
	paths := make([]string, 0)
	if t.Info() != nil {
		for _, file := range t.Files() {
			paths = append(paths, file.Path(), file.Path()+".part")
		}
	}
	hash := t.InfoHash().HexString()
	t.Drop()
	a.mu.Lock()
	record := a.records[hash]
	delete(a.paused, hash)
	delete(a.samples, hash)
	delete(a.records, hash)
	a.mu.Unlock()
	a.saveTorrentRecords()
	if record.MetaFile != "" {
		_ = os.Remove(record.MetaFile)
	}
	root, err := filepath.Abs(a.root)
	if err != nil {
		return
	}
	for _, relative := range paths {
		path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = "all"
	}
	if query == "" {
		writeError(w, http.StatusBadRequest, "search query is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	results, err := search(ctx, provider, query)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, results)
}

func (a *app) find(hash string) (*torrent.Torrent, bool) {
	for _, t := range a.client.Torrents() {
		if strings.EqualFold(t.InfoHash().HexString(), hash) {
			return t, true
		}
	}
	return nil, false
}

func (a *app) view(t *torrent.Torrent) torrentView {
	name := "Fetching metadata..."
	var size, downloaded int64
	if t.Info() != nil {
		name = t.Name()
		size = t.Length()
		downloaded = t.BytesCompleted()
	}
	progress := float64(0)
	if size > 0 {
		progress = float64(downloaded) / float64(size) * 100
	}
	status := "Fetching metadata"
	if t.Info() != nil {
		status = "Ready"
		a.mu.RLock()
		isPaused := a.paused[t.InfoHash().HexString()]
		a.mu.RUnlock()
		if progress >= 100 {
			a.mu.RLock()
			seeding := a.config.Seeding && a.config.Upload
			a.mu.RUnlock()
			if seeding {
				status = "Seeding"
			} else {
				status = "Complete"
			}
		} else if isPaused {
			status = "Paused"
		} else if downloaded > 0 {
			status = "Downloading"
		}
	}
	downloadSpeed, uploadSpeed := a.speeds(t)
	return torrentView{Hash: t.InfoHash().HexString(), Name: name, Size: size, Downloaded: downloaded, Progress: progress, Peers: t.Stats().ActivePeers, Status: status, AddedAt: time.Now().Format(time.RFC3339), Files: a.files(t), DownloadSpeed: downloadSpeed, UploadSpeed: uploadSpeed}
}

func (a *app) speeds(t *torrent.Torrent) (int64, int64) {
	now := time.Now()
	stats := t.Stats()
	current := speedSample{at: now, downloaded: stats.BytesReadUsefulData.Int64(), uploaded: stats.BytesWrittenData.Int64()}
	a.mu.Lock()
	previous, ok := a.samples[t.InfoHash().HexString()]
	a.samples[t.InfoHash().HexString()] = current
	a.mu.Unlock()
	if !ok || current.at.Sub(previous.at) <= 0 {
		return 0, 0
	}
	seconds := current.at.Sub(previous.at).Seconds()
	return maxRate(current.downloaded-previous.downloaded, seconds), maxRate(current.uploaded-previous.uploaded, seconds)
}

func maxRate(bytes int64, seconds float64) int64 {
	if bytes <= 0 || seconds <= 0 {
		return 0
	}
	return int64(float64(bytes) / seconds)
}

func (a *app) files(t *torrent.Torrent) []fileView {
	if t.Info() == nil {
		return []fileView{}
	}
	items := make([]fileView, 0, len(t.Files()))
	for _, file := range t.Files() {
		items = append(items, fileView{Path: file.Path(), Size: file.Length(), Downloaded: file.BytesCompleted(), Complete: file.BytesCompleted() >= file.Length()})
	}
	return items
}

func (a *app) fileByPath(t *torrent.Torrent, path string) *torrent.File {
	for _, file := range t.Files() {
		if file.Path() == path {
			return file
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"error": message})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
