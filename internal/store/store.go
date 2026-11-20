package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"csmock/internal/model"

	_ "modernc.org/sqlite"
)

var keepLogs = 2000

// Store 是本地 SQLite 存储。
type Store struct {
	db   *sql.DB
	path string
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Open 打开或创建数据库文件，并启用 WAL。
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	var journal string
	if err := db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&journal); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("设置 WAL 失败: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("设置 busy_timeout 失败: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: abs}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Path() string { return s.path }

// Migrate 创建表结构。
func (s *Store) Migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS interfaces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL DEFAULT 0,
			method TEXT NOT NULL DEFAULT '*',
			path TEXT NOT NULL,
			query_match TEXT NOT NULL DEFAULT '[]',
			header_match TEXT NOT NULL DEFAULT '[]',
			body_match TEXT NOT NULL DEFAULT '[]',
			state_match TEXT NOT NULL DEFAULT '[]',
			status_code INTEGER NOT NULL DEFAULT 200,
			response_headers TEXT NOT NULL DEFAULT '{}',
			response_body TEXT NOT NULL DEFAULT '',
			delay_ms INTEGER NOT NULL DEFAULT 0,
			mode TEXT NOT NULL DEFAULT 'mock',
			proxy_url TEXT NOT NULL DEFAULT '',
			record INTEGER NOT NULL DEFAULT 0,
			state_set TEXT NOT NULL DEFAULT '{}',
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id INTEGER,
			rule_name TEXT NOT NULL DEFAULT '',
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			query TEXT NOT NULL DEFAULT '',
			headers TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			status_code INTEGER NOT NULL DEFAULT 0,
			matched INTEGER NOT NULL DEFAULT 0,
			remote_addr TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			response_headers TEXT NOT NULL DEFAULT '',
			response_body TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_id ON request_logs(id DESC)`,
		`CREATE TABLE IF NOT EXISTS states (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("初始化数据库失败: %w", err)
		}
	}
	if err := s.ensureColumn("rules", "interface_id", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("rules", "flow", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return s.backfillInterfaces()
}

func (s *Store) CountRules() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM rules`).Scan(&n)
	return n, err
}

// Seed 在空库时写入一组接口，以及挂在接口下的规则。
func (s *Store) Seed() error {
	n, err := s.CountRules()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	type sample struct {
		iface model.Interface
		rules []model.Rule
	}
	jsonHeader := model.StringMap{"Content-Type": "application/json; charset=utf-8"}
	samples := []sample{
		{
			iface: model.Interface{Name: "健康检查", Enabled: true, Method: "GET", Path: "/hello", Description: "启动后可直接访问"},
			rules: []model.Rule{{
				Name: "默认", Enabled: true, StatusCode: 200, ResponseHeaders: jsonHeader,
				ResponseBody: "{\n  \"message\": \"hello from csmock\",\n  \"time\": \"{{now}}\"\n}",
			}},
		},
		{
			iface: model.Interface{Name: "查询用户", Enabled: true, Method: "GET", Path: "/user/:id", Description: "路径参数示例"},
			rules: []model.Rule{{
				Name: "默认", Enabled: true, StatusCode: 200, ResponseHeaders: jsonHeader,
				ResponseBody: "{\n  \"id\": \"{{params.id}}\",\n  \"verbose\": \"{{query.verbose}}\"\n}",
			}},
		},
		{
			iface: model.Interface{Name: "登录", Enabled: true, Method: "POST", Path: "/login"},
			rules: []model.Rule{
				{
					Name: "登录成功", Enabled: true, Priority: 10,
					BodyMatch:  []model.Condition{{Key: "username", Op: "eq", Value: "demo"}},
					StatusCode: 200, ResponseHeaders: jsonHeader,
					ResponseBody: "{\n  \"token\": \"{{uuid}}\",\n  \"user\": \"{{body.username}}\"\n}",
					StateSet:     model.StringMap{"auth": "logged_in", "user": "{{body.username}}"},
					Description:  "请求体 username=demo 时登录成功",
				},
				{
					Name: "登录失败", Enabled: true, Priority: 0, StatusCode: 401, ResponseHeaders: jsonHeader,
					ResponseBody: "{\n  \"error\": \"invalid credentials\"\n}",
				},
			},
		},
		{
			iface: model.Interface{Name: "当前用户", Enabled: true, Method: "GET", Path: "/me"},
			rules: []model.Rule{
				{
					Name: "已登录", Enabled: true, Priority: 10,
					StateMatch: []model.Condition{{Key: "auth", Op: "eq", Value: "logged_in"}},
					StatusCode: 200, ResponseHeaders: jsonHeader,
					ResponseBody: "{\n  \"user\": \"{{state.user}}\",\n  \"auth\": \"{{state.auth}}\"\n}",
				},
				{
					Name: "未登录", Enabled: true, Priority: 0, StatusCode: 401, ResponseHeaders: jsonHeader,
					ResponseBody: "{\n  \"error\": \"unauthorized\"\n}",
				},
			},
		},
	}
	for i := range samples {
		if err := samples[i].iface.Normalize(); err != nil {
			return err
		}
		if err := s.CreateInterface(&samples[i].iface); err != nil {
			return err
		}
		for j := range samples[i].rules {
			rule := samples[i].rules[j]
			rule.InterfaceID = samples[i].iface.ID
			rule.Method = samples[i].iface.Method
			rule.Path = samples[i].iface.Path
			if err := rule.Normalize(); err != nil {
				return err
			}
			if err := s.CreateRule(&rule); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ListRules() ([]model.Rule, error) {
	rows, err := s.db.Query(`SELECT ` + ruleColumns + ` FROM rules ORDER BY priority DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	if out == nil {
		out = []model.Rule{}
	}
	return out, rows.Err()
}

