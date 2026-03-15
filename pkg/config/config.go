package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config holds all TrustForge runtime configuration
type Config struct {
	Server     ServerConfig
	Database   DatabaseConfig
	Firecracker FirecrackerConfig
	Worker     WorkerConfig
	LLM        LLMConfig
	Storage    StorageConfig
}

type ServerConfig struct {
	GRPCPort    int           `mapstructure:"grpc_port"`
	RESTPort    int           `mapstructure:"rest_port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	MaxConns int    `mapstructure:"max_conns"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s pool_max_conns=%d",
		d.Host, d.Port, d.Name, d.User, d.Password, d.MaxConns)
}

type FirecrackerConfig struct {
	// Path to the firecracker binary
	BinaryPath string `mapstructure:"binary_path"`
	// Path to the jailer binary
	JailerPath string `mapstructure:"jailer_path"`
	// Directory where VM socket files are created
	SocketDir string `mapstructure:"socket_dir"`
	// Directory where VM disk images are stored
	ImageDir string `mapstructure:"image_dir"`
	// Path to the read-only base image
	BaseImagePath string `mapstructure:"base_image_path"`
	// Path to directory for ephemeral task disks
	TaskDiskDir string `mapstructure:"task_disk_dir"`
	// Path to snapshot storage
	SnapshotDir string `mapstructure:"snapshot_dir"`
	// Kernel image path
	KernelPath string `mapstructure:"kernel_path"`
	// vCPU count per VM
	VCPUCount int64 `mapstructure:"vcpu_count"`
	// Memory per VM in MiB
	MemSizeMiB int64 `mapstructure:"mem_size_mib"`
	// Max wall-clock time for a verifier execution
	ExecutionTimeout time.Duration `mapstructure:"execution_timeout"`
	// UID/GID for the jailer process
	JailerUID int `mapstructure:"jailer_uid"`
	JailerGID int `mapstructure:"jailer_gid"`
}

type WorkerConfig struct {
	// Maximum number of concurrent VMs
	PoolSize int `mapstructure:"pool_size"`
	// Number of jobs to buffer in the queue
	QueueDepth int `mapstructure:"queue_depth"`
	// How many snapshots to keep warm
	WarmSnapshotCount int `mapstructure:"warm_snapshot_count"`
}

type LLMConfig struct {
	// Anthropic API key for red-team analysis
	APIKey      string  `mapstructure:"api_key"`
	Model       string  `mapstructure:"model"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	// Risk threshold above which a submission is rejected
	RiskThreshold float64 `mapstructure:"risk_threshold"`
}

type StorageConfig struct {
	// Task disk size in bytes (default 10MB)
	TaskDiskSize int64 `mapstructure:"task_disk_size"`
}

// Load reads configuration from file and environment variables
func Load(path string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("server.grpc_port", 50051)
	v.SetDefault("server.rest_port", 8080)
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.max_conns", 20)
	v.SetDefault("firecracker.vcpu_count", 1)
	v.SetDefault("firecracker.mem_size_mib", 128)
	v.SetDefault("firecracker.execution_timeout", "30s")
	v.SetDefault("firecracker.jailer_uid", 1001)
	v.SetDefault("firecracker.jailer_gid", 1001)
	v.SetDefault("worker.pool_size", 50)
	v.SetDefault("worker.queue_depth", 500)
	v.SetDefault("worker.warm_snapshot_count", 5)
	v.SetDefault("llm.model", "claude-sonnet-4-20250514")
	v.SetDefault("llm.max_tokens", 2048)
	v.SetDefault("llm.risk_threshold", 0.7)
	v.SetDefault("storage.task_disk_size", 10*1024*1024) // 10MB

	v.SetConfigFile(path)
	v.AutomaticEnv()
	v.SetEnvPrefix("TRUSTFORGE")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return &cfg, nil
}
