<div align="center">

# One API Future

[English](./README.md) | **简体中文**

_✨ 基于 [songquanpeng/one-api](https://github.com/songquanpeng/one-api) 二次开发的公司内部自用版 ✨_

</div>

> **关于本项目**：本项目是 [songquanpeng/one-api](https://github.com/songquanpeng/one-api)（MIT License）的定制分支，面向**公司内部自用**场景做了功能增删与计费简化，遵循原项目 MIT 协议开源。原版 README 见 [README-ORIGINAL.md](./README-ORIGINAL.md)。
>
> 基线版本：one-api main 分支快照（v0.0.0）。技术栈不变：Go (Gin + GORM) 后端 + React (Semantic UI) 前端，前端随 Go 二进制 embed 发布。

---

## 新增功能

### 1. 渠道定时启用 / 禁用
- 每个渠道可配置「每日禁用时间 ~ 每日启用时间」，后台每 30 秒自动切换状态
- **支持跨零点窗口**（如 23:00 禁用、06:00 启用），适合只在部分时段可用的上游
- 只影响手动启用/禁用状态，**不会覆盖**渠道测试失败导致的自动禁用（状态 3），与故障检测机制互不干扰
- 配置入口：渠道管理页每行「定时」按钮；启用后渠道名称旁显示时间窗徽标

### 2. 全局模型别名（统一模型名）
- 运营设置 →「模型别名 / 统一模型名」，JSON 配置：`{"对外的统一模型名": "实际后端模型"}`
- 用户（AI agent）始终使用统一模型名；**后端模型更换时只需改映射，用户侧零改动**
- 网关在渠道选择前解析别名，计费、日志、重试均按真实模型进行；别名可叠加渠道级「模型重定向」（渠道级优先）
- `GET /v1/models` 会把可用真实模型对应的别名一并列出，便于 agent 自动发现

### 3. OpenAI Responses API 对接（`POST /v1/responses`）
- 以**透传**方式对接源渠道的 Responses API（不做协议转换），支持流式 SSE（逐块转发实时 Flush）与非流式
- 渠道编辑页勾选「支持 Responses API」后才会被使用；未勾选的渠道返回明确的 `responses_not_supported` 错误，而不是上游 404
- 请求中的模型同样应用全局别名/映射后转发
- **计入用量统计**：从响应（非流式 JSON `usage` 或流式 `response.completed` 事件）提取 token 数，按统一公式记账（quota = prompt + completion × completionRatio），写入消费日志并更新用户/渠道用量

### 4. 内部自用计费简化
- 模型倍率、补全倍率、分组倍率**恒为 1**，quota 直接等于 token 数，日志显示 `倍率：1.00 × 1.00 × 1.00`
- 用户额度视为**无限**：不再检查/扣减用户额度，用量通过消费日志与用户请求统计查看
- 令牌额度仍可按需设置（用于用量管控，超限拒绝请求），**新建令牌默认无限额度**

## 移除内容（内部自用不需要）

- 顶栏「兑换」「充值」导航入口及 `/topup` 充值中心页面
- 运营设置中的「额度设置」「倍率设置」「充值链接」「额度提醒阈值」配置卡
- 额度不足提醒邮件（用户额度已视为无限）

> 兑换码管理页面保留在 `/redemption` 路由（顶栏无入口），如需要可自行关闭。

## 部署方法

### 方式一：二进制 + 交互式安装向导（推荐，最简单）

Linux amd64 静态二进制（musl 静态链接，无任何依赖）：

```bash
mkdir -p /opt/one-api && tar xzf one-api-deploy.tar.gz -C /opt/one-api && cd /opt/one-api
chmod +x install.sh start.sh
sudo ./install.sh
```

向导会依次询问（一路回车 = 全默认）：
1. **数据库**：SQLite（回车默认，零配置）或 MySQL（逐项输入地址/端口/库名/用户名/密码，密码不能含 `@ : / 空格`）
2. **对外端口**：默认 3000
3. **是否开机自启**：默认 y，自动注册 systemd 服务

结束时打印访问地址、初始账号（`root` / `123456`，请立即修改）。修改配置重跑 `sudo ./install.sh` 即可。

### 方式二：Docker

```bash
docker build -t one-api-future .
docker run -d --name one-api-future --restart always -p 3000:3000 \
  -e TZ=Asia/Shanghai -v /your/data/path:/data \
  one-api-future
# 使用 MySQL 时追加：
#   -e SQL_DSN="user:password@tcp(mysql-host:3306)/one-api"
```

### 方式三：源码编译

```bash
# 前端（产物自动移动到 web/build/default，随 Go 一起 embed）
cd web/default && npm install && DISABLE_ESLINT_PLUGIN=true npm run build && cd ../..

# 后端（SQLite 需要 CGO）
CGO_ENABLED=1 go build -o one-api .
# 交叉编译 Linux 静态二进制示例（使用 zig 作为 C 编译器）：
# CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" go build -o one-api-linux-amd64 .
```

运行所需环境变量：

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `PORT` | 服务端口 | `3000` |
| `SESSION_SECRET` | 会话密钥（生产必须设置随机值） | 随机 |
| `SQL_DSN` | MySQL 连接串 `user:pass@tcp(host:3306)/db`，不设则用 SQLite | - |
| `LOG_SQL_DSN` | 日志表独立数据库（可选） | 同主库 |
| `SQLITE_PATH` | SQLite 文件路径 | `./one-api.db` |
| `CHANNEL_TEST_FREQUENCY` | 渠道自动测试间隔（秒），不设则关闭 | - |
| `SQLITE_PATH` / `TIKTOKEN_CACHE_DIR` 等 | 其余见 [README-ORIGINAL.md](./README-ORIGINAL.md) 环境变量一节 | - |

> 提示：SQLite 需要 CGO。首次启动需联网下载 tiktoken 分词表（约 1-2 分钟），离线环境请参考原版 README 设置 `TIKTOKEN_CACHE_DIR`。

## 主要改动文件

| 功能 | 文件 |
| --- | --- |
| 渠道定时启停 | `controller/channel_schedule.go`、`model/channel.go`、`web/.../ChannelsTable.js` |
| 全局模型别名 | `model/alias.go`、`middleware/distributor.go`、`model/option.go`、`web/.../OperationSetting.js` |
| Responses API 透传+计费 | `relay/controller/responses.go`、`router/relay.go` |
| 计费简化 | `relay/billing/ratio/{model,group}.go`、`model/token.go` |
| UI 精简 | `web/.../Header.js`、`OperationSetting.js`、`App.js`、`Token/EditToken.js` |

## License

MIT © 原项目作者 [songquanpeng](https://github.com/songquanpeng/one-api) 及本项目贡献者。本项目的所有修改同样以 MIT 协议发布，使用请遵循原项目 [LICENSE](./LICENSE)。
