//go:build ignore

// 用法: go run script/set_setting.go <db路径> <key> <value>
// 直接修改 settings 表中的单项配置（如 server.port），改后重启生效。
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Println("usage: set_setting <db> <key> <value>")
		os.Exit(1)
	}
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?_pragma=busy_timeout(5000)")
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer db.Close()

	_, err = db.Exec(
		`INSERT INTO settings(key, value, updated_at) VALUES(?, ?, datetime('now','localtime'))
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=datetime('now','localtime')`,
		os.Args[2], os.Args[3],
	)
	if err != nil {
		fmt.Println("exec:", err)
		os.Exit(1)
	}
	fmt.Println("OK:", os.Args[2], "=", os.Args[3])
}
