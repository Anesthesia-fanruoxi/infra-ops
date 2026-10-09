package repo

import (
	"database/sql"
	"fmt"

	"infra-ops/model"
	"infra-ops/store"
)

// RedisRepo Redis 连接配置的数据存取。
type RedisRepo struct{}

func NewRedisRepo() *RedisRepo { return &RedisRepo{} }

// redisConnCols 列表查询列（不含密码密文）。
const redisConnCols = "c.id,c.name,c.host,c.port,c.username,c.default_db,c.ssh_host_id,c.remark,c.created_at,c.updated_at"

// List 分页查询 Redis 连接配置，联表带出跳板机名称。
func (r *RedisRepo) List(keyword string, page, pageSize int) ([]model.RedisConnView, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE c.name LIKE ? OR c.host LIKE ? OR c.remark LIKE ?"
		kw := "%" + keyword + "%"
		args = append(args, kw, kw, kw)
	}

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM redis_conns c "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("redis count: %w", err)
	}

	offset := (page - 1) * pageSize
	// 密文列只用于判断「是否已配置密码」，不进入 View 结构，故不会下发前端
	q := "SELECT " + redisConnCols + ",c.encrypted_secret,IFNULL(h.name,'') FROM redis_conns c " +
		"LEFT JOIN hosts h ON h.id = c.ssh_host_id " + where + " ORDER BY c.id DESC LIMIT ? OFFSET ?"
	rows, err := store.DB.Query(q, append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("redis list: %w", err)
	}
	defer rows.Close()

	items := []model.RedisConnView{}
	for rows.Next() {
		var v model.RedisConnView
		var secret []byte
		if err := rows.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.DefaultDB,
			&v.SSHHostID, &v.Remark, &v.CreatedAt, &v.UpdatedAt, &secret, &v.SSHHostName); err != nil {
			return nil, 0, fmt.Errorf("redis scan: %w", err)
		}
		v.HasPassword = len(secret) > 0
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetByID 按 ID 查询，含密码密文（仅供后端内部解密使用）。
func (r *RedisRepo) GetByID(id int64) (*model.RedisConn, error) {
	c := &model.RedisConn{}
	err := store.DB.QueryRow(
		"SELECT id,name,host,port,username,encrypted_secret,default_db,ssh_host_id,remark,created_at,updated_at FROM redis_conns WHERE id=?",
		id,
	).Scan(&c.ID, &c.Name, &c.Host, &c.Port, &c.Username, &c.EncryptedSecret, &c.DefaultDB,
		&c.SSHHostID, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}
	return c, nil
}

// Create 新增 Redis 连接配置。
func (r *RedisRepo) Create(c *model.RedisConn) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO redis_conns(name,host,port,username,encrypted_secret,default_db,ssh_host_id,remark) VALUES(?,?,?,?,?,?,?,?)",
		c.Name, c.Host, c.Port, c.Username, c.EncryptedSecret, c.DefaultDB, c.SSHHostID, c.Remark,
	)
	if err != nil {
		if isUniqueErr(err) {
			return 0, fmt.Errorf("duplicate name: %w", err)
		}
		return 0, fmt.Errorf("redis create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新连接配置；EncryptedSecret 为 nil 时保留原密码。
func (r *RedisRepo) Update(id int64, c *model.RedisConn) error {
	if c.EncryptedSecret != nil {
		_, err := store.DB.Exec(
			"UPDATE redis_conns SET name=?,host=?,port=?,username=?,encrypted_secret=?,default_db=?,ssh_host_id=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
			c.Name, c.Host, c.Port, c.Username, c.EncryptedSecret, c.DefaultDB, c.SSHHostID, c.Remark, id,
		)
		return err
	}
	_, err := store.DB.Exec(
		"UPDATE redis_conns SET name=?,host=?,port=?,username=?,default_db=?,ssh_host_id=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
		c.Name, c.Host, c.Port, c.Username, c.DefaultDB, c.SSHHostID, c.Remark, id,
	)
	return err
}

// Delete 删除连接配置。
func (r *RedisRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM redis_conns WHERE id=?", id)
	return err
}
