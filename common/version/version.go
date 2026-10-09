// Package version 应用版本号的唯一来源：/api/version 与设置页的系统信息共用，避免两处各写一份而漂移。
package version

// Version 当前版本号。
// 发布构建可用 -ldflags "-X infra-ops/common/version.Version=x.y.z" 覆盖。
var Version = "0.1.0-dev"
