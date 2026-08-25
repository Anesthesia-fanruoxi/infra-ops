package store

import (
	"database/sql"
	"fmt"
	"net"
	"sort"
	"strings"

	"infra-ops/model"
)

// HostRepo ???????
type HostRepo struct{}

func NewHostRepo() *HostRepo {
	return &HostRepo{}
}

// GetByID ? ID ?????
func (r *HostRepo) GetByID(id int64) (*model.Host, error) {
	h := &model.Host{}
	var lastCheckAt sql.NullString
	err := DB.QueryRow(
		`SELECT id,name,ip,port,tag,remark,credential_id,status,latency_ms,info_json,last_check_at,created_at,updated_at
		 FROM hosts WHERE id=?`, id,
	).Scan(&h.ID, &h.Name, &h.IP, &h.Port, &h.Tag, &h.Remark, &h.CredentialID,
		&h.Status, &h.LatencyMs, &h.InfoJSON, &lastCheckAt, &h.CreatedAt, &h.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("host get: %w", err)
	}
	h.LastCheckAt = lastCheckAt.String
	return h, nil
}

// List 主机列表：支持按主机名/IP 排序（IP 按数值八位组比较），默认主机名升序。
func (r *HostRepo) List(tag, status, name, ip, sortBy, order string, page, pageSize int) ([]model.Host, int64, error) {
	where, args := buildHostWhere(tag, status, name, ip)

	var total int64
	if err := DB.QueryRow("SELECT COUNT(*) FROM hosts "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("host count: %w", err)
	}

	// 规模为几十~几百台：过滤后取全量，内存排序再分页，保证 IP 数值序
	query := `SELECT id,name,ip,port,tag,remark,credential_id,status,latency_ms,info_json,last_check_at,created_at,updated_at
	          FROM hosts ` + where
	rows, err := DB.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("host list: %w", err)
	}
	defer rows.Close()

	items := make([]model.Host, 0, total)
	for rows.Next() {
		var h model.Host
		if err := scanHost(rows, &h); err != nil {
			return nil, 0, fmt.Errorf("host scan: %w", err)
		}
		items = append(items, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	SortHosts(items, sortBy, order)

	offset := (page - 1) * pageSize
	if offset >= len(items) {
		return []model.Host{}, total, nil
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end], total, nil
}

// SortHosts 就地排序主机：sortBy 为 name（默认）或 ip；order 为 asc（默认）或 desc。
func SortHosts(items []model.Host, sortBy, order string) {
	desc := order == "desc"
	less := func(a, b model.Host) bool { return a.ID < b.ID }
	switch sortBy {
	case "ip":
		less = func(a, b model.Host) bool {
			x, y := ipKey(a.IP), ipKey(b.IP)
			if x == nil || y == nil {
				return y == nil && x != nil // 非法 IP 排最后
			}
			for i := 0; i < 4; i++ {
				if x[i] != y[i] {
					return x[i] < y[i]
				}
			}
			return a.ID < b.ID
		}
	default: // name
		less = func(a, b model.Host) bool {
			x, y := strings.ToLower(a.Name), strings.ToLower(b.Name)
			if x == y {
				return a.ID < b.ID
			}
			return x < y
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(items[j], items[i])
		}
		return less(items[i], items[j])
	})
}

// ipKey 解析 IPv4 为 4 字节键；非法返回 nil。
func ipKey(s string) []byte {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil || ip.To4() == nil {
		return nil
	}
	return ip.To4()
}

// Create ???????? ID?
func (r *HostRepo) Create(h *model.Host) (int64, error) {
	res, err := DB.Exec(
		`INSERT INTO hosts(name,ip,port,tag,remark,credential_id) VALUES(?,?,?,?,?,?)`,
		h.Name, h.IP, h.Port, h.Tag, h.Remark, h.CredentialID,
	)
	if err != nil {
		if isUniqueErr(err) {
			return 0, fmt.Errorf("duplicate name: %w", err)
		}
		return 0, fmt.Errorf("host create: %w", err)
	}
	return res.LastInsertId()
}

