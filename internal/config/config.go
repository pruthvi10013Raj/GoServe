package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTPSPort       int
	TCPPort         int
	StaticDirectory string
	UploadDirectory string
	MaxUploadSize   int64
}

type yamlServerConfig struct {
	HTTPSPort       *int    `yaml:"https_port"`
	TCPPort         *int    `yaml:"tcp_port"`
	StaticDirectory *string `yaml:"static_directory"`
	UploadDirectory *string `yaml:"upload_directory"`
	MaxUploadSize   *int64  `yaml:"max_upload_size"`
}

func Default() Config {
	return Config{
		HTTPSPort:       8443,
		TCPPort:         9090,
		StaticDirectory: "./public",
		UploadDirectory: "./uploads",
		MaxUploadSize:   10 << 20,
	}
}

func Load(path string) (Config, error) {
	config := Default()
	if path == "" {
		return config, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var values yamlServerConfig
	if err := decoder.Decode(&values); err != nil {
		return Config{}, fmt.Errorf("decode config file: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("config file must contain only one YAML document")
		}
		return Config{}, fmt.Errorf("decode config file: %w", err)
	}

	if values.HTTPSPort != nil {
		config.HTTPSPort = *values.HTTPSPort
	}
	if values.TCPPort != nil {
		config.TCPPort = *values.TCPPort
	}
	if values.StaticDirectory != nil {
		config.StaticDirectory = *values.StaticDirectory
	}
	if values.UploadDirectory != nil {
		config.UploadDirectory = *values.UploadDirectory
	}
	if values.MaxUploadSize != nil {
		config.MaxUploadSize = *values.MaxUploadSize
	}

	return config, nil
}

func (config Config) Validate() error {
	if config.HTTPSPort < 1 || config.HTTPSPort > 65535 {
		return fmt.Errorf("HTTPS port must be between 1 and 65535")
	}
	if config.TCPPort < 1 || config.TCPPort > 65535 {
		return fmt.Errorf("TCP port must be between 1 and 65535")
	}
	if strings.TrimSpace(config.StaticDirectory) == "" {
		return fmt.Errorf("static directory must not be empty")
	}
	if strings.TrimSpace(config.UploadDirectory) == "" {
		return fmt.Errorf("upload directory must not be empty")
	}
	if config.MaxUploadSize <= 0 {
		return fmt.Errorf("maximum upload size must be greater than zero")
	}
	staticInfo, err := os.Stat(config.StaticDirectory)
	if err != nil {
		return fmt.Errorf("static directory %q: %w", config.StaticDirectory, err)
	}
	if !staticInfo.IsDir() {
		return fmt.Errorf("static directory %q is not a directory", config.StaticDirectory)
	}
	if err := validateUploadDirectoryPath(config.UploadDirectory); err != nil {
		return err
	}
	return nil
}

func validateUploadDirectoryPath(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("upload directory path %q has a non-directory parent %q", path, current)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("upload directory path %q: %w", path, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("upload directory path %q has no existing parent directory", path)
		}
		current = parent
	}
}

func ApplyOverrides(config *Config, overrides Config, explicit map[string]bool) error {
	if explicit["https-port"] && explicit["port"] {
		return fmt.Errorf("use only one of -https-port and -port")
	}
	if explicit["static-dir"] && explicit["dir"] {
		return fmt.Errorf("use only one of -static-dir and -dir")
	}

	if explicit["https-port"] {
		config.HTTPSPort = overrides.HTTPSPort
	} else if explicit["port"] {
		config.HTTPSPort = overrides.HTTPSPort
	}
	if explicit["tcp-port"] {
		config.TCPPort = overrides.TCPPort
	}
	if explicit["static-dir"] || explicit["dir"] {
		config.StaticDirectory = overrides.StaticDirectory
	}
	if explicit["upload-dir"] {
		config.UploadDirectory = overrides.UploadDirectory
	}
	if explicit["max-upload-size"] {
		config.MaxUploadSize = overrides.MaxUploadSize
	}
	return nil
}

func Parse(args []string) (Config, error) {
	defaults := Default()
	flags := flag.NewFlagSet("goserve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	configPath := flags.String("config", "", "Path to YAML configuration file")
	httpsPort := defaults.HTTPSPort
	tcpPort := defaults.TCPPort
	staticDirectory := defaults.StaticDirectory
	uploadDirectory := defaults.UploadDirectory
	maxUploadSize := defaults.MaxUploadSize
	flags.IntVar(&httpsPort, "https-port", defaults.HTTPSPort, "HTTPS server port")
	flags.IntVar(&httpsPort, "port", defaults.HTTPSPort, "HTTPS server port (legacy alias)")
	flags.IntVar(&tcpPort, "tcp-port", defaults.TCPPort, "TCP server port")
	flags.StringVar(&staticDirectory, "static-dir", defaults.StaticDirectory, "Static files directory")
	flags.StringVar(&staticDirectory, "dir", defaults.StaticDirectory, "Static files directory (legacy alias)")
	flags.StringVar(&uploadDirectory, "upload-dir", defaults.UploadDirectory, "Upload files directory")
	flags.Int64Var(&maxUploadSize, "max-upload-size", defaults.MaxUploadSize, "Maximum upload size in bytes")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}

	config, err := Load(*configPath)
	if err != nil {
		return Config{}, err
	}
	explicitFlags := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) {
		explicitFlags[f.Name] = true
	})
	overrides := Config{
		HTTPSPort:       httpsPort,
		TCPPort:         tcpPort,
		StaticDirectory: staticDirectory,
		UploadDirectory: uploadDirectory,
		MaxUploadSize:   maxUploadSize,
	}
	if err := ApplyOverrides(&config, overrides, explicitFlags); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}
