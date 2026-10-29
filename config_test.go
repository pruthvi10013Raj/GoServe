package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultServerConfig(t *testing.T) {
	config := defaultServerConfig()
	if err := config.validate(); err != nil {
		t.Fatalf("default config is invalid: %v", err)
	}
	if config.HTTPSPort != 8443 || config.TCPPort != 9090 {
		t.Errorf("default ports = (%d, %d), want (8443, 9090)", config.HTTPSPort, config.TCPPort)
	}
	if config.StaticDirectory != "./public" || config.UploadDirectory != "./uploads" {
		t.Errorf("default directories = (%q, %q), want (./public, ./uploads)", config.StaticDirectory, config.UploadDirectory)
	}
	if config.MaxUploadSize != 10<<20 {
		t.Errorf("default upload limit = %d, want %d", config.MaxUploadSize, 10<<20)
	}
}

func TestLoadServerConfigOverlaysYAMLOnDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("https_port: 9443\nupload_directory: ./incoming\nmax_upload_size: 2048\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	config, err := loadServerConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.HTTPSPort != 9443 || config.TCPPort != 9090 {
		t.Errorf("ports = (%d, %d), want (9443, 9090)", config.HTTPSPort, config.TCPPort)
	}
	if config.StaticDirectory != "./public" || config.UploadDirectory != "./incoming" {
		t.Errorf("directories = (%q, %q), want (./public, ./incoming)", config.StaticDirectory, config.UploadDirectory)
	}
	if config.MaxUploadSize != 2048 {
		t.Errorf("upload limit = %d, want 2048", config.MaxUploadSize)
	}
}

func TestApplyConfigOverridesPrecedence(t *testing.T) {
	config := serverConfig{
		HTTPSPort:       9443,
		TCPPort:         9191,
		StaticDirectory: "./yaml-public",
		UploadDirectory: "./yaml-uploads",
		MaxUploadSize:   2048,
	}
	overrides := serverConfig{
		HTTPSPort:       10443,
		TCPPort:         9292,
		StaticDirectory: "./cli-public",
		UploadDirectory: "./cli-uploads",
		MaxUploadSize:   4096,
	}
	explicit := map[string]bool{
		"https-port":      true,
		"upload-dir":      true,
		"max-upload-size": true,
	}
	if err := applyConfigOverrides(&config, overrides, explicit); err != nil {
		t.Fatal(err)
	}
	if config.HTTPSPort != 10443 || config.TCPPort != 9191 {
		t.Errorf("ports after overrides = (%d, %d), want (10443, 9191)", config.HTTPSPort, config.TCPPort)
	}
	if config.StaticDirectory != "./yaml-public" || config.UploadDirectory != "./cli-uploads" {
		t.Errorf("directories after overrides = (%q, %q), want (./yaml-public, ./cli-uploads)", config.StaticDirectory, config.UploadDirectory)
	}
	if config.MaxUploadSize != 4096 {
		t.Errorf("upload limit after override = %d, want 4096", config.MaxUploadSize)
	}
}

func TestApplyConfigOverridesRejectsConflictingAliases(t *testing.T) {
	for _, explicit := range []map[string]bool{
		{"https-port": true, "port": true},
		{"static-dir": true, "dir": true},
	} {
		if err := applyConfigOverrides(&serverConfig{}, serverConfig{}, explicit); err == nil {
			t.Errorf("apply overrides for %v succeeded, want conflict error", explicit)
		}
	}
}

func TestParseServerConfigAppliesCLIOverYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	staticDirectory := filepath.Join(t.TempDir(), "yaml-public")
	uploadDirectory := filepath.Join(t.TempDir(), "yaml-uploads")
	if err := os.Mkdir(staticDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(uploadDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	cliUploadDirectory := filepath.Join(t.TempDir(), "cli-uploads")
	if err := os.Mkdir(cliUploadDirectory, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte("https_port: 9443\ntcp_port: 9191\nstatic_directory: " + staticDirectory + "\nupload_directory: " + uploadDirectory + "\nmax_upload_size: 2048\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	config, err := parseServerConfig([]string{
		"-config", path,
		"-https-port", "10443",
		"-upload-dir", cliUploadDirectory,
	})
	if err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	if config.HTTPSPort != 10443 || config.TCPPort != 9191 {
		t.Errorf("ports = (%d, %d), want (10443, 9191)", config.HTTPSPort, config.TCPPort)
	}
	if config.StaticDirectory != staticDirectory || config.UploadDirectory != cliUploadDirectory {
		t.Errorf("directories = (%q, %q), want (%q, %q)", config.StaticDirectory, config.UploadDirectory, staticDirectory, cliUploadDirectory)
	}
	if config.MaxUploadSize != 2048 {
		t.Errorf("upload limit = %d, want 2048", config.MaxUploadSize)
	}
}

func TestParseServerConfigPreservesLegacyFlags(t *testing.T) {
	staticDirectory := t.TempDir()
	config, err := parseServerConfig([]string{"-port", "9443", "-dir", staticDirectory})
	if err != nil {
		t.Fatalf("parse legacy flags: %v", err)
	}
	if config.HTTPSPort != 9443 || config.StaticDirectory != staticDirectory {
		t.Errorf("legacy flags produced port %d and directory %q", config.HTTPSPort, config.StaticDirectory)
	}
}

func TestLoadServerConfigRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	for _, contents := range []string{
		"unknown: true\n",
		"https_port: 8443\n---\ntcp_port: 9090\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadServerConfig(path); err == nil {
			t.Errorf("load config %q succeeded, want error", contents)
		}
	}
}

func TestServerConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*serverConfig)
	}{
		{"https port below range", func(c *serverConfig) { c.HTTPSPort = 0 }},
		{"https port above range", func(c *serverConfig) { c.HTTPSPort = 65536 }},
		{"tcp port below range", func(c *serverConfig) { c.TCPPort = 0 }},
		{"tcp port above range", func(c *serverConfig) { c.TCPPort = 65536 }},
		{"blank static directory", func(c *serverConfig) { c.StaticDirectory = "  " }},
		{"blank upload directory", func(c *serverConfig) { c.UploadDirectory = "" }},
		{"nonpositive upload limit", func(c *serverConfig) { c.MaxUploadSize = 0 }},
		{"missing static directory", func(c *serverConfig) { c.StaticDirectory = filepath.Join(t.TempDir(), "missing") }},
		{"static path is a file", func(c *serverConfig) {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			c.StaticDirectory = path
		}},
		{"upload path has file parent", func(c *serverConfig) {
			path := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			c.UploadDirectory = filepath.Join(path, "uploads")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := defaultServerConfig()
			test.change(&config)
			if err := config.validate(); err == nil {
				t.Fatalf("validation error = %v, want a clear validation error", err)
			} else if strings.TrimSpace(err.Error()) == "" {
				t.Fatal("validation error message is empty")
			}
		})
	}
}

func TestServerConfigAcceptsMissingUploadDirectoryWithExistingParent(t *testing.T) {
	config := defaultServerConfig()
	config.UploadDirectory = filepath.Join(t.TempDir(), "not-created-yet")
	if err := config.validate(); err != nil {
		t.Fatalf("config with creatable upload directory is invalid: %v", err)
	}
}
