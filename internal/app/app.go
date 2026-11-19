package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"csmock/internal/admin"
	"csmock/internal/live"
	"csmock/internal/mocksrv"
	"csmock/internal/model"
	"csmock/internal/store"
)

// Config 是进程启动参数。MockSet / AdminSet 为真时，命令行优先于数据库里保存的地址。
type Config struct {
	MockAddr  string
	AdminAddr string
	DataDir   string
	RulesFile string
	Watch     bool
	MockSet   bool
	AdminSet  bool
}

// Run 启动 Mock 服务和 Web 配置页，直到进程收到中断信号。
func Run(cfg Config) error {
	dataDir, err := resolveDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := adoptOldDatabase(dataDir); err != nil {
		return err
	}
	dbPath := filepath.Join(dataDir, "MockServerGo.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return err
	}
	if err := st.Seed(); err != nil {
		return err
	}
	if !cfg.MockSet {
		if v, ok, err := st.GetSetting("mock_addr"); err != nil {
			return err
		} else if ok && v != "" {
			cfg.MockAddr = v
		}
	}
	if !cfg.AdminSet {
		if v, ok, err := st.GetSetting("admin_addr"); err != nil {
			return err
		} else if ok && v != "" {
			cfg.AdminAddr = v
		}
	}
	if err := checkAddr("Mock 监听地址", cfg.MockAddr); err != nil {
		return err
	}
	if err := checkAddr("管理页监听地址", cfg.AdminAddr); err != nil {
		return err
	}

	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	if cfg.RulesFile != "" {
		if err := importRules(st, cfg.RulesFile); err != nil {
			return err
		}
		if cfg.Watch {
			go watchRules(watchCtx, st, cfg.RulesFile)
		}
	}

	info := admin.Info{
		Version:   model.Version,
		MockAddr:  cfg.MockAddr,
		AdminAddr: cfg.AdminAddr,
		DataDir:   dataDir,
		DBPath:    dbPath,
		RulesFile: cfg.RulesFile,
	}
	hub := live.New()
	mockServer := &http.Server{
		Addr:              cfg.MockAddr,
		Handler:           mocksrv.New(st, hub),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      370 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	adminServer := &http.Server{
		Addr:              cfg.AdminAddr,
		Handler:           admin.New(st, info, hub),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 实时监控是长连接，不能套用普通接口的写超时。
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 2)
	go serve(errCh, "Mock 服务", mockServer)
	go serve(errCh, "配置页面", adminServer)

	log.Printf("MockServerGo %s", model.Version)
	log.Printf("Mock 服务    http://%s    （绑定 %s）", browseHost(cfg.MockAddr), cfg.MockAddr)
	log.Printf("配置页面     http://%s", browseHost(cfg.AdminAddr))
	log.Printf("数据库       %s", dbPath)
	if host, _, err := net.SplitHostPort(cfg.AdminAddr); err == nil && !isLoopback(host) {
		log.Printf("警告: 配置页面绑定在 %s，同一网络内的其他人可以修改规则", host)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		shutdown(mockServer, adminServer)
		if err != nil {
			return err
		}
		return nil
	case <-ctx.Done():
		log.Printf("正在关闭")
		shutdown(mockServer, adminServer)
		return nil
	}
}

func serve(errCh chan<- error, name string, srv *http.Server) {
	err := srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("%s启动失败: %w", name, err)
		return
	}
	errCh <- nil
}

func shutdown(servers ...*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(ctx)
	}
}

// adoptOldDatabase 把旧文件名 csmock.db 改成 MockServerGo.db，已有规则不会丢。
func adoptOldDatabase(dataDir string) error {
	next := filepath.Join(dataDir, "MockServerGo.db")
	if _, err := os.Stat(next); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	prev := filepath.Join(dataDir, "csmock.db")
	if _, err := os.Stat(prev); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := prev + suffix
		to := next + suffix
		if _, err := os.Stat(from); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("把 %s 改名为 %s 失败: %w", filepath.Base(from), filepath.Base(to), err)
		}
	}
	return nil
}

func resolveDataDir(flag string) (string, error) {
	if strings.TrimSpace(flag) != "" {
		return filepath.Abs(flag)
	}
	exe, err := os.Executable()
	if err != nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return "", cwdErr
		}
		return filepath.Join(cwd, "data"), nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	if strings.Contains(strings.ToLower(dir), "go-build") {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, "data"), nil
	}
	return filepath.Join(dir, "data"), nil
}

func checkAddr(label, addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("%s无效: %s，示例 0.0.0.0:8080", label, addr)
	}
	return nil
}

func browseHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return "127.0.0.1:" + port
	}
	return addr
}

func isLoopback(host string) bool {
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func importRules(st *store.Store, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取规则文件失败: %w", err)
	}
	doc, err := model.DecodeDocument(b)
	if err != nil {
		return err
	}
	result, err := st.Import(doc, doc.Replace)
	if err != nil {
		return fmt.Errorf("导入规则失败: %w", err)
	}
	log.Printf("已导入 %s，新增 %d 条，更新 %d 条", path, result.Created, result.Updated)
	return nil
}

func watchRules(ctx context.Context, st *store.Store, path string) {
	var last time.Time
	if fi, err := os.Stat(path); err == nil {
		last = fi.ModTime()
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fi, err := os.Stat(path)
			if err != nil || !fi.ModTime().After(last) {
				continue
			}
			last = fi.ModTime()
			if err := importRules(st, path); err != nil {
				log.Printf("重新加载规则失败: %v", err)
				continue
			}
			log.Printf("规则文件已热更新")
		}
	}
}
