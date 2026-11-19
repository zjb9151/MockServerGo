package store

import (
	"database/sql"
	"fmt"
	"strings"

	"csmock/internal/model"
)

func (s *Store) ensureColumn(table, column, decl string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	return err
}

func (s *Store) backfillInterfaces() error {
	rows, err := s.db.Query(`SELECT id, name, method, path, description, enabled FROM rules WHERE interface_id = 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id          int64
		name        string
		method      string
		path        string
		description string
		enabled     int
	}
	groups := map[string][]row{}
	var order []string
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.name, &item.method, &item.path, &item.description, &item.enabled); err != nil {
			return err
		}
		key := item.method + "\n" + item.path
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range order {
		items := groups[key]
		iface := model.Interface{
			Name:        items[0].name,
			Enabled:     false,
			Method:      items[0].method,
			Path:        items[0].path,
			Description: items[0].description,
		}
		if len(items) > 1 {
			iface.Name = items[0].method + " " + items[0].path
			iface.Description = ""
		}
		for _, item := range items {
			if item.enabled == 1 {
				iface.Enabled = true
			}
		}
		if err := iface.Normalize(); err != nil {
			return err
		}
		if err := s.CreateInterface(&iface); err != nil {
			existing, findErr := s.FindInterface(iface.Method, iface.Path)
			if findErr != nil {
				return err
			}
			iface = existing
		}
		for _, item := range items {
			if _, err := s.db.Exec(`UPDATE rules SET interface_id = ?, method = ?, path = ? WHERE id = ?`, iface.ID, iface.Method, iface.Path, item.id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ListInterfaces() ([]model.Interface, error) {
	rows, err := s.db.Query(`SELECT i.id, i.name, i.enabled, i.method, i.path, i.description, i.created_at, i.updated_at,
		(SELECT COUNT(1) FROM rules r WHERE r.interface_id = i.id)
		FROM interfaces i ORDER BY i.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Interface{}
	for rows.Next() {
		item, err := scanInterface(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetInterface(id int64) (model.Interface, error) {
	row := s.db.QueryRow(`SELECT id, name, enabled, method, path, description, created_at, updated_at,
		(SELECT COUNT(1) FROM rules r WHERE r.interface_id = interfaces.id)
		FROM interfaces WHERE id = ?`, id)
	item, err := scanInterface(row, true)
	if err == sql.ErrNoRows {
		return model.Interface{}, fmt.Errorf("接口不存在")
	}
	return item, err
}

func (s *Store) FindInterface(method, path string) (model.Interface, error) {
	row := s.db.QueryRow(`SELECT id, name, enabled, method, path, description, created_at, updated_at, 0 FROM interfaces WHERE method = ? AND path = ?`, method, path)
	item, err := scanInterface(row, true)
	if err == sql.ErrNoRows {
		return model.Interface{}, fmt.Errorf("接口不存在")
	}
	return item, err
}

func (s *Store) CreateInterface(a *model.Interface) error {
	if err := a.Normalize(); err != nil {
		return err
	}
	if _, err := s.FindInterface(a.Method, a.Path); err == nil {
		return fmt.Errorf("已有相同方法和路径的接口")
	}
	return insertInterface(s.db, a)
}

func (s *Store) UpdateInterface(a *model.Interface) error {
	if err := a.Normalize(); err != nil {
		return err
	}
	var other int64
	err := s.db.QueryRow(`SELECT id FROM interfaces WHERE method = ? AND path = ? AND id <> ?`, a.Method, a.Path, a.ID).Scan(&other)
	if err == nil {
		return fmt.Errorf("已有相同方法和路径的接口")
	}
	if err != sql.ErrNoRows {
		return err
	}
	a.UpdatedAt = nowUTC()
	res, err := s.db.Exec(`UPDATE interfaces SET name=?, enabled=?, method=?, path=?, description=?, updated_at=? WHERE id=?`,
		a.Name, bint(a.Enabled), a.Method, a.Path, a.Description, a.UpdatedAt, a.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("接口不存在")
	}
	_, err = s.db.Exec(`UPDATE rules SET method = ?, path = ? WHERE interface_id = ?`, a.Method, a.Path, a.ID)
	return err
}

func (s *Store) DeleteInterface(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM rules WHERE interface_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM interfaces WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("接口不存在")
	}
	return tx.Commit()
}

func (s *Store) ToggleInterface(id int64) (model.Interface, error) {
	res, err := s.db.Exec(`UPDATE interfaces SET enabled = CASE enabled WHEN 1 THEN 0 ELSE 1 END, updated_at = ? WHERE id = ?`, nowUTC(), id)
	if err != nil {
		return model.Interface{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.Interface{}, fmt.Errorf("接口不存在")
	}
	return s.GetInterface(id)
}

func (s *Store) ListRulesByInterface(id int64) ([]model.Rule, error) {
	rows, err := s.db.Query(`SELECT `+ruleColumns+` FROM rules WHERE interface_id = ? ORDER BY priority DESC, id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Rule{}
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// ListMatchRules 返回启用中的接口下的全部规则，供 Mock 匹配使用。
func (s *Store) ListMatchRules() ([]model.Rule, error) {
	rows, err := s.db.Query(`SELECT ` + prefixColumns(ruleColumns, "r") + ` FROM rules r
		INNER JOIN interfaces i ON i.id = r.interface_id
		WHERE i.enabled = 1
		ORDER BY r.priority DESC, r.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Rule{}
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Store) EnsureInterface(method, path, name string) (model.Interface, error) {
	return ensureInterface(s.db, method, path, name)
}

func (s *Store) attachInterface(r *model.Rule) error {
	if r.InterfaceID == 0 {
		return fmt.Errorf("请先新建接口，再添加规则")
	}
	iface, err := s.GetInterface(r.InterfaceID)
	if err != nil {
		return err
	}
	r.Method = iface.Method
	r.Path = iface.Path
	return nil
}

func ensureInterface(e execer, method, path, name string) (model.Interface, error) {
	iface := model.Interface{Name: name, Method: method, Path: path, Enabled: true}
	if err := iface.Normalize(); err != nil {
		return model.Interface{}, err
	}
	row := e.QueryRow(`SELECT id, name, enabled, method, path, description, created_at, updated_at, 0 FROM interfaces WHERE method = ? AND path = ?`, iface.Method, iface.Path)
	found, err := scanInterface(row, true)
	if err == nil {
		return found, nil
	}
	if err != sql.ErrNoRows {
		return model.Interface{}, err
	}
	if err := insertInterface(e, &iface); err != nil {
		return model.Interface{}, err
	}
	return iface, nil
}

func insertInterface(e execer, a *model.Interface) error {
	now := nowUTC()
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	res, err := e.Exec(`INSERT INTO interfaces(name, enabled, method, path, description, created_at, updated_at) VALUES(?,?,?,?,?,?,?)`,
		a.Name, bint(a.Enabled), a.Method, a.Path, a.Description, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	a.ID = id
	return nil
}

func scanInterface(sc scanner, withCount bool) (model.Interface, error) {
	var item model.Interface
	var enabled int
	if !withCount {
		err := sc.Scan(&item.ID, &item.Name, &enabled, &item.Method, &item.Path, &item.Description, &item.CreatedAt, &item.UpdatedAt)
		item.Enabled = enabled == 1
		return item, err
	}
	err := sc.Scan(&item.ID, &item.Name, &enabled, &item.Method, &item.Path, &item.Description, &item.CreatedAt, &item.UpdatedAt, &item.RuleCount)
	item.Enabled = enabled == 1
	return item, err
}

func prefixColumns(cols, alias string) string {
	parts := strings.Split(cols, ", ")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}
