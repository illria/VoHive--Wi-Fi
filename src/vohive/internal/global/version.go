package global

var (
	// Version 应用程序版本，由编译时 -ldflags -X 注入。
	// 开发默认值不是可发布版本，避免把本地或未注入构建误判成正式版本。
	Version = "v0.0.0-dev"

	// BuildTime 构建时间，由编译时 -ldflags -X 注入
	BuildTime = "unknown"

	// Commit 构建所对应的 Git commit，由编译时 -ldflags -X 注入
	Commit = "unknown"
)
