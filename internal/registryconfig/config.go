package registryconfig

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/martinmares/oci-toolbox/internal/cleanup"
)

type Config struct {
	Registry     RegistryConfig `yaml:"registry"`
	GitLab       GitLabConfig   `yaml:"gitlab,omitempty"`
	Cleanup      CleanupConfig  `yaml:"cleanup"`
	Repositories []Repository   `yaml:"repositories"`
}

type GitLabConfig struct {
	URL     string `yaml:"url"`
	Project string `yaml:"project"`
	Token   string `yaml:"token"`
}

type RegistryConfig struct {
	URL      string    `yaml:"url"`
	Username string    `yaml:"username"`
	Password string    `yaml:"password"`
	TLS      TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Insecure bool   `yaml:"insecure"`
	CACert   string `yaml:"ca_cert"`
}

type CleanupConfig struct {
	DeleteVia          string   `yaml:"delete_via"`
	Mode               string   `yaml:"mode"`
	KeepCount          int      `yaml:"keep_count"`
	TagPattern         string   `yaml:"tag_pattern"`
	ProtectLatest      bool     `yaml:"protect_latest"`
	ProtectTagPatterns []string `yaml:"protect_tag_patterns"`
}

type Repository struct {
	Name string `yaml:"name"`
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(os.ExpandEnv(string(raw)))))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}

	if cfg.Registry.URL == "" {
		return Config{}, fmt.Errorf("registry.url is required")
	}
	if cfg.Cleanup.KeepCount == 0 {
		cfg.Cleanup.KeepCount = 14
	}
	if cfg.Cleanup.Mode == "" {
		cfg.Cleanup.Mode = "timestamp"
	}
	if cfg.Cleanup.TagPattern == "" {
		cfg.Cleanup.TagPattern = cleanup.DefaultTagPattern
	}
	return cfg, nil
}
