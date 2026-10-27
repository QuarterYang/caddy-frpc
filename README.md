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
            protocol tcp
            pool_count 5
            admin_addr 127.0.0.1
            admin_port 7400
            admin_user admin
            admin_password admin
        }

        server serverB {
            server_addr 5.6.7.8
            server_port 7000
            user aaa
            token bbb
            tcp_mux true
            heartbeat_interval 30
            heartbeat_timeout 90
        }

        proxy proxyA {
            server serverA
            type tcp
            local_ip 127.0.0.1
            local_port 1234
            remote_port 12345
            use_compression true
            bandwidth_limit 2MB
            bandwidth_limit_mode client
            health_check_type tcp
            health_check_interval_seconds 10
            health_check_max_failed 3
        }

        proxy proxyB {
            server serverB
            type tcp
            local_ip 127.0.0.1
            local_port 1234
            remote_port 12345
        }

        proxy webA {
            server serverA
            type http
            local_ip 127.0.0.1
            local_port 8080
            custom_domains app.example.com
            group web
            group_key anykey
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

基础连接:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `server_addr` | frps 服务器地址 | **必填** |
| `server_port` | frps 服务器端口 | 7000 |
| `user` | 用户名(proxy 名称会自动加 `user.` 前缀) | - |
| `client_id` | 客户端唯一标识 | 随机生成 |

认证:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `auth_method` | 认证方式:token/oidc | token |
| `token` | 认证 token | - |
| `oidc_client_id` | OIDC client ID | - |
| `oidc_client_secret` | OIDC client secret | - |
| `oidc_audience` | OIDC audience | - |
| `oidc_scope` | OIDC scope | - |
| `oidc_token_endpoint_url` | OIDC token endpoint | - |

传输:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `protocol` | 传输协议:tcp/kcp/quic/websocket/wss | tcp |
| `wire_protocol` | frpc/frps 内部通信协议版本:v1/v2 | v1 |
| `dial_server_timeout` | 连接 frps 超时(秒) | 10 |
| `dial_server_keepalive` | 连接保活间隔(秒),负数禁用 | 7200 |
| `connect_server_local_ip` | 连接 frps 时使用的本机 IP(仅 tcp/websocket) | - |
| `proxy_url` | 通过代理连接 frps,如 `http://user:pass@1.2.3.4:8080` | 读 `http_proxy` |
| `pool_count` | 预先与 frps 建立的连接数 | 1 |

多路复用与心跳:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `tcp_mux` | 启用 TCP 多路复用(true/false) | true |
| `tcp_mux_keepalive_interval` | 多路复用保活间隔(秒) | 30 |
| `heartbeat_interval` | 应用层心跳间隔(秒),tcp_mux 开启时无效 | 30 |
| `heartbeat_timeout` | 心跳超时(秒),tcp_mux 开启时无效 | 90 |

其他连接行为:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `dns_server` | 自定义 DNS 服务器 | 系统默认 |
| `nat_hole_stun_server` | NAT 打洞用的 STUN 服务器 | stun.easyvoip.com:3478 |
| `udp_packet_size` | UDP 包大小 | 1500 |
| `login_fail_exit` | 登录失败是否退出(true/false) | false |

TLS:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `tls_enable` | 启用 TLS(true/false) | true |
| `tls_disable_custom_first_byte` | 关闭 TLS 首字节混淆(true/false) | true |
| `tls_cert_file` | 客户端证书路径 | - |
| `tls_key_file` | 客户端私钥路径 | - |
| `tls_trusted_ca_file` | 可信 CA 证书路径 | - |
| `tls_server_name` | TLS SNI,默认与 `server_addr` 相同 | - |

QUIC(仅 `protocol quic`):

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `quic_keepalive_period` | QUIC 保活周期(秒) | 10 |
| `quic_max_idle_timeout` | QUIC 最大空闲超时(秒) | 30 |
| `quic_max_incoming_streams` | QUIC 最大流入流数 | 100000 |

管理接口(frpc 自带 Web UI / API):

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `admin_addr` | 监听地址 | 127.0.0.1 |
| `admin_port` | 监听端口,0 表示不启动 | 0 |
| `admin_user` | 登录用户名 | - |
| `admin_password` | 登录密码 | - |

其他:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `log_level` | 日志级别:trace/debug/info/warn/error | info |
| `meta` | 客户端元数据,格式:`meta <key> <value>` | - |

### proxy 子块指令

基础:

| 指令 | 说明 |
|------|------|
| `server` | 引用的 server 名称(**必填**) |
| `type` | 代理类型:tcp/udp/http/https/tcpmux/stcp/xtcp/sudp(默认 tcp) |
| `enabled` | 是否启用该隧道(true/false,默认 true) |
| `local_ip` | 本地 IP(默认 127.0.0.1) |
| `local_port` | 本地端口(**必填**) |
| `remote_port` | 远程端口(tcp/udp **必填**) |
| `meta` | 代理元数据,格式:`meta <key> <value>` |

域名与路由:

| 指令 | 说明 |
|------|------|
| `custom_domains` | 自定义域名(http/https/tcpmux,可多个) |
| `sub_domain` | 子域名(http/https/tcpmux) |
| `locations` | URL 路径前缀(http,可多个) |
| `http_user` | HTTP 基本认证用户名(http/tcpmux) |
| `http_password` | HTTP 基本认证密码(http/tcpmux) |
| `host_header_rewrite` | 重写 Host 头(http) |
| `request_header` | 设置请求头(http,格式:`request_header <key> <value>`) |
| `response_header` | 设置响应头(http,格式:`response_header <key> <value>`) |
| `route_by_http_user` | 按 HTTP 用户路由(http/tcpmux) |
| `multiplexer` | tcpmux 多路复用器:httpconnect/httpconnect2 |

通用传输:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `use_encryption` | 启用加密(true/false) | false |
| `use_compression` | 启用压缩(true/false) | false |
| `bandwidth_limit` | 带宽限速,如 `1MB`、`512KB` | 不限速 |
| `bandwidth_limit_mode` | 限速方向:client/server | client |
| `proxy_protocol_version` | PROXY Protocol 版本:v1/v2 | 自动 |

负载均衡与健康检查:

| 指令 | 说明 | 默认值 |
|------|------|--------|
| `group` | 负载均衡分组名 | - |
| `group_key` | 分组密钥 | - |
| `health_check_type` | 健康检查方式:tcp/http,空表示不检查 | - |
| `health_check_timeout_seconds` | 检查超时(秒) | 3 |
| `health_check_max_failed` | 允许的连续失败次数 | 1 |
| `health_check_interval_seconds` | 检查间隔(秒) | 10 |
| `health_check_path` | HTTP 检查路径 | - |
| `health_check_http_header` | 检查请求头,格式:`health_check_http_header <name> <value>` | - |

stcp / xtcp / sudp:

| 指令 | 说明 |
|------|------|
| `secret_key` | 加密密钥(stcp/xtcp/sudp **必填**) |
| `allow_users` | 允许的访问者用户列表(可多个) |
| `nat_traversal_disable_assisted_addrs` | 禁用本地网卡地址辅助打洞(xtcp,true/false) |

## 注意事项

- **依赖兼容性**:frp v0.69.1 与 Caddy v2.11.3 的第三方依赖可能存在版本冲突(尤其是 `quic-go`、`golang.org/x/net` 等)。构建时如遇到编译错误,运行 `go mod tidy`;若仍有问题,在 go.mod 中添加 `replace` 指令调整版本。frp v0.69.1 仍依赖其自维护的 yamux fork,go.mod 中已通过 `replace github.com/hashicorp/yamux => github.com/fatedier/yamux ...` 指向对应版本。
- **信号处理**:frp v0.69.1 的 `Run(ctx)` 不注册信号处理器、不调用 `os.Exit`,完全通过 context 取消实现优雅停止,不会干扰 Caddy 的信号处理。
- **配置热更新 (Reload)**:支持通过 `caddy reload` 重新加载 Caddyfile。Caddy 会先 Start 新配置的 App,成功后 Stop 旧 App,存在短暂重叠期。由于 LoginFailExit 始终为 false,新实例在遇到端口冲突时会自动重试,等旧实例停止后即可成功连接。