func (s *Store) GetRule(id int64) (model.Rule, error) {
	row := s.db.QueryRow(`SELECT `+ruleColumns+` FROM rules WHERE id = ?`, id)
	rule, err := scanRule(row)
	if err == sql.ErrNoRows {
		return model.Rule{}, fmt.Errorf("规则不存在")
	}
	return rule, err
}

func (s *Store) CreateRule(r *model.Rule) error {
	if err := s.attachInterface(r); err != nil {
		return err
	}
	return insertRule(s.db, r)
}

func (s *Store) UpdateRule(r *model.Rule) error {
	if r.InterfaceID == 0 && r.ID > 0 {
		old, err := s.GetRule(r.ID)
		if err != nil {
			return err
		}
		r.InterfaceID = old.InterfaceID
	}
	if err := s.attachInterface(r); err != nil {
		return err
	}
	return updateRule(s.db, r)
}

func (s *Store) DeleteRule(id int64) error {
	res, err := s.db.Exec(`DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("规则不存在")
	}
	return nil
}

func (s *Store) ToggleRule(id int64) (model.Rule, error) {
	now := nowUTC()
	res, err := s.db.Exec(`UPDATE rules SET enabled = CASE enabled WHEN 1 THEN 0 ELSE 1 END, updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return model.Rule{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.Rule{}, fmt.Errorf("规则不存在")
	}
	return s.GetRule(id)
}

// ImportResult 描述一次导入改了多少条规则。
type ImportResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Total   int `json:"total"`
}

