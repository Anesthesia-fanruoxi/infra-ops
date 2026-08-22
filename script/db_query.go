//go:build ignore

package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "data/infra-ops.db")
	if err != nil {
		fmt.Println("open err:", err)
		return
	}
	defer db.Close()

	rows, err := db.Query("SELECT addr, fingerprint FROM host_keys")
	if err != nil {
		fmt.Println("query err:", err)
		return
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var addr, fp string
		rows.Scan(&addr, &fp)
		fmt.Printf("addr=%s fp=%s\n", addr, fp)
		found = true
	}
	if !found {
		fmt.Println("host_keys 表为空")
	}
}
