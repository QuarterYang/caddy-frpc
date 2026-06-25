# caddy-frpc
> **特别声明**：本项目完全使用GLM-5.2开发，仅手动编写样例和执行测试。

将 [frp](https://github.com/fatedier/frp) 客户端 (frpc) 集成为 [Caddy](https://github.com/caddyserver/caddy) v2 的 App 模块。

无需单独运行 frpc 进程,内网穿透隧道随 Caddy 一起启动/停止。**支持同时连接多个 frps 服务器**,每个实例独立配置代理隧道。

## 构建

```bash
xcaddy build \
  --with github.com/quarteryang/caddy-frpc
```

## 配置

### Caddyfile 方式

`server` 和 `proxy` 是 `frpc` 块内的平级指令。`server` 定义 frps 连接，`proxy` 通过 `server` 指令引用所属连接：

```caddyfile
{
    frpc {
        server serverA {
            server_addr 1.2.3.4
            server_port 7000
            user xxx
            token yyy
            tls_enable false
        }

        server serverB {
            server_addr 5.6.7.8
            server_port 7000
            user aaa
            token bbb
        }

        proxy proxyA {
            server serverA
            type tcp
            local_ip 127.0.0.1
            local_port 1234
            remote_port 12345
        }

        proxy proxyB {
            server serverB
            type tcp
            local_ip 127.0.0.1
            local_port 1234
            remote_port 12345
        }
    }
}
```

### JSON 方式

```json
{
  "apps": {
    "frpc": {
      "servers": [
        { "name": "serverA", "server_addr": "1.2.3.4", "server_port": 7000, "user": "xxx", "token": "yyy", "tls_enable": false },
        { "name": "serverB", "server_addr": "5.6.7.8", "server_port": 7000, "user": "aaa", "token": "bbb" }
      ],
      "proxies": [
        { "server": "serverA", "name": "proxyA", "type": "tcp", "local_ip": "127.0.0.1", "local_port": 1234, "remote_port": 12345 },
        { "server": "serverB", "name": "proxyB", "type": "tcp", "local_ip": "127.0.0.1", "local_port": 1234, "remote_port": 12345 }
      ]
    }
  }
}
```

## 指令参考

### server 子块指令

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `server_addr` | frps 服务器地址 | **必填** |
| `server_port` | frps 服务器端口 | 7000 |
| `user` | 用户名 | - |
| `token` | 认证 token | - |
| `protocol` | 传输协议:tcp/kcp/quic/websocket/wss | tcp |
| `tls_enable` | 启用 TLS (true/false) | true |
| `log_level` | 日志级别:trace/debug/info/warn/error | info |

### proxy 子块指令

| 指令 | 说明 |
|------|------|
| `server` | 引用的 server 名称(**必填**) |
| `type` | 代理类型:tcp/udp/http/https/stcp/xtcp(默认 tcp) |
| `local_ip` | 本地 IP(默认 127.0.0.1) |
| `local_port` | 本地端口(**必填**) |
| `remote_port` | 远程端口(tcp/udp **必填**) |
| `custom_domains` | 自定义域名(http/https,可多个) |
| `sub_domain` | 子域名(http/https) |
| `locations` | URL 路径前缀(http,可多个) |
| `http_user` | HTTP 基本认证用户名(http) |
| `http_password` | HTTP 基本认证密码(http) |
| `host_header_rewrite` | 重写 Host 头(http) |
| `request_header` | 设置请求头(http,格式:`request_header <key> <value>`) |
| `response_header` | 设置响应头(http,格式:`response_header <key> <value>`) |
| `route_by_http_user` | 按 HTTP 用户路由(http) |
| `secret_key` | 加密密钥(stcp/xtcp **必填**) |
| `allow_users` | 允许的访问者用户列表(stcp/xtcp,可多个) |

## 注意事项

- **依赖兼容性**:frp v0.69.1 与 Caddy v2.11.3 的第三方依赖可能存在版本冲突(尤其是 `quic-go`、`golang.org/x/net` 等)。构建时如遇到编译错误,运行 `go mod tidy`;若仍有问题,在 go.mod 中添加 `replace` 指令调整版本。frp v0.69.1 仍依赖其自维护的 yamux fork,go.mod 中已通过 `replace github.com/hashicorp/yamux => github.com/fatedier/yamux ...` 指向对应版本。
- **信号处理**:frp v0.69.1 的 `Run(ctx)` 不注册信号处理器、不调用 `os.Exit`,完全通过 context 取消实现优雅停止,不会干扰 Caddy 的信号处理。
- **配置热更新 (Reload)**:支持通过 `caddy reload` 重新加载 Caddyfile。Caddy 会先 Start 新配置的 App,成功后 Stop 旧 App,存在短暂重叠期。由于 LoginFailExit 始终为 false,新实例在遇到端口冲突时会自动重试,等旧实例停止后即可成功连接。