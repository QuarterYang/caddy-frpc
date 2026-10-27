package caddyfrpc

import (
	"strconv"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	v1 "github.com/fatedier/frp/pkg/config/v1"
)

// parseProxyBlock 解析 proxy 子块,支持 tcp/udp/http/https/tcpmux/stcp/xtcp/sudp 所有字段。
func parseProxyBlock(d *caddyfile.Dispenser, name string) (ProxyConfig, error) {
	p := ProxyConfig{Name: name}

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		// --- 归属与基础 ---
		case "server":
			if err := readArg(d, &p.Server); err != nil {
				return p, err
			}
		case "type":
			if err := readArg(d, &p.Type); err != nil {
				return p, err
			}
		case "enabled":
			if err := readBoolPtr(d, &p.Enabled); err != nil {
				return p, err
			}
		case "local_ip":
			if err := readArg(d, &p.LocalIP); err != nil {
				return p, err
			}
		case "local_port":
			if err := readInt(d, &p.LocalPort); err != nil {
				return p, err
			}
		case "remote_port":
			if err := readInt(d, &p.RemotePort); err != nil {
				return p, err
			}

		// --- 域名 (http/https/tcpmux) ---
		case "custom_domains":
			vals, err := readArgs(d)
			if err != nil {
				return p, err
			}
			p.CustomDomains = append(p.CustomDomains, vals...)
		case "sub_domain":
			if err := readArg(d, &p.SubDomain); err != nil {
				return p, err
			}
		case "locations":
			vals, err := readArgs(d)
			if err != nil {
				return p, err
			}
			p.Locations = append(p.Locations, vals...)

		// --- HTTP 路由与鉴权 ---
		case "http_user":
			if err := readArg(d, &p.HTTPUser); err != nil {
				return p, err
			}
		case "http_password":
			if err := readArg(d, &p.HTTPPassword); err != nil {
				return p, err
			}
		case "host_header_rewrite":
			if err := readArg(d, &p.HostHeaderRewrite); err != nil {
				return p, err
			}
		case "route_by_http_user":
			if err := readArg(d, &p.RouteByHTTPUser); err != nil {
				return p, err
			}
		case "request_header":
			key, val, err := readHeaderPair(d)
			if err != nil {
				return p, err
			}
			if p.RequestHeaders == nil {
				p.RequestHeaders = make(map[string]string)
			}
			p.RequestHeaders[key] = val
		case "response_header":
			key, val, err := readHeaderPair(d)
			if err != nil {
				return p, err
			}
			if p.ResponseHeaders == nil {
				p.ResponseHeaders = make(map[string]string)
			}
			p.ResponseHeaders[key] = val
		case "multiplexer":
			if err := readArg(d, &p.Multiplexer); err != nil {
				return p, err
			}

		// --- 通用传输 ---
		case "use_encryption":
			if err := readBool(d, &p.UseEncryption); err != nil {
				return p, err
			}
		case "use_compression":
			if err := readBool(d, &p.UseCompression); err != nil {
				return p, err
			}
		case "bandwidth_limit":
			if err := readArg(d, &p.BandwidthLimit); err != nil {
				return p, err
			}
		case "bandwidth_limit_mode":
			if err := readArg(d, &p.BandwidthLimitMode); err != nil {
				return p, err
			}
		case "proxy_protocol_version":
			if err := readArg(d, &p.ProxyProtocolVersion); err != nil {
				return p, err
			}

		// --- 负载均衡 ---
		case "group":
			if err := readArg(d, &p.Group); err != nil {
				return p, err
			}
		case "group_key":
			if err := readArg(d, &p.GroupKey); err != nil {
				return p, err
			}

		// --- 健康检查 ---
		case "health_check_type":
			if err := readArg(d, &p.HealthCheckType); err != nil {
				return p, err
			}
		case "health_check_timeout_seconds":
			if err := readInt(d, &p.HealthCheckTimeoutSeconds); err != nil {
				return p, err
			}
		case "health_check_max_failed":
			if err := readInt(d, &p.HealthCheckMaxFailed); err != nil {
				return p, err
			}
		case "health_check_interval_seconds":
			if err := readInt(d, &p.HealthCheckIntervalSeconds); err != nil {
				return p, err
			}
		case "health_check_path":
			if err := readArg(d, &p.HealthCheckPath); err != nil {
				return p, err
			}
		case "health_check_http_header":
			key, val, err := readHeaderPair(d)
			if err != nil {
				return p, err
			}
			p.HealthCheckHTTPHeaders = append(p.HealthCheckHTTPHeaders, v1.HTTPHeader{Name: key, Value: val})

		// --- stcp/xtcp/sudp ---
		case "secret_key":
			if err := readArg(d, &p.SecretKey); err != nil {
				return p, err
			}
		case "allow_users":
			vals, err := readArgs(d)
			if err != nil {
				return p, err
			}
			p.AllowUsers = append(p.AllowUsers, vals...)
		case "nat_traversal_disable_assisted_addrs":
			if err := readBool(d, &p.NatTraversalDisableAssistedAddrs); err != nil {
				return p, err
			}

		// --- 元数据 ---
		case "meta":
			key, val, err := readHeaderPair(d)
			if err != nil {
				return p, err
			}
			if p.Metadatas == nil {
				p.Metadatas = make(map[string]string)
			}
			p.Metadatas[key] = val

		default:
			return p, d.Errf("unrecognized proxy subdirective: %s", d.Val())
		}
	}

	if p.Type == "" {
		p.Type = "tcp"
	}

	return p, nil
}

