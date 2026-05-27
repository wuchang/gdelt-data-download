package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/wuchang/gdelt-data-download/config"
	"github.com/wuchang/gdelt-data-download/downloader"
	"github.com/wuchang/gdelt-data-download/log"
	"github.com/wuchang/gdelt-data-download/resolver"

	"github.com/spf13/cobra"
)

type flags struct {
	startDate     string
	endDate       string
	table         string
	output        string
	flat          bool
	translation   bool
	watch         bool
	dataDir       string
	concurrency   int
	bucket        string
	zipPrefix     string
	minioEndpoint string
	minioAccessKey string
	minioSecretKey string
	minioSecure   string
}

var f flags

var rootCmd = &cobra.Command{
	Use:   "gdelt-data-download",
	Short: "GDELT 数据下载工具",
	Long: `从 data.gdeltproject.org 下载 GDELT v2 数据文件。
支持按日期范围、表类型下载，以及实时监测模式。

输出可保存到本地目录或直接上传到 MinIO。`,
	SilenceUsage:  false,
	SilenceErrors: true,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
	RunE: run,
}

func init() {
	// Required
	rootCmd.Flags().SortFlags = false
	rootCmd.Flags().StringVar(&f.startDate, "start-date", "", "起始日期 (YYYYMMDD)")

	// Optional
	rootCmd.Flags().StringVar(&f.endDate, "end-date", "", "结束日期 (YYYYMMDD)，不指定则一直下载到最新")
	rootCmd.Flags().StringVar(&f.table, "table", "export,gkg,mentions", "数据表，逗号分隔多个")
	rootCmd.Flags().StringVar(&f.output, "output", "local", "输出目标: local 或 minio")
	rootCmd.Flags().BoolVar(&f.flat, "flat", false, "使用扁平目录结构（不使用 year=YYYY/month=MM/day=DD 层级）")
	rootCmd.Flags().BoolVar(&f.translation, "translation", false, "同时下载翻译版本")
	rootCmd.Flags().BoolVar(&f.watch, "watch", false, "下载完成后进入实时监测模式")
	rootCmd.Flags().StringVar(&f.dataDir, "dir", "./data", "本地数据目录")
	rootCmd.Flags().IntVar(&f.concurrency, "concurrency", 5, "下载并发数")

	// MinIO
	rootCmd.Flags().StringVar(&f.bucket, "bucket", "", "MinIO Bucket 名称")
	rootCmd.Flags().StringVar(&f.zipPrefix, "zip-prefix", config.DefaultZipPrefix, "MinIO 对象前缀")
	rootCmd.Flags().StringVar(&f.minioEndpoint, "minio-endpoint", "", "MinIO 服务器地址")
	rootCmd.Flags().StringVar(&f.minioAccessKey, "minio-access-key", "", "MinIO Access Key")
	rootCmd.Flags().StringVar(&f.minioSecretKey, "minio-secret-key", "", "MinIO Secret Key")
	rootCmd.Flags().StringVar(&f.minioSecure, "minio-secure", "", "MinIO 使用 HTTPS (true/false)")

	rootCmd.MarkFlagRequired("start-date")
}

func run(cmd *cobra.Command, args []string) error {
	// Parse log level
	logger := log.NewLogger(slog.LevelInfo)

	// Load .env file (silent if not found)
	config.LoadEnv()

	// Validate
	cfg := &config.DownloadConfig{
		StartDate:   f.startDate,
		EndDate:     f.endDate,
		Table:       f.table,
		Output:      f.output,
		Flat:        f.flat,
		Translation: f.translation,
		Watch:       f.watch,
		DataDir:     f.dataDir,
		Concurrency: f.concurrency,
		ZipPrefix:   f.zipPrefix,
	}
	if err := config.ValidateDownloadFlags(cfg); err != nil {
		return fmt.Errorf("参数错误: %w", err)
	}

	// Build download options
	dlCfg := &downloader.DownloadOptions{
		StartDate:   f.startDate,
		EndDate:     f.endDate,
		Table:       f.table,
		Output:      f.output,
		Flat:        f.flat,
		Translation: f.translation,
		Watch:       f.watch,
		DataDir:     f.dataDir,
		Concurrency: f.concurrency,
		ZipPrefix:   f.zipPrefix,
		Logger:      logger,
	}

	// Resolve MinIO config (env -> CLI override)
	minioEndpoint, minioAK, minioSK, minioBucket, minioSecure := config.MinioEnvConfig()
	var securePtr *bool
	if cmd.Flag("minio-secure").Changed {
		v, err := strconv.ParseBool(f.minioSecure)
		if err == nil {
			securePtr = &v
		}
	}
	dlCfg.MinioEndpoint, dlCfg.MinioAccessKey, dlCfg.MinioSecretKey, dlCfg.MinioBucket, dlCfg.MinioSecure =
		config.MergeMinioFlags(minioEndpoint, minioAK, minioSK, minioBucket, minioSecure,
			f.minioEndpoint, f.minioAccessKey, f.minioSecretKey, f.bucket, securePtr)

	logger.Info("GDELT 数据下载工具启动")
	logger.Info("配置信息",
		"start_date", dlCfg.StartDate,
		"end_date", func() string {
			if dlCfg.EndDate == "" {
				return "最新"
			}
			return dlCfg.EndDate
		}(),
		"table", dlCfg.Table,
		"output", dlCfg.Output,
		"flat", dlCfg.Flat,
		"translation", dlCfg.Translation,
		"watch", dlCfg.Watch,
		"concurrency", dlCfg.Concurrency,
	)

	// Context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("接收到信号，正在退出...", "signal", sig)
		cancel()
	}()

	// Resolve fastest GDELT server
	logger.Info("正在探测最快 GDELT 服务器...")
	result, err := resolver.ResolveFastestBaseURL(ctx)
	if err != nil {
		logger.Warn("探测最快服务器失败，使用默认地址", "error", err)
		dlCfg.BaseURL = "http://data.gdeltproject.org/gdeltv2"
		dlCfg.HostHeader = ""
	} else {
		dlCfg.BaseURL = result.BaseURL
		dlCfg.HostHeader = result.HostHeader
		logger.Info("最快服务器", "url", result.BaseURL)
	}

	// Batch download
	if err := downloader.BatchDownload(ctx, dlCfg); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}

	// Watch mode
	if f.watch && ctx.Err() == nil {
		if err := downloader.WatchMode(ctx, dlCfg); err != nil {
			return fmt.Errorf("监测模式失败: %w", err)
		}
	}

	logger.Info("程序正常结束")
	return nil
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
