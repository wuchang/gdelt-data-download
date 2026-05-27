# gdelt-data-download

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

从 [GDELT Project](https://www.gdeltproject.org/) 官方服务器下载 GDELT v2 数据文件的命令行工具。

支持按日期范围、数据表类型批量下载，以及实时监测模式。输出可保存到本地目录或直接上传到 MinIO。

## 特点

- **自动优选最快 IP**：启动时对所有 GDELT 服务器 IP 并发 TCP 竞速，选择延迟最低的节点直连下载，速度优于默认 DNS 解析
- **断点续传**：已下载的文件自动跳过，中断后重跑不浪费流量
- **实时监测**：`--watch` 模式持续轮询，自动拉取最新数据
- **多目标输出**：本地目录或 MinIO/S3 兼容存储
- **零运行时依赖**：静态编译的单一二进制文件

## 免责声明

本工具仅供个人学习与研究使用，严禁用于任何非法用途。

使用者应自行遵守 [GDELT Project](https://www.gdeltproject.org/) 的服务条款及数据使用政策。下载数据的使用、存储与分发须符合相关法律法规。因违规使用产生的任何法律责任由使用者自行承担，项目作者不对此负责。

## 环境要求

- [Go](https://go.dev/dl/) 1.26 或更高版本（从源码编译时需要）
- 预编译二进制文件无需任何运行时依赖

## 安装

### 预编译二进制

从 [Releases](https://github.com/wuchang/gdelt-data-download/releases) 页面下载对应平台的二进制文件即可直接使用。

### go install

```bash
go install github.com/wuchang/gdelt-data-download@latest
```

### 手动编译

```bash
git clone https://github.com/wuchang/gdelt-data-download.git
cd gdelt-data-download
go build -o gdelt-data-download .
```

#### 交叉编译

```bash
# Linux (amd64)
GOOS=linux GOARCH=amd64 go build -o gdelt-data-download-linux-amd64 .

# macOS (amd64)
GOOS=darwin GOARCH=amd64 go build -o gdelt-data-download-darwin-amd64 .

# macOS (arm64 / Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o gdelt-data-download-darwin-arm64 .

# Windows (amd64)
GOOS=windows GOARCH=amd64 go build -o gdelt-data-download.exe .
```

## 使用

```bash
# 下载 2026 年 5 月 1 日 export 表到本地
gdelt-data-download --start-date 20260501 --table export

# 下载日期范围内所有表
gdelt-data-download --start-date 20260501 --end-date 20260502

# 下载到 MinIO
gdelt-data-download --start-date 20260501 --output minio

# 先批量下载，完成后进入实时监测（每 5 分钟轮询新数据）
gdelt-data-download --start-date 20260501 --watch

# 包含翻译版本
gdelt-data-download --start-date 20260501 --translation

# 扁平目录（不使用 Hive 分区结构）
gdelt-data-download --start-date 20260501 --flat
```

### 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--start-date` | 必填 | 起始日期 YYYYMMDD |
| `--end-date` | 空 | 结束日期，不填则一直下载到最新 |
| `--table` | `export,gkg,mentions` | 数据表，逗号分隔多个 |
| `--output` | `local` | 输出目标：`local` 或 `minio` |
| `--flat` | `false` | 扁平目录，不使用 `year=YYYY/month=MM/day=DD` 层级 |
| `--translation` | `false` | 同时下载翻译版本 |
| `--watch` | `false` | 下载完成后进入实时监测模式 |
| `--dir` | `./data` | 本地数据目录 |
| `--concurrency` | `5` | 下载并发数 |
| `--bucket` | | MinIO bucket 名称 |
| `--zip-prefix` | `gdelt-zip` | MinIO 对象前缀 |

### MinIO 配置

优先级：CLI 参数 > `.env` 文件 > 环境变量

| 环境变量 | 说明 |
|----------|------|
| `MINIO_ENDPOINT` | MinIO 地址 |
| `MINIO_ACCESS_KEY` | Access Key |
| `MINIO_SECRET_KEY` | Secret Key |
| `MINIO_BUCKET` | 默认 bucket |

### 实时监测

`--watch` 模式每 5 分钟轮询 GDELT 服务器，自动下载缺失的 15 分钟时间片。
通过扫描本地目录或 MinIO bucket 判断已下载的文件，不会重复下载。

## 数据结构

### 本地目录（默认）

```
./data/
├── export/
│   └── year=2026/month=05/day=25/
│       ├── 20260525000000.export.CSV.zip
│       ├── 20260525001500.export.CSV.zip
│       └── ...
├── gkg/
│   └── year=2026/month=05/day=25/
│       └── ...
└── mentions/
    └── year=2026/month=05/day=25/
        └── ...
```

`--flat` 模式下不使用 `year=YYYY/month=MM/day=DD` 层级：

```
./data/export/20260525000000.export.CSV.zip
```

### MinIO

默认前缀 `gdelt-zip/`，目录结构与本地一致。

## GDELT 数据表

| 表名 | 内容 | 更新频率 |
|------|------|----------|
| `export` | GDELT 事件数据 (CSV) | 每 15 分钟 |
| `gkg` | Global Knowledge Graph | 每 15 分钟 |
| `mentions` | 事件提及 | 每 15 分钟 |

## 技术细节

- 自动探测最快的 GDELT 服务器 IP（TCP 并发竞速）
- 断点续传：已存在的文件自动跳过（检查文件大小）
- 原子写入：先写 `.tmp`，完成后重命名
- 重试机制：失败最多重试 3 次，指数退避
- 并发控制：channel-based worker pool
- 彩色日志输出
- 零运行时依赖（静态编译）

## 贡献

欢迎提交 Issue 和 Pull Request。

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/xxx`)
3. 提交变更 (`git commit -m 'Add xxx'`)
4. 推送到分支 (`git push origin feature/xxx`)
5. 创建 Pull Request

## Changelog

详见 [CHANGELOG.md](CHANGELOG.md)。

## License

[MIT](LICENSE)
