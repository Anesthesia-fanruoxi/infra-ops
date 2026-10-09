package repo

import (
	"database/sql"
	"fmt"

	"infra-ops/model"
	"infra-ops/store"
)

// MySQLRepo MySQL 连接配置的数据存取。
type MySQLRepo struct{}

func NewMySQLRepo() *MySQLRepo { return &MySQLRepo{} }

// mysqlConnCols 列表查询列（不含密码密文）。
const mysqlConnCols = "c.id,c.name,c.host,c.port,c.username,c.default_schema,c.charset,c.read_only,c.ssh_host_id,c.remark,c.created_at,c.updated_at"

// List 分页查询 MySQL 连接配置，联表带出跳板机名称。
func (r *MySQLRepo) List(keyword string, page, pageSize int) ([]model.MySQLConnView, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE c.name LIKE ? OR c.host LIKE ? OR c.username LIKE ? OR c.remark LIKE ?"
		kw := "%" + keyword + "%"
		args = append(args, kw, kw, kw, kw)
	}

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM mysql_conns c "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("mysql count: %w", err)
	}

	offset := (page - 1) * pageSize
	// 密文列只用于判断「是否已配置密码」，不进入 View 结构，故不会下发前端
	q := "SELECT " + mysqlConnCols + ",c.encrypted_secret,IFNULL(h.name,'') FROM mysql_conns c " +
		"LEFT JOIN hosts h ON h.id = c.ssh_host_id " + where + " ORDER BY c.id DESC LIMIT ? OFFSET ?"
	rows, err := store.DB.Query(q, append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("mysql list: %w", err)
	}
	defer rows.Close()

	items := []model.MySQLConnView{}
	for rows.Next() {
		var v model.MySQLConnView
		var readOnly int
		var secret []byte
		if err := rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.DefaultSchema,
			&v.Charset, &readOnly, &v.SSHHostID, &v.Remark, &v.CreatedAt, &v.UpdatedAt, &secret, &v.SSHHostName); err != nil {
			return nil, 0, fmt.Errorf("mysql scan: %w", err)
		}
		v.ReadOnly = readOnly == 1
		v.HasPassword = len(secret) > 0
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetByID 按 ID 查询，含密码密文（仅供后端内部解密使用）。
func (r *MySQLRepo) GetByID(id int64) (*model.MySQLConn, error) {
	c := &model.MySQLConn{}
	var readOnly int
	err := store.DB.QueryRow(
		"SELECT id,name,host,port,username,encrypted_secret,default_schema,charset,read_only,ssh_host_id,remark,created_at,updated_at FROM mysql_conns WHERE id=?",
		id,
	).Scan(&c.ID, &c.Name, &c.Host, &c.Port, &c.Username, &c.EncryptedSecret, &c.DefaultSchema,
		&c.Charset, &readOnly, &c.SSHHostID, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mysql get: %w", err)
	}
	c.ReadOnly = readOnly == 1
	return c, nil
}

// Create 新增 MySQL 连接配置。
func (r *MySQLRepo) Create(c *model.MySQLConn) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO mysql_conns(name,host,port,username,encrypted_secret,default_schema,charset,read_only,ssh_host_id,remark) VALUES(?,?,?,?,?,?,?,?,?,?)",
		c.Name, c.Host, c.Port, c.Username, c.EncryptedSecret, c.DefaultSchema, c.Charset, boolToInt(c.ReadOnly), c.SSHHostID, c.Remark,
	)
	if err != nil {
		if isUniqueErr(err) {
			return 0, fmt.Errorf("duplicate name: %w", err)
		}
		return 0, fmt.Errorf("mysql create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新连接配置；EncryptedSecret 为 nil 时保留原密码。
func (r *MySQLRepo) Update(id int64, c *model.MySQLConn) error {
	if c.EncryptedSecret != nil {
		_, err := store.DB.Exec(
			"UPDATE mysql_conns SET name=?,host=?,port=?,username=?,encrypted_secret=?,default_schema=?,charset=?,read_only=?,ssh_host_id=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
			c.Name, c.Host, c.Port, c.Username, c.EncryptedSecret, c.DefaultSchema, c.Charset, boolToInt(c.ReadOnly), c.SSHHostID, c.Remark, id,
		)
		return err
	}
	_, err := store.DB.Exec(
		"UPDATE mysql_conns SET name=?,host=?,port=?,username=?,default_schema=?,charset=?,read_only=?,ssh_host_id=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
		c.Name, c.Host, c.Port, c.Username, c.DefaultSchema, c.Charset, boolToInt(c.ReadOnly), c.SSHHostID, c.Remark, id,
	)
	return err
}

// Delete 删除连接配置。
func (r *MySQLRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM mysql_conns WHERE id=?", id)
	return err
}
