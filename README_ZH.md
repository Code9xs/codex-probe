<p align="center">
  <img width="256" height="256" alt="Gemini_Generated_Image_6p04mm6p04mm6p04" src="https://github.com/user-attachments/assets/512cd1d8-93af-40dc-acde-6e7daa339493" />
</p>
<div align="center">

**Codex 凭证管理与接口诊断命令行工具**

[![Release](https://img.shields.io/github/v/release/Code9xs/codex-probe?style=flat-square)](../../releases)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey?style=flat-square)]()

[English](README.md) · [中文](README_ZH.md)

</div>

`codex-probe` 是一个单文件 CLI，用来集中管理 Codex token 的登录、续期、额度查询、接口测试、凭证格式转换，以及按需同步到 Supabase。

## 项目简介

<p align="center">
  <img width="80%" alt="logo" src="https://github.com/user-attachments/assets/9bd3c05b-5274-4bb3-b504-c18a52891181" />
</p>

这个工具把一堆偏底层的参数，整理成一套更顺手的日常 token 管理流程。

功能特性：

- `--login`：走一遍网页登录流程，把 token 保存到本地 JSON
- `--renew`：给单个 token 文件或整个目录批量续期
- `--status`：查询现有 token 的额度窗口
- `--apitest`：用最小请求探测模型接口能不能用
- `--convert`：将凭证文件转换为 **sub2api** 或 **CPA** 格式
- `--serve`：启动 **Web 可视化界面** 和 REST API 服务
- `--sync`：把本地 token 加密后同步到 Supabase，也能从云端拉回本地
- `--output`：把 `--status` 和 `--apitest` 结果导出成 CSV
- `--proxy`：手动指定代理，或者直接走系统代理检测

详细说明见：

- [docs/advanced_ZH.md](docs/advanced_ZH.md)
- [docs/supabase_ZH.md](docs/supabase_ZH.md)

## 安装

可直接从 [Releases](../../releases) 下载预编译文件。

| 平台 | 文件名 |
|---|---|
| Linux x86-64 | `codex-probe-linux-amd64` |
| Linux ARM64 | `codex-probe-linux-arm64` |
| macOS Intel | `codex-probe-darwin-amd64` |
| macOS Apple Silicon | `codex-probe-darwin-arm64` |
| Windows x86-64 | `codex-probe-windows-amd64.exe` |

也可以从源码编译：

```bash
git clone https://github.com/Code9xs/codex-probe
cd codex-probe
go build -o codex-probe ./cmd/codex-probe/
```

跨平台一键打包（编译所有平台）：

```bash
# 编译所有平台 → ./dist/
./build.sh

# 仅编译当前平台 → ./codex-probe
./build.sh current

# 编译指定平台
./build.sh darwin-arm64
```

macOS 首次运行如果被系统拦截，可先去掉隔离属性：

```bash
xattr -d com.apple.quarantine codex-probe-darwin-*
chmod +x codex-probe-darwin-*
./codex-probe-darwin-*
```

## 快速上手

首次运行前，先复制示例配置：

```bash
cp ./config.example.json ./config.json
```

常用命令：

```bash
# 登录并保存 token 文件
codex-probe --login -o ./tokens/

# 就地续期单个 token 文件
codex-probe --renew ./tokens/me.json

# 查看额度
codex-probe --status ./tokens/me.json

# 测试模型可用性
codex-probe --apitest ./tokens/

# 与 Supabase 同步本地 token 文件
codex-probe --sync

# 启动 Web 可视化界面
codex-probe --serve --port 8080 ./tokens/
```

## 凭证格式转换

`--convert` 命令可以在不同凭证格式之间转换：

| 格式 | 说明 |
|---|---|
| `sub2api` | 标准 [sub2api](https://github.com/AIDotNet/sub2api) 导入 JSON，包含 `accounts[]`、`model_mapping`、`concurrency` 等字段 |
| `cpa` | 行式 JSON 存档格式 — 每行一条完整的凭证 JSON |

### 支持的输入类型

| 输入 | 读取方式 |
|---|---|
| `./tokens/` | 目录 — 读取目录下所有 `*.json` 凭证文件 |
| `./tokens/me.json` | 单个 codex-probe 凭证 JSON 文件 |
| `./cpa.txt` | 行式文件 — 每行一条完整凭证 JSON（`.txt` 后缀） |

### 使用示例

```bash
# 目录 → sub2api 格式 JSON
codex-probe --convert --format sub2api ./tokens/

# 单文件 → sub2api 格式 JSON
codex-probe --convert --format sub2api ./tokens/me.json

# CPA 行式文件 → sub2api 格式 JSON
codex-probe --convert --format sub2api ./cpa.txt

# 目录 → CPA 行式文件
codex-probe --convert --format cpa ./tokens/

# 自定义输出目录
codex-probe --convert --format sub2api --output ./my_output/ ./tokens/

# 交互模式 — 省略 --format 在运行时选择格式
codex-probe --convert ./tokens/
```

### 完整工作流

```
codex-probe --login -o ./tokens/       ← 第1步：获取凭证
                 ↓
codex-probe --convert --format sub2api ./tokens/  ← 第2步：转换格式
                 ↓
           sub2api-5-20260512-215046.json          ← 可直接导入
```

## Web 可视化界面

`--serve` 命令启动内置的 Web Dashboard 和 REST API 服务，提供图形化的凭证管理体验：

```bash
# 启动 Web 服务（默认端口 18152）
codex-probe --serve ./tokens/

# 自定义端口
codex-probe --serve --port 8080 ./tokens/

# 不预载凭证，启动空白 Dashboard
codex-probe --serve
```

启动后打开浏览器访问 `http://localhost:18152` 即可使用。

### Dashboard 功能

- 📊 **统计面板** — 凭证总数 / 有效数 / 即将过期数
- 📁 **拖放上传** — 支持 Codex `.json`、CPA `.txt`、Sub2API `.json`（自动识别格式，保存为 codex 格式）
- 📋 **凭证列表** — 分页、筛选、全选/本页选、批量操作
- ✅ **状态检测** — 一键检测所有凭证可用性（标记 401/403 失效账号）
- 🔄 **格式转换** — 选中或全部转换为 sub2api / CPA 并下载
- 📈 **额度查询** — 可视化展示 5 小时 / 周额度环形图
- 🔐 **OAuth 登录** — 直接从网页登录新账号
- 🗑️ **增删管理** — 逐条删除或一键清空

### REST API

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/health` | 健康检查 |
| `GET` | `/api/keys` | 获取凭证列表（含状态字段） |
| `POST` | `/api/keys/upload` | 上传凭证文件 (codex/CPA/sub2api) |
| `POST` | `/api/keys/batch-check` | 批量检测凭证可用性 |
| `GET` | `/api/keys/{idx}` | 获取单条凭证详情 |
| `DELETE` | `/api/keys/{idx}` | 删除单条凭证 |
| `DELETE` | `/api/keys` | 清空所有凭证 |
| `GET` | `/api/keys/{idx}/status` | 查询额度 |
| `POST` | `/api/keys/{idx}/renew` | 刷新凭证 |
| `POST` | `/api/convert` | 格式转换 (body: `{format, indices}`) |
| `GET` | `/api/login` | OAuth 登录流程 |

## 本地配置

默认会读取可执行文件同级的 `config.json`。建议直接从 [config.example.json](config.example.json) 开始。

```json
{
  "renew_before_expiry_days": 3,
  "sync_url": "https://<project>.supabase.co",
  "sync_api_key": "<publishable-key>",
  "sync_aes_gcm_key": "<64-char-hex>",
  "sync_dir": "./tokens"
}
```

- `renew_before_expiry_days`：距离过期多少天内，工具会认为该续期了
- `sync_url`：Supabase 项目地址
- `sync_api_key`：Supabase publishable key
- `sync_aes_gcm_key`：本地 AES-256-GCM 密钥，可用 `openssl rand -hex 32` 生成
- `sync_dir`：`--sync` 使用的本地 token 目录

Supabase 用 Free Plan 就够用了。

如果你想看 Supabase 控制台配置、本地配置字段详解、续期机制、代理检测、CSV 格式和工作原理，请看：

- [docs/advanced_ZH.md](docs/advanced_ZH.md)
- [docs/supabase_ZH.md](docs/supabase_ZH.md)
- [supabase.sql](supabase.sql)

## 打包脚本

`build.sh` 脚本支持跨平台一键打包：

```bash
./build.sh              # 编译所有平台 → ./dist/
./build.sh current      # 仅编译当前平台
./build.sh clean        # 清理编译产物
./build.sh darwin-arm64 # 编译指定平台
VERSION=v1.2.0 ./build.sh  # 指定版本号
```

编译产物输出到 `./dist/` 目录，附带 SHA-256 校验和。

## License

MIT

## Community

[![LinuxDO](https://img.shields.io/badge/社区-Linux.do-blue?style=flat-square)](https://linux.do/)

欢迎前往 [linux.do](https://linux.do/) 交流讨论。
