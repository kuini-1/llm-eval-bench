// Package config loads bench.yaml: the models that can be benchmarked, how
// to reach them, what they cost, and which one judges by default.
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"
)

type Price struct {
	Input  float64 `yaml:"input" json:"input"`   // USD per million input tokens
	Output float64 `yaml:"output" json:"output"` // USD per million output tokens
}

type Model struct {
	Name        string   `yaml:"name" json:"name"`         // label used in runs and reports
	Provider    string   `yaml:"provider" json:"provider"` // anthropic | openai | ollama | mock
	Model       string   `yaml:"model" json:"model"`       // the provider's model id
	BaseURL     string   `yaml:"base_url" json:"base_url,omitempty"`
	APIKey      string   `yaml:"api_key" json:"-"`
	MaxTokens   int      `yaml:"max_tokens" json:"max_tokens"`
	Effort      string   `yaml:"effort" json:"effort,omitempty"`           // Claude: low | medium | high | xhigh | max
	Temperature *float64 `yaml:"temperature" json:"temperature,omitempty"` // ignored for Claude, which rejects sampling parameters
	Think       *bool    `yaml:"think" json:"think,omitempty"`             // Ollama: turn a thinking model's reasoning on or off
	Concurrency int      `yaml:"concurrency" json:"concurrency"`
	Timeout     Duration `yaml:"timeout" json:"-"`
	Price       *Price   `yaml:"price" json:"price,omitempty"` // nil: unpriced, cost is reported as n/a
	Local       bool     `yaml:"local" json:"local"`           // runs on this machine; warmed up before timing
}

type Judge struct {
	Model             string  `yaml:"model"`   // name of a model in the list
	Samples           int     `yaml:"samples"` // verdicts per answer; >1 measures the judge's consistency
	SampleTemperature float64 `yaml:"sample_temperature"`
	Concurrency       int     `yaml:"concurrency"`
}

type Config struct {
	Addr   string  `yaml:"addr"`
	DB     string  `yaml:"db"`
	Models []Model `yaml:"models"`
	Judge  Judge   `yaml:"judge"`
}

// Duration reads "90s" style values.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	d.Duration = v
	return nil
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads the file and expands ${ENV} references, so keys stay out of it.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := envRef.ReplaceAllStringFunc(string(raw), func(m string) string {
		return os.Getenv(envRef.FindStringSubmatch(m)[1])
	})
	var c Config
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, c.normalize()
}

func (c *Config) normalize() error {
	if c.Addr == "" {
		c.Addr = "127.0.0.1:8097"
	}
	if c.DB == "" {
		c.DB = "data/evalbench.db"
	}
	seen := map[string]bool{}
	for i := range c.Models {
		m := &c.Models[i]
		if m.Name == "" || m.Model == "" {
			return fmt.Errorf("models[%d]: name and model are required", i)
		}
		if seen[m.Name] {
			return fmt.Errorf("model name %q is used twice", m.Name)
		}
		seen[m.Name] = true
		switch m.Provider {
		case "anthropic", "openai", "ollama", "mock":
		default:
			return fmt.Errorf("model %s: unknown provider %q (anthropic, openai, ollama or mock)", m.Name, m.Provider)
		}
		if m.MaxTokens == 0 {
			m.MaxTokens = 1024
		}
		if m.Concurrency == 0 {
			m.Concurrency = 4
			if m.Local {
				m.Concurrency = 1
			}
		}
		if m.Timeout.Duration == 0 {
			m.Timeout.Duration = 3 * time.Minute
		}
	}
	if c.Judge.Samples == 0 {
		c.Judge.Samples = 1
	}
	if c.Judge.SampleTemperature == 0 {
		c.Judge.SampleTemperature = 0.8
	}
	if c.Judge.Concurrency == 0 {
		c.Judge.Concurrency = 4
	}
	if c.Judge.Model != "" && c.Find(c.Judge.Model) == nil {
		return fmt.Errorf("judge model %q is not in the models list", c.Judge.Model)
	}
	return nil
}

func (c *Config) Find(name string) *Model {
	for i := range c.Models {
		if c.Models[i].Name == name {
			return &c.Models[i]
		}
	}
	return nil
}

// Ready reports whether a model has what it needs to be called (an API key
// for hosted providers), and why not.
func (m *Model) Ready() (bool, string) {
	if (m.Provider == "anthropic" || (m.Provider == "openai" && !m.Local)) && m.APIKey == "" {
		return false, "no API key set"
	}
	return true, ""
}

// Cost in USD for the given tokens, or nil if the model has no price.
func (m *Model) Cost(in, out int64) *float64 {
	if m.Price == nil {
		return nil
	}
	c := (float64(in)*m.Price.Input + float64(out)*m.Price.Output) / 1e6
	return &c
}