// Update ?????????
func (r *HostRepo) Update(id int64, name, ip string, port int, tag, remark string, credentialID int64) error {
	_, err := DB.Exec(
		`UPDATE hosts SET name=?, ip=?, port=?, tag=?, remark=?, credential_id=?, updated_at=datetime('now','localtime') WHERE id=?`,
		name, ip, port, tag, remark, credentialID, id,
	)
	return err
}

// UpdateProbeResult ????/?????
func (r *HostRepo) UpdateProbeResult(id int64, status string, latencyMs int, infoJSON string) error {
	_, err := DB.Exec(
		`UPDATE hosts SET status=?, latency_ms=?, info_json=?, last_check_at=datetime('now','localtime'), updated_at=datetime('now','localtime') WHERE id=?`,
		status, latencyMs, infoJSON, id,
	)
	return err
}

// Rename ???????????????????? UNIQUE ????
func (r *HostRepo) Rename(id int64, name string) error {
	_, err := DB.Exec(
		`UPDATE hosts SET name=?, updated_at=datetime('now','localtime') WHERE id=?`,
		name, id,
	)
	return err
}

// Delete ?????
func (r *HostRepo) Delete(id int64) error {
	_, err := DB.Exec("DELETE FROM hosts WHERE id=?", id)
	return err
}

// CountAll ?????????
func (r *HostRepo) CountAll() (total, online, offline, unverified int64, err error) {
	err = DB.QueryRow("SELECT COUNT(*), COALESCE(SUM(CASE WHEN status='online' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='offline' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='unverified' THEN 1 ELSE 0 END),0) FROM hosts").
		Scan(&total, &online, &offline, &unverified)
	return
}

// CountByTag counts hosts by tag.
func (r *HostRepo) CountByTag() (map[string]int, error) {
	rows, err := DB.Query("SELECT tag, COUNT(*) FROM hosts GROUP BY tag")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int)
	for rows.Next() {
		var tag string
		var count int
		if err := rows.Scan(&tag, &count); err != nil {
			return nil, err
		}
		result[tag] = count
	}
	return result, rows.Err()
}

// ListAll ????????????
func (r *HostRepo) ListAll() ([]model.Host, error) {
	rows, err := DB.Query(`SELECT id,name,ip,port,tag,remark,credential_id,status,latency_ms,info_json,last_check_at,created_at,updated_at FROM hosts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []model.Host
	for rows.Next() {
		var h model.Host
		if err := scanHost(rows, &h); err != nil {
			return nil, err
		}
		items = append(items, h)
	}
	return items, rows.Err()
}

func buildHostWhere(tag, status, name, ip string) (string, []interface{}) {
	conditions := []string{}
	args := []interface{}{}
	if keyword := strings.TrimSpace(tag); keyword != "" {
		pattern := "%" + keyword + "%"
		conditions = append(conditions, "(tag LIKE ? OR tags LIKE ?)")
		args = append(args, pattern, pattern)
	}
	if status != "" {
		conditions = append(conditions, "status=?")
		args = append(args, status)
	}
	if name != "" {
		conditions = append(conditions, "name LIKE ?")
		args = append(args, "%"+strings.TrimSpace(name)+"%")
	}
	if ip != "" {
		conditions = append(conditions, "ip LIKE ?")
		args = append(args, "%"+strings.TrimSpace(ip)+"%")
	}
	if len(conditions) == 0 {
		return "", args
	}
	return "WHERE " + joinConditions(conditions), args
}

func joinConditions(conds []string) string {
	result := conds[0]
	for i := 1; i < len(conds); i++ {
		result += " AND " + conds[i]
	}
	return result
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanHost(s scanner, h *model.Host) error {
	var lastCheckAt sql.NullString
	err := s.Scan(&h.ID, &h.Name, &h.IP, &h.Port, &h.Tag, &h.Remark, &h.CredentialID,
		&h.Status, &h.LatencyMs, &h.InfoJSON, &lastCheckAt, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return err
	}
	h.LastCheckAt = lastCheckAt.String
	return nil
}
