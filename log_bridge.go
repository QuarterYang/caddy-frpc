package caddyfrpc

import (
	"strings"
	"time"

	goliblog "github.com/fatedier/golib/log"
	frplog "github.com/fatedier/frp/pkg/util/log"

	"go.uber.org/zap"
)

// caddyLogWriter 实现 golib/log.Writer 接口,将 frp 的日志转发到 Caddy 的 zap logger。
//
// frp v0.61.0 使用 github.com/fatedier/golib/log 作为日志后端。
// golib/log 的 Logger.out 字段是 io.Writer 类型,但在 log() 方法中会检查
// 是否实现了 Writer 接口(WriteLog 方法),如果实现了就调用 WriteLog 传入级别信息。
//
// 因此我们同时实现 Write(io.Writer 接口要求) 和 WriteLog(golib Writer 接口),
// 让 golib/log 优先调用 WriteLog 以获取日志级别。
type caddyLogWriter struct {
	logger *zap.Logger
}

// Write 实现 io.Writer 接口,作为 fallback(当 golib/log 不走 WriteLog 路径时)。
// 将原始字节作为 Info 级别记录。
func (w *caddyLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	w.logger.Info(extractMessage(msg),
		zap.String("component", "frp"),
	)
	return len(p), nil
}

// WriteLog 是 golib/log.Writer 接口的方法,带有日志级别信息。
// p 是已格式化的日志行(包含时间戳、级别前缀、调用者信息),
// level 是日志级别,when 是时间戳。
func (w *caddyLogWriter) WriteLog(p []byte, level goliblog.Level, when time.Time) (int, error) {
	msg := strings.TrimRight(string(p), "\n")

	// golib/log 格式: "2026/06/24 08:54:10 [W] [control.go:257] [prefix] message"
	// 提取 [级别标记] 之后的内容作为纯消息
	cleanMsg := extractMessage(msg)

	fields := []zap.Field{
		zap.String("component", "frp"),
		zap.Time("frp_time", when),
	}

	switch level {
	case goliblog.TraceLevel, goliblog.DebugLevel:
		w.logger.Debug(cleanMsg, fields...)
	case goliblog.InfoLevel:
		w.logger.Info(cleanMsg, fields...)
	case goliblog.WarnLevel:
		w.logger.Warn(cleanMsg, fields...)
	case goliblog.ErrorLevel:
		w.logger.Error(cleanMsg, fields...)
	default:
		w.logger.Info(cleanMsg, fields...)
	}

	return len(p), nil
}

// extractMessage 从 golib/log 格式的日志行中提取纯消息内容。
// 输入格式: "2026/06/24 08:54:10 [W] [control.go:257] [prefix] actual message"
// 提取结果: "[control.go:257] [prefix] actual message"
func extractMessage(msg string) string {
	// 找到第一个 "]" 后的内容(跳过时间戳)
	idx := strings.Index(msg, "] ")
	if idx == -1 {
		return msg
	}
	rest := msg[idx+2:]

	// 如果还有级别标记 [W]/[I]/[E] 等,跳过它
	if len(rest) > 0 && rest[0] == '[' {
		if idx2 := strings.Index(rest, "] "); idx2 != -1 {
			rest = rest[idx2+2:]
		}
	}

	return rest
}

// bridgeFrpLogs 将 frp 的全局日志重定向到 Caddy 的 zap logger。
// 在每次 Provision 时调用,确保 reload 后日志仍指向当前 Caddy 实例的 logger。
//
// frp v0.61.0 的日志架构:
//   - frp/pkg/util/log.Logger (全局单例, *golib/log.Logger)
//   - xlog 从 context 中获取 prefix,委托给 frp/pkg/util/log.Logger
//
// 通过 WithOutput 替换 Logger 的输出为我们的 caddyLogWriter。
// 每次 Provision 都重新设置,因为 reload 会创建新的 Caddy Context 和 logger。
func bridgeFrpLogs(logger *zap.Logger) {
	writer := &caddyLogWriter{logger: logger}

	// 每次都重新设置,确保 reload 后指向新的 Caddy logger 实例
	frplog.Logger = frplog.Logger.WithOptions(
		goliblog.WithOutput(writer),
		goliblog.WithLevel(goliblog.TraceLevel),
	)

	logger.Info("frpc: frp logs bridged to caddy logger")
}

// resetLogBridge 用于测试时重置
func resetLogBridge() {}