// Import 按名称合并规则；replace 为真时先清空再写入。
func (s *Store) Import(doc model.Document, replace bool) (ImportResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var result ImportResult
	if replace {
		if _, err := tx.Exec(`DELETE FROM rules`); err != nil {
			return ImportResult{}, err
		}
		if _, err := tx.Exec(`DELETE FROM interfaces`); err != nil {
			return ImportResult{}, err
		}
	}
	for i := range doc.Rules {
		rule := doc.Rules[i]
		iface, err := ensureInterface(tx, rule.Method, rule.Path, rule.Name)
		if err != nil {
			return ImportResult{}, err
		}
		rule.InterfaceID = iface.ID
		rule.Method = iface.Method
		rule.Path = iface.Path
		if replace {
			rule.ID = 0
			if err := insertRule(tx, &rule); err != nil {
				return ImportResult{}, err
			}
			result.Created++
			continue
		}
		var id int64
		var created string
		err = tx.QueryRow(`SELECT id, created_at FROM rules WHERE name = ? AND interface_id = ? ORDER BY id LIMIT 1`, rule.Name, rule.InterfaceID).Scan(&id, &created)
		if err == sql.ErrNoRows {
			if err := insertRule(tx, &rule); err != nil {
				return ImportResult{}, err
			}
			result.Created++
			continue
		}
		if err != nil {
			return ImportResult{}, err
		}
		rule.ID = id
		rule.CreatedAt = created
		if err := updateRule(tx, &rule); err != nil {
			return ImportResult{}, err
		}
		result.Updated++
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	result.Total = result.Created + result.Updated
	return result, nil
}

func (s *Store) SaveRecording(id int64, status int, headers model.StringMap, body string) error {
	if headers == nil {
		headers = model.StringMap{}
	}
	hb, err := json.Marshal(headers)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE rules SET status_code = ?, response_headers = ?, response_body = ?, updated_at = ? WHERE id = ?`,
		status, string(hb), body, nowUTC(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("规则不存在")
	}
	return nil
}

func (s *Store) InsertLog(entry model.RequestLog) (int64, error) {
	if entry.CreatedAt == "" {
		entry.CreatedAt = nowUTC()
	}
	var ruleID any
	if entry.RuleID != nil {
		ruleID = *entry.RuleID
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO request_logs(
		rule_id, rule_name, method, path, query, headers, body, status_code, matched,
		remote_addr, duration_ms, response_headers, response_body, error, created_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ruleID, entry.RuleName, entry.Method, entry.Path, entry.Query, entry.Headers, clip(entry.Body, 64<<10),
		entry.StatusCode, bint(entry.Matched), entry.RemoteAddr, entry.DurationMS,
		entry.ResponseHeaders, clip(entry.ResponseBody, 256<<10), entry.Error, entry.CreatedAt)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(`DELETE FROM request_logs WHERE id IN (
		SELECT id FROM request_logs ORDER BY id DESC LIMIT -1 OFFSET ?
	)`, keepLogs)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListLogs(q string, limit, offset int) ([]model.RequestLog, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := ""
	args := []any{}
	if strings.TrimSpace(q) != "" {
		where = " WHERE path LIKE ? ESCAPE '\\' OR rule_name LIKE ? ESCAPE '\\' OR method LIKE ? ESCAPE '\\'"
		like := likeArg(q)
		args = append(args, like, like, like)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM request_logs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.db.Query(`SELECT id, rule_id, rule_name, method, path, query, headers, body, status_code, matched, remote_addr, duration_ms, response_headers, response_body, error, created_at FROM request_logs`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.RequestLog{}
	for rows.Next() {
		item, err := scanLog(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (s *Store) GetLog(id int64) (model.RequestLog, error) {
	row := s.db.QueryRow(`SELECT id, rule_id, rule_name, method, path, query, headers, body, status_code, matched, remote_addr, duration_ms, response_headers, response_body, error, created_at FROM request_logs WHERE id = ?`, id)
	item, err := scanLog(row)
	if err == sql.ErrNoRows {
		return model.RequestLog{}, fmt.Errorf("日志不存在")
	}
	return item, err
}

func (s *Store) DeleteLog(id int64) error {
	res, err := s.db.Exec(`DELETE FROM request_logs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("日志不存在")
	}
	return nil
}

func (s *Store) ClearLogs() error {
	_, err := s.db.Exec(`DELETE FROM request_logs`)
	return err
}

