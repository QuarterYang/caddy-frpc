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

func parseServerBlock(d *caddyfile.Dispenser, srv *Instance) error {
	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
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
		case "token":
			if err := readArg(d, &srv.Token); err != nil {
				return err
			}
		case "protocol":
			if err := readArg(d, &srv.Protocol); err != nil {
				return err
			}
		case "log_level":
			if err := readArg(d, &srv.LogLevel); err != nil {
				return err
			}
		case "tls_enable":
			if err := readBoolPtr(d, &srv.TLSEnable); err != nil {
				return err
			}
		default:
			return d.Errf("unrecognized server subdirective: %s", d.Val())
		}
	}
	return nil
}
