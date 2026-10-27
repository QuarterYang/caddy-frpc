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
	"github.com/fatedier/frp/pkg/config/source"
	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"

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
	Name string `json:"name,omitempty"`

	// --- 基础连接 ---
	ServerAddr string `json:"server_addr,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
	User       string `json:"user,omitempty"`
	ClientID   string `json:"client_id,omitempty"`

	// --- 认证 ---
	Token      string `json:"token,omitempty"`
	AuthMethod string `json:"auth_method,omitempty"` // token (默认), oidc

	// OIDC 认证参数,仅 auth_method 为 oidc 时生效。
	OIDCClientID         string `json:"oidc_client_id,omitempty"`
	OIDCClientSecret     string `json:"oidc_client_secret,omitempty"`
	OIDCAudience         string `json:"oidc_audience,omitempty"`
	OIDCScope            string `json:"oidc_scope,omitempty"`
	OIDCTokenEndpointURL string `json:"oidc_token_endpoint_url,omitempty"`

	// --- 传输层 ---
	Protocol     string `json:"protocol,omitempty"`      // tcp (默认), kcp, quic, websocket, wss
	WireProtocol string `json:"wire_protocol,omitempty"` // v1 (默认), v2

	DialServerTimeout    int64  `json:"dial_server_timeout,omitempty"`     // 秒,默认 10
	DialServerKeepalive  int64  `json:"dial_server_keepalive,omitempty"`   // 秒,默认 7200,负数禁用
	ConnectServerLocalIP string `json:"connect_server_local_ip,omitempty"` // 仅 tcp/websocket 生效
	ProxyURL             string `json:"proxy_url,omitempty"`               // 连接 frps 用的代理
	PoolCount            int    `json:"pool_count,omitempty"`              // 预建连接数,默认 1

	// --- 多路复用与心跳 ---
	TCPMux                  *bool `json:"tcp_mux,omitempty"`                    // 默认 true
	TCPMuxKeepaliveInterval int64 `json:"tcp_mux_keepalive_interval,omitempty"` // 秒,默认 30
	HeartbeatInterval       int64 `json:"heartbeat_interval,omitempty"`         // 秒,tcp_mux 关闭时默认 30
	HeartbeatTimeout        int64 `json:"heartbeat_timeout,omitempty"`          // 秒,tcp_mux 关闭时默认 90

	// --- 其他连接行为 ---
	DNSServer         string `json:"dns_server,omitempty"`
	NatHoleSTUNServer string `json:"nat_hole_stun_server,omitempty"`
	UDPPacketSize     int64  `json:"udp_packet_size,omitempty"` // 默认 1500
	// LoginFailExit 默认 false(与 frp 默认的 true 相反):连接失败时重试而非退出,
	// 这是 Caddy reload 重叠期能够平滑过渡的前提。
	LoginFailExit *bool `json:"login_fail_exit,omitempty"`

	// --- TLS ---
	TLSEnable                 *bool  `json:"tls_enable,omitempty"` // 默认 true (frp v0.50+)
	TLSDisableCustomFirstByte *bool  `json:"tls_disable_custom_first_byte,omitempty"`
	TLSCertFile               string `json:"tls_cert_file,omitempty"`
	TLSKeyFile                string `json:"tls_key_file,omitempty"`
	TLSTrustedCaFile          string `json:"tls_trusted_ca_file,omitempty"`
	TLSServerName             string `json:"tls_server_name,omitempty"`

	// --- QUIC 专属 (protocol quic) ---
	QUICKeepalivePeriod    int `json:"quic_keepalive_period,omitempty"`     // 秒,默认 10
	QUICMaxIdleTimeout     int `json:"quic_max_idle_timeout,omitempty"`     // 秒,默认 30
	QUICMaxIncomingStreams int `json:"quic_max_incoming_streams,omitempty"` // 默认 100000

	// --- 管理接口 (frpc admin UI) ---
	AdminAddr     string `json:"admin_addr,omitempty"` // 默认 127.0.0.1
	AdminPort     int    `json:"admin_port,omitempty"` // 0 表示不启动
	AdminUser     string `json:"admin_user,omitempty"`
	AdminPassword string `json:"admin_password,omitempty"`

	// --- 其他 ---
	LogLevel  string            `json:"log_level,omitempty"` // trace, debug, info, warn, error
	Metadatas map[string]string `json:"metadatas,omitempty"`

	// 运行时字段 (不序列化):在 Provision 阶段从 App.Proxies 中按引用重组分配。
	proxies []ProxyConfig
	svc     *client.Service
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

// ProxyConfig 描述一条代理隧道,支持 tcp/udp/http/https/tcpmux/stcp/xtcp/sudp 类型。
type ProxyConfig struct {
	// Server 指定此 proxy 所属的 server 名称。
	Server string `json:"server,omitempty"`

	Name      string `json:"name"`
	Type      string `json:"type"`              // tcp, udp, http, https, tcpmux, stcp, xtcp, sudp
	Enabled   *bool  `json:"enabled,omitempty"` // nil/true 表示启用
	LocalIP   string `json:"local_ip,omitempty"`
	LocalPort int    `json:"local_port"`

	// tcp/udp: 远程端口
	RemotePort int `json:"remote_port,omitempty"`

	// http/https/tcpmux: 域名配置
	CustomDomains []string `json:"custom_domains,omitempty"`
	SubDomain     string   `json:"sub_domain,omitempty"`

	// http/tcpmux 专属
	Locations         []string          `json:"locations,omitempty"`
	HTTPUser          string            `json:"http_user,omitempty"`
	HTTPPassword      string            `json:"http_password,omitempty"`
	HostHeaderRewrite string            `json:"host_header_rewrite,omitempty"`
	RequestHeaders    map[string]string `json:"request_headers,omitempty"`
	ResponseHeaders   map[string]string `json:"response_headers,omitempty"`
	RouteByHTTPUser   string            `json:"route_by_http_user,omitempty"`

	// tcpmux 专属
	Multiplexer string `json:"multiplexer,omitempty"` // httpconnect (默认), httpconnect2

	// 通用传输参数
	UseEncryption        bool   `json:"use_encryption,omitempty"`
	UseCompression       bool   `json:"use_compression,omitempty"`
	BandwidthLimit       string `json:"bandwidth_limit,omitempty"`      // 如 "1MB"、"512KB",空表示不限速
	BandwidthLimitMode   string `json:"bandwidth_limit_mode,omitempty"` // client (默认), server
	ProxyProtocolVersion string `json:"proxy_protocol_version,omitempty"`

	// 负载均衡分组
	Group    string `json:"group,omitempty"`
	GroupKey string `json:"group_key,omitempty"`

	// 健康检查 (type 为空表示不检查)
	HealthCheckType            string          `json:"health_check_type,omitempty"` // tcp, http
	HealthCheckTimeoutSeconds  int             `json:"health_check_timeout_seconds,omitempty"`
	HealthCheckMaxFailed       int             `json:"health_check_max_failed,omitempty"`
	HealthCheckIntervalSeconds int             `json:"health_check_interval_seconds,omitempty"`
	HealthCheckPath            string          `json:"health_check_path,omitempty"`
	HealthCheckHTTPHeaders     []v1.HTTPHeader `json:"health_check_http_headers,omitempty"`

	// stcp/xtcp/sudp
	SecretKey  string   `json:"secret_key,omitempty"`
	AllowUsers []string `json:"allow_users,omitempty"`

	// xtcp 专属:禁用本地网卡地址辅助打洞
	NatTraversalDisableAssistedAddrs bool `json:"nat_traversal_disable_assisted_addrs,omitempty"`

	Metadatas map[string]string `json:"metadatas,omitempty"`
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
	if inst.ServerAddr == "" {
		return nil, nil, fmt.Errorf("server_addr is required")
	}

	switch inst.Protocol {
	case "", "tcp", "kcp", "quic", "websocket", "wss":
	default:
		return nil, nil, fmt.Errorf("unsupported protocol %q (supported: tcp, kcp, quic, websocket, wss)", inst.Protocol)
	}
	switch inst.WireProtocol {
	case "", "v1", "v2":
	default:
		return nil, nil, fmt.Errorf("unsupported wire_protocol %q (supported: v1, v2)", inst.WireProtocol)
	}
	switch inst.AuthMethod {
	case "", "token", "oidc":
	default:
		return nil, nil, fmt.Errorf("unsupported auth_method %q (supported: token, oidc)", inst.AuthMethod)
	}

	common := &v1.ClientCommonConfig{}
	common.ServerAddr = inst.ServerAddr
	common.ServerPort = inst.ServerPort
	common.User = inst.User
	common.ClientID = inst.ClientID
	common.DNSServer = inst.DNSServer
	common.NatHoleSTUNServer = inst.NatHoleSTUNServer
	common.UDPPacketSize = inst.UDPPacketSize
	common.Metadatas = inst.Metadatas

	// 认证
	if inst.AuthMethod != "" {
		common.Auth.Method = v1.AuthMethod(inst.AuthMethod)
	}
	common.Auth.Token = inst.Token
	if inst.OIDCClientID != "" || inst.OIDCTokenEndpointURL != "" {
		common.Auth.OIDC = v1.AuthOIDCClientConfig{
			ClientID:         inst.OIDCClientID,
			ClientSecret:     inst.OIDCClientSecret,
			Audience:         inst.OIDCAudience,
			Scope:            inst.OIDCScope,
			TokenEndpointURL: inst.OIDCTokenEndpointURL,
		}
	}

	// 传输层
	common.Transport.Protocol = inst.Protocol
	common.Transport.WireProtocol = inst.WireProtocol
	common.Transport.DialServerTimeout = inst.DialServerTimeout
	common.Transport.DialServerKeepAlive = inst.DialServerKeepalive
	common.Transport.ConnectServerLocalIP = inst.ConnectServerLocalIP
	common.Transport.ProxyURL = inst.ProxyURL
	common.Transport.PoolCount = inst.PoolCount
	common.Transport.TCPMux = inst.TCPMux
	common.Transport.TCPMuxKeepaliveInterval = inst.TCPMuxKeepaliveInterval
	common.Transport.HeartbeatInterval = inst.HeartbeatInterval
	common.Transport.HeartbeatTimeout = inst.HeartbeatTimeout
	common.Transport.QUIC = v1.QUICOptions{
		KeepalivePeriod:    inst.QUICKeepalivePeriod,
		MaxIdleTimeout:     inst.QUICMaxIdleTimeout,
		MaxIncomingStreams: inst.QUICMaxIncomingStreams,
	}

	// TLS
	common.Transport.TLS.Enable = inst.TLSEnable
	common.Transport.TLS.DisableCustomTLSFirstByte = inst.TLSDisableCustomFirstByte
	common.Transport.TLS.CertFile = inst.TLSCertFile
	common.Transport.TLS.KeyFile = inst.TLSKeyFile
	common.Transport.TLS.TrustedCaFile = inst.TLSTrustedCaFile
	common.Transport.TLS.ServerName = inst.TLSServerName

	// 管理接口
	common.WebServer = v1.WebServerConfig{
		Addr:     inst.AdminAddr,
		Port:     inst.AdminPort,
		User:     inst.AdminUser,
		Password: inst.AdminPassword,
	}

	// 日志
	common.Log.Level = inst.LogLevel

	// LoginFailExit 默认 false,确保连接失败时自动重试而非退出。
	// 这对 Caddy reload 时的重叠期(新实例等待旧实例释放端口)至关重要。
	loginFailExit := false
	if inst.LoginFailExit != nil {
		loginFailExit = *inst.LoginFailExit
	}
	common.LoginFailExit = &loginFailExit

	if err := common.Complete(); err != nil {
		return nil, nil, fmt.Errorf("complete common config: %w", err)
	}

	// frp v0.69.1 起,proxy 的 user 前缀由 client 在生成 wire 消息时
	// 通过 naming.AddUserPrefix 自动追加,这里无需再手动拼前缀。
	pxyCfgs := make([]v1.ProxyConfigurer, 0, len(inst.proxies))
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
	if p.Type == "" {
		p.Type = "tcp"
	}

	base, err := buildProxyBase(p)
	if err != nil {
		return nil, err
	}

	switch p.Type {
	case "tcp":
		return buildTCPProxy(base, p)
	case "udp":
		return buildUDPProxy(base, p)
	case "http":
		return buildHTTPProxy(base, p)
	case "https":
		return buildHTTPSProxy(base, p)
	case "tcpmux":
		return buildTCPMuxProxy(base, p)
	case "stcp":
		return buildSTCPProxy(base, p)
	case "xtcp":
		return buildXTCPProxy(base, p)
	case "sudp":
		return buildSUDPProxy(base, p)
	default:
		return nil, fmt.Errorf("unsupported proxy type %q (supported: tcp, udp, http, https, tcpmux, stcp, xtcp, sudp)", p.Type)
	}
}

// buildProxyBase 构造所有代理类型共享的基础配置(后端地址、传输、负载均衡、健康检查等)。
func buildProxyBase(p ProxyConfig) (*v1.ProxyBaseConfig, error) {
	base := &v1.ProxyBaseConfig{}
	base.Name = p.Name
	base.Type = p.Type
	base.Enabled = p.Enabled
	base.LocalIP = p.LocalIP
	base.LocalPort = p.LocalPort
	base.Metadatas = p.Metadatas

	// 传输参数
	base.Transport.UseEncryption = p.UseEncryption
	base.Transport.UseCompression = p.UseCompression
	base.Transport.ProxyProtocolVersion = p.ProxyProtocolVersion
	switch p.BandwidthLimitMode {
	case "", types.BandwidthLimitModeClient, types.BandwidthLimitModeServer:
		base.Transport.BandwidthLimitMode = p.BandwidthLimitMode
	default:
		return nil, fmt.Errorf("unsupported bandwidth_limit_mode %q (supported: client, server)", p.BandwidthLimitMode)
	}
	if p.BandwidthLimit != "" {
		q, err := types.NewBandwidthQuantity(p.BandwidthLimit)
		if err != nil {
			return nil, fmt.Errorf("invalid bandwidth_limit %q: %w", p.BandwidthLimit, err)
		}
		base.Transport.BandwidthLimit = q
	}
	switch p.ProxyProtocolVersion {
	case "", "v1", "v2":
	default:
		return nil, fmt.Errorf("unsupported proxy_protocol_version %q (supported: v1, v2)", p.ProxyProtocolVersion)
	}

	// 负载均衡分组
	base.LoadBalancer.Group = p.Group
	base.LoadBalancer.GroupKey = p.GroupKey

	// 健康检查
	if p.HealthCheckType != "" {
		switch p.HealthCheckType {
		case "tcp", "http":
		default:
			return nil, fmt.Errorf("unsupported health_check_type %q (supported: tcp, http)", p.HealthCheckType)
		}
		interval := p.HealthCheckIntervalSeconds
		if interval <= 0 {
			interval = 10
		}
		timeout := p.HealthCheckTimeoutSeconds
		if timeout <= 0 {
			timeout = 3
		}
		maxFailed := p.HealthCheckMaxFailed
		if maxFailed <= 0 {
			maxFailed = 1
		}
		base.HealthCheck = v1.HealthCheckConfig{
			Type:            p.HealthCheckType,
			TimeoutSeconds:  timeout,
			MaxFailed:       maxFailed,
			IntervalSeconds: interval,
			Path:            p.HealthCheckPath,
			HTTPHeaders:     p.HealthCheckHTTPHeaders,
		}
	}

	return base, nil
}

func buildTCPProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.RemotePort <= 0 {
		return nil, fmt.Errorf("remote_port is required for tcp proxy")
	}
	cfg := &v1.TCPProxyConfig{ProxyBaseConfig: *base}
	cfg.RemotePort = p.RemotePort
	cfg.Complete()
	return cfg, nil
}

func buildUDPProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.RemotePort <= 0 {
		return nil, fmt.Errorf("remote_port is required for udp proxy")
	}
	cfg := &v1.UDPProxyConfig{ProxyBaseConfig: *base}
	cfg.RemotePort = p.RemotePort
	cfg.Complete()
	return cfg, nil
}

func buildHTTPProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if len(p.CustomDomains) == 0 && p.SubDomain == "" {
		return nil, fmt.Errorf("custom_domains or sub_domain is required for http proxy")
	}
	cfg := &v1.HTTPProxyConfig{ProxyBaseConfig: *base}
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

func buildHTTPSProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if len(p.CustomDomains) == 0 && p.SubDomain == "" {
		return nil, fmt.Errorf("custom_domains or sub_domain is required for https proxy")
	}
	cfg := &v1.HTTPSProxyConfig{ProxyBaseConfig: *base}
	cfg.CustomDomains = p.CustomDomains
	cfg.SubDomain = p.SubDomain
	cfg.Complete()
	return cfg, nil
}

func buildTCPMuxProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if len(p.CustomDomains) == 0 && p.SubDomain == "" {
		return nil, fmt.Errorf("custom_domains or sub_domain is required for tcpmux proxy")
	}
	cfg := &v1.TCPMuxProxyConfig{ProxyBaseConfig: *base}
	cfg.CustomDomains = p.CustomDomains
	cfg.SubDomain = p.SubDomain
	cfg.HTTPUser = p.HTTPUser
	cfg.HTTPPassword = p.HTTPPassword
	cfg.RouteByHTTPUser = p.RouteByHTTPUser
	cfg.Multiplexer = p.Multiplexer
	cfg.Complete()
	return cfg, nil
}

func buildSTCPProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.SecretKey == "" {
		return nil, fmt.Errorf("secret_key is required for stcp proxy")
	}
	cfg := &v1.STCPProxyConfig{ProxyBaseConfig: *base}
	cfg.Secretkey = p.SecretKey
	cfg.AllowUsers = p.AllowUsers
	cfg.Complete()
	return cfg, nil
}

func buildXTCPProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.SecretKey == "" {
		return nil, fmt.Errorf("secret_key is required for xtcp proxy")
	}
	cfg := &v1.XTCPProxyConfig{ProxyBaseConfig: *base}
	cfg.Secretkey = p.SecretKey
	cfg.AllowUsers = p.AllowUsers
	if p.NatTraversalDisableAssistedAddrs {
		cfg.NatTraversal = &v1.NatTraversalConfig{DisableAssistedAddrs: true}
	}
	cfg.Complete()
	return cfg, nil
}

func buildSUDPProxy(base *v1.ProxyBaseConfig, p ProxyConfig) (v1.ProxyConfigurer, error) {
	if p.LocalPort <= 0 {
		return nil, fmt.Errorf("local_port is required")
	}
	if p.SecretKey == "" {
		return nil, fmt.Errorf("secret_key is required for sudp proxy")
	}
	cfg := &v1.SUDPProxyConfig{ProxyBaseConfig: *base}
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