// AdoptLog 把某次响应保存成 Mock 规则。已有关联规则时覆盖响应，否则新建。
func (s *Store) AdoptLog(id int64) (model.Rule, error) {
	entry, err := s.GetLog(id)
	if err != nil {
		return model.Rule{}, err
	}
	headers := model.StringMap{}
	if strings.TrimSpace(entry.ResponseHeaders) != "" {
		_ = json.Unmarshal([]byte(entry.ResponseHeaders), &headers)
	}
	if entry.RuleID != nil {
		rule, err := s.GetRule(*entry.RuleID)
		if err != nil {
			return model.Rule{}, err
		}
		rule.Mode = "mock"
		if entry.StatusCode > 0 {
			rule.StatusCode = entry.StatusCode
		}
		rule.ResponseHeaders = headers
		rule.ResponseBody = entry.ResponseBody
		if err := rule.Normalize(); err != nil {
			return model.Rule{}, err
		}
		if err := s.UpdateRule(&rule); err != nil {
			return model.Rule{}, err
		}
		return rule, nil
	}
	status := entry.StatusCode
	if status == 0 {
		status = 200
	}
	rule := model.Rule{
		Name:            trimName("录制 " + entry.Method + " " + entry.Path),
		Enabled:         true,
		Method:          entry.Method,
		Path:            entry.Path,
		StatusCode:      status,
		ResponseHeaders: headers,
		ResponseBody:    entry.ResponseBody,
		Mode:            "mock",
		Description:     "由请求日志录制",
	}
	if rule.Path == "" || !strings.HasPrefix(rule.Path, "/") {
		rule.Path = "/"
	}
	iface, err := s.EnsureInterface(rule.Method, rule.Path, rule.Name)
	if err != nil {
		return model.Rule{}, err
	}
	rule.InterfaceID = iface.ID
	if err := rule.Normalize(); err != nil {
		return model.Rule{}, err
	}
	if err := s.CreateRule(&rule); err != nil {
		return model.Rule{}, err
	}
	return rule, nil
}

