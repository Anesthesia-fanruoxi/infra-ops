// Package sysutil 提供运行时自适应参数计算。
package sysutil

import "runtime"

// AdaptiveConcurrency 返回 SSH 扇出自适应并发数：
// 以 CPU 核数×4 为基准，下限 8、上限 32，且不超过目标主机数。
// n<=0 时仅按 CPU 计算。
func AdaptiveConcurrency(n int) int {
	c := runtime.NumCPU() * 4
	if c < 8 {
		c = 8
	}
	if c > 32 {
		c = 32
	}
	if n > 0 && n < c {
		return n
	}
	return c
}
