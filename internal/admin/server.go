package admin

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"csmock/internal/live"
	"csmock/internal/store"
)

//go:embed web/index.html web/app.css web/app.js
var webFS embed.FS

// Info 是控制台需要展示的运行信息。
type Info struct {
	Version   string
	MockAddr  string
	AdminAddr string
	DataDir   string
	DBPath    string
	RulesFile string
}

// Server 提供 Web 配置页和管理 API。
type Server struct {
	store *store.Store
	info  Info
	hub   *live.Hub
}

func New(st *store.Store, info Info, hub *live.Hub) http.Handler {
	return &Server{store: st, info: info, hub: hub}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
		s.handleAPI(w, r)
		return
	}
	s.static(w, r)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("管理接口异常: %v", rec)
			writeErr(w, http.StatusInternalServerError, "内部错误")
		}
	}()

	path := r.URL.Path
	switch {
	case path == "/api/health" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case path == "/api/meta" && r.Method == http.MethodGet:
		s.meta(w, r)
	case path == "/api/interfaces" && r.Method == http.MethodGet:
		s.listInterfaces(w, r)
	case path == "/api/interfaces" && r.Method == http.MethodPost:
		s.createInterface(w, r)
	case path == "/api/rules" && r.Method == http.MethodGet:
		s.listRules(w, r)
	case path == "/api/rules" && r.Method == http.MethodPost:
		s.createRule(w, r)
	case path == "/api/try" && r.Method == http.MethodPost:
		s.tryMatch(w, r)
	case path == "/api/live" && r.Method == http.MethodGet:
		s.live(w, r)
	case path == "/api/logs" && r.Method == http.MethodGet:
		s.listLogs(w, r)
	case path == "/api/logs" && r.Method == http.MethodDelete:
		s.clearLogs(w, r)
	case path == "/api/state" && r.Method == http.MethodGet:
		s.listState(w, r)
	case path == "/api/state" && r.Method == http.MethodPut:
		s.putState(w, r)
	case path == "/api/state" && r.Method == http.MethodDelete:
		s.clearState(w, r)
	case path == "/api/export" && r.Method == http.MethodGet:
		s.exportRules(w, r)
	case path == "/api/import" && r.Method == http.MethodPost:
		s.importRules(w, r)
	case path == "/api/settings" && r.Method == http.MethodGet:
		s.getSettings(w, r)
	case path == "/api/settings" && r.Method == http.MethodPut:
		s.putSettings(w, r)
	case strings.HasPrefix(path, "/api/interfaces/"):
		s.interfaceByID(w, r, strings.TrimPrefix(path, "/api/interfaces/"))
	case strings.HasPrefix(path, "/api/rules/"):
		s.ruleByID(w, r, strings.TrimPrefix(path, "/api/rules/"))
	case strings.HasPrefix(path, "/api/logs/"):
		s.logByID(w, r, strings.TrimPrefix(path, "/api/logs/"))
	case strings.HasPrefix(path, "/api/state/"):
		s.stateByKey(w, r, strings.TrimPrefix(path, "/api/state/"))
	default:
		writeErr(w, http.StatusNotFound, "接口不存在")
	}
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeErr(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	if r.URL.Path == "/favicon.ico" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	name := "web/index.html"
	contentType := "text/html; charset=utf-8"
	switch r.URL.Path {
	case "/app.css":
		name = "web/app.css"
		contentType = "text/css; charset=utf-8"
	case "/app.js":
		name = "web/app.js"
		contentType = "text/javascript; charset=utf-8"
	}
	b, err := fs.ReadFile(webFS, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "页面不存在")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readBody(r *http.Request, limit int64) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, limit+1))
}

func parseID(s string) (int64, string, bool) {
	idPart, extra, _ := strings.Cut(s, "/")
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return 0, extra, false
	}
	return id, extra, true
}
