package caddyfrpc

import (
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func parseCaddyfile(d *caddyfile.Dispenser, _ any) (any, error) {
	app := &App{}

	for d.Next() {
		for nesting := d.Nesting(); d.NextBlock(nesting); {
			switch d.Val() {
			case "server":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				srv := &Instance{Name: d.Val()}
				if err := parseServerBlock(d, srv); err != nil {
					return nil, err
				}
				app.Servers = append(app.Servers, *srv)
			case "proxy":
				if !d.NextArg() {
					return nil, d.ArgErr()
				}
				p, err := parseProxyBlock(d, d.Val())
				if err != nil {
					return nil, err
				}
				app.Proxies = append(app.Proxies, p)
			default:
				return nil, d.Errf("unrecognized subdirective: %s (use 'server' or 'proxy')", d.Val())
			}
		}
	}

	if len(app.Servers) == 0 {
		return nil, d.Err("frpc: no servers configured; add at least one 'server <name> { ... }' block")
	}
	for i := range app.Servers {
		if app.Servers[i].ServerAddr == "" {
			return nil, d.Errf("frpc: server %q requires server_addr", app.Servers[i].Name)
		}
	}

	return httpcaddyfile.App{
		Name:  "frpc",
		Value: caddyconfig.JSON(app, nil),
	}, nil
}

// parseServerBlock 解析 server 子块,覆盖 frpc 常用的连接/认证/传输/日志/管理接口参数。
func parseServerBlock(d *caddyfile.Dispenser, srv *Instance) error {
	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		// --- 基础连接 ---
		case "server_addr":
			if err := readArg(d, &srv.ServerAddr); err != nil {
				return err
			}
		case "server_port":
			if err := readInt(d, &srv.ServerPort); err != nil {
				return err
			}
		case "user":
			if err := readArg(d, &srv.User); err != nil {
				return err
			}
		case "client_id":
			if err := readArg(d, &srv.ClientID); err != nil {
				return err
			}

		// --- 认证 ---
		case "token":
			if err := readArg(d, &srv.Token); err != nil {
				return err
			}
		case "auth_method":
			if err := readArg(d, &srv.AuthMethod); err != nil {
				return err
			}
		case "oidc_client_id":
			if err := readArg(d, &srv.OIDCClientID); err != nil {
				return err
			}
		case "oidc_client_secret":
			if err := readArg(d, &srv.OIDCClientSecret); err != nil {
				return err
			}
		case "oidc_audience":
			if err := readArg(d, &srv.OIDCAudience); err != nil {
				return err
			}
		case "oidc_scope":
			if err := readArg(d, &srv.OIDCScope); err != nil {
				return err
			}
		case "oidc_token_endpoint_url":
			if err := readArg(d, &srv.OIDCTokenEndpointURL); err != nil {
				return err
			}

		// --- 传输 ---
		case "protocol":
			if err := readArg(d, &srv.Protocol); err != nil {
				return err
			}
		case "wire_protocol":
			if err := readArg(d, &srv.WireProtocol); err != nil {
				return err
			}
		case "dial_server_timeout":
			if err := readInt64(d, &srv.DialServerTimeout); err != nil {
				return err
			}
		case "dial_server_keepalive":
			if err := readInt64(d, &srv.DialServerKeepalive); err != nil {
				return err
			}
		case "connect_server_local_ip":
			if err := readArg(d, &srv.ConnectServerLocalIP); err != nil {
				return err
			}
		case "proxy_url":
			if err := readArg(d, &srv.ProxyURL); err != nil {
				return err
			}
		case "pool_count":
			if err := readInt(d, &srv.PoolCount); err != nil {
				return err
			}

		// --- 多路复用与心跳 ---
		case "tcp_mux":
			if err := readBoolPtr(d, &srv.TCPMux); err != nil {
				return err
			}
		case "tcp_mux_keepalive_interval":
			if err := readInt64(d, &srv.TCPMuxKeepaliveInterval); err != nil {
				return err
			}
		case "heartbeat_interval":
			if err := readInt64(d, &srv.HeartbeatInterval); err != nil {
				return err
			}
		case "heartbeat_timeout":
			if err := readInt64(d, &srv.HeartbeatTimeout); err != nil {
				return err
			}

		// --- 其他连接行为 ---
		case "dns_server":
			if err := readArg(d, &srv.DNSServer); err != nil {
				return err
			}
		case "nat_hole_stun_server":
			if err := readArg(d, &srv.NatHoleSTUNServer); err != nil {
				return err
			}
		case "udp_packet_size":
			if err := readInt64(d, &srv.UDPPacketSize); err != nil {
				return err
			}
		case "login_fail_exit":
			if err := readBoolPtr(d, &srv.LoginFailExit); err != nil {
				return err
			}

		// --- TLS ---
		case "tls_enable":
			if err := readBoolPtr(d, &srv.TLSEnable); err != nil {
				return err
			}
		case "tls_disable_custom_first_byte":
			if err := readBoolPtr(d, &srv.TLSDisableCustomFirstByte); err != nil {
				return err
			}
		case "tls_cert_file":
			if err := readArg(d, &srv.TLSCertFile); err != nil {
				return err
			}
		case "tls_key_file":
			if err := readArg(d, &srv.TLSKeyFile); err != nil {
				return err
			}
		case "tls_trusted_ca_file":
			if err := readArg(d, &srv.TLSTrustedCaFile); err != nil {
				return err
			}
		case "tls_server_name":
			if err := readArg(d, &srv.TLSServerName); err != nil {
				return err
			}

		// --- QUIC (protocol quic) ---
		case "quic_keepalive_period":
			if err := readInt(d, &srv.QUICKeepalivePeriod); err != nil {
				return err
			}
		case "quic_max_idle_timeout":
			if err := readInt(d, &srv.QUICMaxIdleTimeout); err != nil {
				return err
			}
		case "quic_max_incoming_streams":
			if err := readInt(d, &srv.QUICMaxIncomingStreams); err != nil {
				return err
			}

		// --- 管理接口 ---
		case "admin_addr":
			if err := readArg(d, &srv.AdminAddr); err != nil {
				return err
			}
		case "admin_port":
			if err := readInt(d, &srv.AdminPort); err != nil {
				return err
			}
		case "admin_user":
			if err := readArg(d, &srv.AdminUser); err != nil {
				return err
			}
		case "admin_password":
			if err := readArg(d, &srv.AdminPassword); err != nil {
				return err
			}

		// --- 其他 ---
		case "log_level":
			if err := readArg(d, &srv.LogLevel); err != nil {
				return err
			}
		case "meta":
			key, val, err := readHeaderPair(d)
			if err != nil {
				return err
			}
			if srv.Metadatas == nil {
				srv.Metadatas = make(map[string]string)
			}
			srv.Metadatas[key] = val

		default:
			return d.Errf("unrecognized server subdirective: %s", d.Val())
		}
	}
	return nil
}