func (s *Store) ListState() ([]model.StateVar, error) {
	rows, err := s.db.Query(`SELECT key, value, updated_at FROM states ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.StateVar{}
	for rows.Next() {
		var item model.StateVar
		if err := rows.Scan(&item.Key, &item.Value, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) StateMap() (map[string]string, error) {
	items, err := s.ListState()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.Key] = item.Value
	}
	return out, nil
}

func (s *Store) SetState(key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("状态键不能为空")
	}
	_, err := s.db.Exec(`INSERT INTO states(key, value, updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, nowUTC())
	return err
}

func (s *Store) SetStatePairs(pairs map[string]string) error {
	if len(pairs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowUTC()
	for k, v := range pairs {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO states(key, value, updated_at) VALUES(?,?,?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, k, v, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteState(key string) error {
	res, err := s.db.Exec(`DELETE FROM states WHERE key = ?`, key)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("状态不存在")
	}
	return nil
}

func (s *Store) ClearState() error {
	_, err := s.db.Exec(`DELETE FROM states`)
	return err
}

func (s *Store) GetSetting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

const ruleColumns = `id, name, enabled, priority, method, path, query_match, header_match, body_match, state_match, status_code, response_headers, response_body, delay_ms, mode, proxy_url, record, state_set, description, created_at, updated_at, interface_id, flow`

type scanner interface {
	Scan(dest ...any) error
}

func scanRule(sc scanner) (model.Rule, error) {
	var r model.Rule
	var enabled, record int
	var query, header, body, state, respHeaders, stateSet, flow string
	err := sc.Scan(
		&r.ID, &r.Name, &enabled, &r.Priority, &r.Method, &r.Path,
		&query, &header, &body, &state, &r.StatusCode, &respHeaders, &r.ResponseBody,
		&r.DelayMS, &r.Mode, &r.ProxyURL, &record, &stateSet, &r.Description, &r.CreatedAt, &r.UpdatedAt, &r.InterfaceID, &flow,
	)
	if err != nil {
		return model.Rule{}, err
	}
	r.Enabled = enabled == 1
	r.Record = record == 1
	_ = json.Unmarshal([]byte(query), &r.QueryMatch)
	_ = json.Unmarshal([]byte(header), &r.HeaderMatch)
	_ = json.Unmarshal([]byte(body), &r.BodyMatch)
	_ = json.Unmarshal([]byte(state), &r.StateMatch)
	_ = json.Unmarshal([]byte(respHeaders), &r.ResponseHeaders)
	_ = json.Unmarshal([]byte(stateSet), &r.StateSet)
	if strings.TrimSpace(flow) != "" {
		_ = json.Unmarshal([]byte(flow), &r.Flow)
	}
	r.FillDefaults()
	return r, nil
}

func insertRule(e execer, r *model.Rule) error {
	now := nowUTC()
	if r.CreatedAt == "" {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	query, header, body, state, respHeaders, stateSet, flow, err := marshalRule(r)
	if err != nil {
		return err
	}
	res, err := e.Exec(`INSERT INTO rules(
		name, enabled, priority, method, path, query_match, header_match, body_match, state_match,
		status_code, response_headers, response_body, delay_ms, mode, proxy_url, record, state_set,
		description, created_at, updated_at, interface_id, flow
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Name, bint(r.Enabled), r.Priority, r.Method, r.Path, query, header, body, state,
		r.StatusCode, respHeaders, r.ResponseBody, r.DelayMS, r.Mode, r.ProxyURL, bint(r.Record), stateSet,
		r.Description, r.CreatedAt, r.UpdatedAt, r.InterfaceID, flow)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

func updateRule(e execer, r *model.Rule) error {
	r.UpdatedAt = nowUTC()
	query, header, body, state, respHeaders, stateSet, flow, err := marshalRule(r)
	if err != nil {
		return err
	}
	res, err := e.Exec(`UPDATE rules SET
		name=?, enabled=?, priority=?, method=?, path=?, query_match=?, header_match=?, body_match=?, state_match=?,
		status_code=?, response_headers=?, response_body=?, delay_ms=?, mode=?, proxy_url=?, record=?, state_set=?,
		description=?, updated_at=?, interface_id=?, flow=?
		WHERE id=?`,
		r.Name, bint(r.Enabled), r.Priority, r.Method, r.Path, query, header, body, state,
		r.StatusCode, respHeaders, r.ResponseBody, r.DelayMS, r.Mode, r.ProxyURL, bint(r.Record), stateSet,
		r.Description, r.UpdatedAt, r.InterfaceID, flow, r.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("规则不存在")
	}
	return nil
}

func marshalRule(r *model.Rule) (query, header, body, state, respHeaders, stateSet, flow string, err error) {
	r.FillDefaults()
	qb, err := json.Marshal(r.QueryMatch)
	if err != nil {
		return
	}
	hb, err := json.Marshal(r.HeaderMatch)
	if err != nil {
		return
	}
	bb, err := json.Marshal(r.BodyMatch)
	if err != nil {
		return
	}
	sb, err := json.Marshal(r.StateMatch)
	if err != nil {
		return
	}
	rh, err := json.Marshal(r.ResponseHeaders)
	if err != nil {
		return
	}
	ss, err := json.Marshal(r.StateSet)
	if err != nil {
		return
	}
	flowText := ""
	if r.Flow != nil {
		fb, ferr := json.Marshal(r.Flow)
		if ferr != nil {
			err = ferr
			return
		}
		flowText = string(fb)
	}
	return string(qb), string(hb), string(bb), string(sb), string(rh), string(ss), flowText, nil
}

func scanLog(sc scanner) (model.RequestLog, error) {
	var item model.RequestLog
	var ruleID sql.NullInt64
	var matched int
	err := sc.Scan(
		&item.ID, &ruleID, &item.RuleName, &item.Method, &item.Path, &item.Query, &item.Headers, &item.Body,
		&item.StatusCode, &matched, &item.RemoteAddr, &item.DurationMS, &item.ResponseHeaders, &item.ResponseBody,
		&item.Error, &item.CreatedAt,
	)
	if err != nil {
		return model.RequestLog{}, err
	}
	item.Matched = matched == 1
	if ruleID.Valid {
		id := ruleID.Int64
		item.RuleID = &id
	}
	return item, nil
}

func bint(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func likeArg(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}

func trimName(s string) string {
	if len(s) <= 80 {
		return s
	}
	return s[:80]
}
