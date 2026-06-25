// Package caddyfrpc 将 frp 客户端 (frpc) 集成为 Caddy 的一个 App 模块。
// 无需单独运行 frpc 进程,内网穿透隧道随 Caddy 一起启动/停止。
// 支持同时连接多个 frps 服务器,每个实例独立配置代理隧道。
//
// 基于 frp v0.69.1,使用 v1 config 包,所有配置通过 Caddyfile 内联定义。
package caddyfrpc

import (
	"context"
	"fmt"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/fatedier/frp/client"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/source"

	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(&App{})
	httpcaddyfile.RegisterGlobalOption("frpc", parseCaddyfile)
}

// App 是 frpc 的 Caddy App 模块,实现 caddy.App 接口。
type App struct {
	// Servers 是 frps 连接定义列表。
	Servers []Instance `json:"servers,omitempty"`

	// Proxies 是代理隧道定义列表,每个 proxy 通过 server 字段引用所属的连接。
	Proxies []ProxyConfig `json:"proxies,omitempty"`

	logger *zap.Logger
}

// Instance 描述一个 frps 连接定义。
type Instance struct {
	Name       string `json:"name,omitempty"`
	ServerAddr string `json:"server_addr,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
	User       string `json:"user,omitempty"`
	Token      string `json:"token,omitempty"`
	Protocol   string `json:"protocol,omitempty"`   // tcp (默认), kcp, quic, websocket, wss
	TLSEnable  *bool  `json:"tls_enable,omitempty"` // nil=用默认值(true since v0.50)
	LogLevel   string `json:"log_level,omitempty"`  // trace, debug, info, warn, error

	// 运行时字段 (不序列化):在 Provision 阶段从 App.Proxies 中按引用重组分配。
	proxies []ProxyConfig
	svc     *client.Service
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// ProxyConfig 描述一条代理隧道,支持 tcp/udp/http/https/stcp/xtcp 类型。
type ProxyConfig struct {
	// Server 指定此 proxy 所属的 server 名称。
	Server string `json:"server,omitempty"`

	Name      string `json:"name"`
	Type      string `json:"type"` // tcp, udp, http, https, stcp, xtcp
	LocalIP   string `json:"local_ip,omitempty"`
	LocalPort int    `json:"local_port"`

	// tcp/udp: 远程端口
	RemotePort int `json:"remote_port,omitempty"`

	// http/https: 域名配置
	CustomDomains []string `json:"custom_domains,omitempty"`
	SubDomain     string   `json:"sub_domain,omitempty"`

	// http 专属
	Locations         []string          `json:"locations,omitempty"`
	HTTPUser          string            `json:"http_user,omitempty"`
	HTTPPassword      string            `json:"http_password,omitempty"`
	HostHeaderRewrite string            `json:"host_header_rewrite,omitempty"`
	RequestHeaders    map[string]string `json:"request_headers,omitempty"`
	ResponseHeaders   map[string]string `json:"response_headers,omitempty"`
	RouteByHTTPUser   string            `json:"route_by_http_user,omitempty"`

	// stcp/xtcp
	SecretKey  string   `json:"secret_key,omitempty"`
	AllowUsers []string `json:"allow_users,omitempty"`
}

// CaddyModule 返回模块信息。
func (a *App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "frpc",
		New: func() caddy.Module { return new(App) },
	}
}

// Provision 将 proxy 按其 server 引用重组到对应 Server,然后创建 frp Service。
func (a *App) Provision(ctx caddy.Context) error {
	a.logger = ctx.Logger()
	bridgeFrpLogs(a.logger)

	if len(a.Servers) == 0 {
		return fmt.Errorf("frpc: no servers configured")
	}

	// 构建 server 名称 → 索引的映射,校验重名
	srvMap := make(map[string]int)
	for i := range a.Servers {
		srv := &a.Servers[i]
		if srv.Name == "" {
			return fmt.Errorf("frpc: server at index %d has no name", i)
		}
		if _, exists := srvMap[srv.Name]; exists {
			return fmt.Errorf("frpc: duplicate server name %q", srv.Name)
		}
		srvMap[srv.Name] = i
	}

	// 校验全局 proxy 名称唯一性
	proxyNames := make(map[string]string) // name → server reference
	for _, p := range a.Proxies {
		if prevSrv, exists := proxyNames[p.Name]; exists {
			return fmt.Errorf("frpc: duplicate proxy name %q (referenced by server %q and server %q)",
				p.Name, prevSrv, p.Server)
		}
		proxyNames[p.Name] = p.Server
	}

	// 将 proxy 按其 server 引用分配到对应的 Server
	for _, p := range a.Proxies {
		if p.Server == "" {
			return fmt.Errorf("frpc: proxy %q has no server reference", p.Name)
		}
		idx, ok := srvMap[p.Server]
		if !ok {
			return fmt.Errorf("frpc: proxy %q references unknown server %q", p.Name, p.Server)
		}
		a.Servers[idx].proxies = append(a.Servers[idx].proxies, p)
	}

	// 为每个 server 创建 frp Service
	for i := range a.Servers {
		srv := &a.Servers[i]

		a.logger.Info("frpc: provisioning server",
			zap.String("name", srv.Name),
			zap.String("server", srv.ServerAddr),
			zap.Int("proxies", len(srv.proxies)),
		)

		if err := srv.build(); err != nil {
			return fmt.Errorf("frpc: server %q: %w", srv.Name, err)
		}
		srv.ctx, srv.cancel = context.WithCancel(context.Background())
	}

	return nil
}

// build 解析实例配置并创建 frp Service。
//
// frp v0.69.1 起,client.ServiceOptions 不再直接接收 ProxyCfgs,
// 而是通过 ConfigSourceAggregator 提供配置源。这里使用内存型的
// source.ConfigSource 装载代理配置,再交给 NewService 内部完成
// Complete/Filter 流程。
func (inst *Instance) build() error {
	common, pxyCfgs, err := inst.buildConfig()
	if err != nil {
		return err
	}

	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll(pxyCfgs, nil); err != nil {
		return fmt.Errorf("set config source: %w", err)
	}
	aggregator := source.NewAggregator(configSource)

	svc, err := client.NewService(client.ServiceOptions{
		Common:                 common,
		ConfigSourceAggregator: aggregator,
	})
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}

	inst.svc = svc
	return nil
}

// buildConfig 从 Instance 的内联字段构造 frp v1 配置对象。
func (inst *Instance) buildConfig() (*v1.ClientCommonConfig, []v1.ProxyConfigurer, error) {
	common := &v1.ClientCommonConfig{}

	if inst.ServerAddr == "" {
		return nil, nil, fmt.Errorf("server_addr is required")
	}
	common.ServerAddr = inst.ServerAddr
	if inst.ServerPort > 0 {
		common.ServerPort = inst.ServerPort
	}
	if inst.User != "" {
		common.User = inst.User
	}
	if inst.Token != "" {
		common.Auth.Token = inst.Token
	}
	if inst.Protocol != "" {
		common.Transport.Protocol = inst.Protocol
	}
	if inst.LogLevel != "" {
		common.Log.Level = inst.LogLevel
	}
	// LoginFailExit 始终为 false,确保连接失败时自动重试而非退出。
	// 这对 Caddy reload 时的重叠期(新实例等待旧实例释放端口)至关重要。
	loginFailExit := false
	common.LoginFailExit = &loginFailExit
	if inst.TLSEnable != nil {
		common.Transport.TLS.Enable = inst.TLSEnable
	}

	if err := common.Complete(); err != nil {
		return nil, nil, fmt.Errorf("complete common config: %w", err)
	}

	// frp v0.69.1 起,proxy 的 user 前缀由 client 在生成 wire 消息时
	// 通过 naming.AddUserPrefix 自动追加,这里无需再手动拼前缀。
	var pxyCfgs []v1.ProxyConfigurer
	for _, p := range inst.proxies {
		pcfg, err := buildProxyConf(p)
		if err != nil {
			return nil, nil, fmt.Errorf("proxy %q: %w", p.Name, err)
		}
		pxyCfgs = append(pxyCfgs, pcfg)
	}

	return common, pxyCfgs, nil
}

// buildProxyConf 根据类型构造对应的 v1.ProxyConfigurer 实现。
func buildProxyConf(p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	switch p.Type {
	case "tcp":
		return buildTCPProxy(p)
	case "udp":
		return buildUDPProxy(p)
	case "http":
		return buildHTTPProxy(p)
	case "https":
		return buildHTTPSProxy(p)
	case "stcp":
		return buildSTCPProxy(p)
	case "xtcp":
		return buildXTCPProxy(p)
	default:
		return nil, fmt.Errorf("unsupported proxy type %q (supported: tcp, udp, http, https, stcp, xtcp)", p.Type)
	}
}

func buildTCPProxy(p ProxyConfig) (*v1.TCPProxyConfig, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.RemotePort <= 0 {
		return nil, fmt.Errorf("remote_port is required for tcp proxy")
	}
	cfg := &v1.TCPProxyConfig{}
	cfg.Name = p.Name
	cfg.Type = string(v1.ProxyTypeTCP)
	cfg.LocalIP = p.LocalIP
	cfg.LocalPort = p.LocalPort
	cfg.RemotePort = p.RemotePort
	cfg.Complete()
	return cfg, nil
}

func buildUDPProxy(p ProxyConfig) (*v1.UDPProxyConfig, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.RemotePort <= 0 {
		return nil, fmt.Errorf("remote_port is required for udp proxy")
	}
	cfg := &v1.UDPProxyConfig{}
	cfg.Name = p.Name
	cfg.Type = string(v1.ProxyTypeUDP)
	cfg.LocalIP = p.LocalIP
	cfg.LocalPort = p.LocalPort
	cfg.RemotePort = p.RemotePort
	cfg.Complete()
	return cfg, nil
}

func buildHTTPProxy(p ProxyConfig) (*v1.HTTPProxyConfig, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if len(p.CustomDomains) == 0 && p.SubDomain == "" {
		return nil, fmt.Errorf("custom_domains or sub_domain is required for http proxy")
	}
	cfg := &v1.HTTPProxyConfig{}
	cfg.Name = p.Name
	cfg.Type = string(v1.ProxyTypeHTTP)
	cfg.LocalIP = p.LocalIP
	cfg.LocalPort = p.LocalPort
	cfg.CustomDomains = p.CustomDomains
	cfg.SubDomain = p.SubDomain
	cfg.Locations = p.Locations
	cfg.HTTPUser = p.HTTPUser
	cfg.HTTPPassword = p.HTTPPassword
	cfg.HostHeaderRewrite = p.HostHeaderRewrite
	cfg.RequestHeaders.Set = p.RequestHeaders
	cfg.ResponseHeaders.Set = p.ResponseHeaders
	cfg.RouteByHTTPUser = p.RouteByHTTPUser
	cfg.Complete()
	return cfg, nil
}

func buildHTTPSProxy(p ProxyConfig) (*v1.HTTPSProxyConfig, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if len(p.CustomDomains) == 0 && p.SubDomain == "" {
		return nil, fmt.Errorf("custom_domains or sub_domain is required for https proxy")
	}
	cfg := &v1.HTTPSProxyConfig{}
	cfg.Name = p.Name
	cfg.Type = string(v1.ProxyTypeHTTPS)
	cfg.LocalIP = p.LocalIP
	cfg.LocalPort = p.LocalPort
	cfg.CustomDomains = p.CustomDomains
	cfg.SubDomain = p.SubDomain
	cfg.Complete()
	return cfg, nil
}

func buildSTCPProxy(p ProxyConfig) (*v1.STCPProxyConfig, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.SecretKey == "" {
		return nil, fmt.Errorf("secret_key is required for stcp proxy")
	}
	cfg := &v1.STCPProxyConfig{}
	cfg.Name = p.Name
	cfg.Type = string(v1.ProxyTypeSTCP)
	cfg.LocalIP = p.LocalIP
	cfg.LocalPort = p.LocalPort
	cfg.Secretkey = p.SecretKey
	cfg.AllowUsers = p.AllowUsers
	cfg.Complete()
	return cfg, nil
}

func buildXTCPProxy(p ProxyConfig) (*v1.XTCPProxyConfig, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.SecretKey == "" {
		return nil, fmt.Errorf("secret_key is required for xtcp proxy")
	}
	cfg := &v1.XTCPProxyConfig{}
	cfg.Name = p.Name
	cfg.Type = string(v1.ProxyTypeXTCP)
	cfg.LocalIP = p.LocalIP
	cfg.LocalPort = p.LocalPort
	cfg.Secretkey = p.SecretKey
	cfg.AllowUsers = p.AllowUsers
	cfg.Complete()
	return cfg, nil
}

// Start 在后台 goroutine 中启动所有 frpc 实例。
//
// Caddy reload 机制说明:
// Caddy 在 reload 时先 Start 新 App 实例,成功后才 Stop 旧 App 实例。
// 这意味着新旧 frpc 实例会短暂重叠,可能导致端口冲突。
// LoginFailExit 始终为 false,新实例会自动重试连接,
// 等旧实例 Stop 后端口释放即可成功。
func (a *App) Start() error {
	for i := range a.Servers {
		srv := &a.Servers[i]
		srv.done = make(chan struct{})
		a.logger.Info("frpc: starting server",
			zap.String("name", srv.Name),
			zap.String("server", srv.ServerAddr),
		)
		go func(srv *Instance) {
			defer close(srv.done)
			if err := srv.svc.Run(srv.ctx); err != nil {
				a.logger.Error("frpc: server exited with error",
					zap.String("name", srv.Name),
					zap.Error(err),
				)
			}
			a.logger.Info("frpc: server stopped", zap.String("name", srv.Name))
		}(srv)
	}
	return nil
}

// Stop 优雅关闭所有 frpc 实例。
func (a *App) Stop() error {
	for i := range a.Servers {
		srv := &a.Servers[i]
		a.logger.Info("frpc: stopping server", zap.String("name", srv.Name))

		if srv.cancel != nil {
			srv.cancel()
		}

		if srv.done != nil {
			select {
			case <-srv.done:
			case <-time.After(3 * time.Second):
				a.logger.Warn("frpc: server did not stop gracefully, forcing close",
					zap.String("name", srv.Name))
				if srv.svc != nil {
					srv.svc.Close()
				}
			}
		} else if srv.svc != nil {
			srv.svc.Close()
		}
	}
	return nil
}

// 接口断言
var (
	_ caddy.App         = (*App)(nil)
	_ caddy.Provisioner = (*App)(nil)
)