// readArg 读取一个参数到 string 目标。
func readArg(d *caddyfile.Dispenser, target *string) error {
	if !d.NextArg() {
		return d.ArgErr()
	}
	*target = d.Val()
	return nil
}

// readInt 读取一个参数并解析为 int。
func readInt(d *caddyfile.Dispenser, target *int) error {
	if !d.NextArg() {
		return d.ArgErr()
	}
	val, err := strconv.Atoi(d.Val())
	if err != nil {
		return d.Errf("invalid integer: %s", d.Val())
	}
	*target = val
	return nil
}

// readInt64 读取一个参数并解析为 int64。
func readInt64(d *caddyfile.Dispenser, target *int64) error {
	if !d.NextArg() {
		return d.ArgErr()
	}
	val, err := strconv.ParseInt(d.Val(), 10, 64)
	if err != nil {
		return d.Errf("invalid integer: %s", d.Val())
	}
	*target = val
	return nil
}

// readBool 读取一个 true/false 参数到 bool。
func readBool(d *caddyfile.Dispenser, target *bool) error {
	if !d.NextArg() {
		return d.ArgErr()
	}
	val, err := strconv.ParseBool(d.Val())
	if err != nil {
		return d.Errf("invalid boolean (true/false): %s", d.Val())
	}
	*target = val
	return nil
}

// readBoolPtr 读取一个 true/false 参数到 *bool (用于区分"未设置"与 false)。
func readBoolPtr(d *caddyfile.Dispenser, target **bool) error {
	var val bool
	if err := readBool(d, &val); err != nil {
		return err
	}
	*target = &val
	return nil
}

// readArgs 读取当前指令后跟随的所有参数(至少一个)。
func readArgs(d *caddyfile.Dispenser) ([]string, error) {
	var out []string
	for d.NextArg() {
		out = append(out, d.Val())
	}
	if len(out) == 0 {
		return nil, d.ArgErr()
	}
	return out, nil
}

// readHeaderPair 读取 "key value" 格式的键值对。
func readHeaderPair(d *caddyfile.Dispenser) (string, string, error) {
	if !d.NextArg() {
		return "", "", d.ArgErr()
	}
	key := d.Val()
	if !d.NextArg() {
		return "", "", d.ArgErr()
	}
	return key, d.Val(), nil
}
