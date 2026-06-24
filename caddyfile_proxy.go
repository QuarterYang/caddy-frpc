package caddyfrpc

import (
	"strconv"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// parseProxyBlock 解析 proxy 子块,支持 tcp/udp/http/https/stcp/xtcp 所有字段。
func parseProxyBlock(d *caddyfile.Dispenser, name string) (ProxyConfig, error) {
	p := ProxyConfig{Name: name}

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "server":
			if err := readArg(d, &p.Server); err != nil {
				return p, err
			}
		case "type":
			if err := readArg(d, &p.Type); err != nil {
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
		case "custom_domains":
			for d.NextArg() {
				p.CustomDomains = append(p.CustomDomains, d.Val())
			}
		case "sub_domain":
			if err := readArg(d, &p.SubDomain); err != nil {
				return p, err
			}
		case "locations":
			for d.NextArg() {
				p.Locations = append(p.Locations, d.Val())
			}
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
		case "secret_key":
			if err := readArg(d, &p.SecretKey); err != nil {
				return p, err
			}
		case "allow_users":
			for d.NextArg() {
				p.AllowUsers = append(p.AllowUsers, d.Val())
			}
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

// readBoolPtr 读取一个 true/false 参数到 *bool。
func readBoolPtr(d *caddyfile.Dispenser, target **bool) error {
	if !d.NextArg() {
		return d.ArgErr()
	}
	val, err := strconv.ParseBool(d.Val())
	if err != nil {
		return d.Errf("invalid boolean (true/false): %s", d.Val())
	}
	*target = &val
	return nil
}

// readHeaderPair 读取 "key value" 格式的 header 对。
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
