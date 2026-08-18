// Package config loads and validates the YAML config that is the bot's single
// source of truth (spec §10).
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"gopkg.in/yaml.v3"
)

type ChatsPolicy struct {
	Mode          string  `yaml:"mode"`
	StartInDryRun bool    `yaml:"start_in_dry_run"`
	Allowlist     []int64 `yaml:"allowlist"`
}

// Duration wraps time.Duration so it can be parsed from YAML as a Go
// duration string (e.g. "60s", "5m") via time.ParseDuration, rather than
// requiring a raw integer of nanoseconds. Use Duration() to get the
// underlying time.Duration.
type Duration time.Duration

// Duration returns the underlying time.Duration value.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a YAML string scalar (e.g. "60s") into a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML renders the Duration back as a Go duration string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// DetectionRules configures the pure hard-rule detector (detect.Rules).
//
// BlockLinksForUntrusted is a *bool (rather than bool) so that Defaults can
// tell "unset in YAML" (nil, gets the default of true) apart from an
// explicit "false" in the config file — a plain bool can't distinguish
// those two cases since both leave the field at its zero value.
type DetectionRules struct {
	DenyStopwords          []string `yaml:"deny_stopwords"`
	AllowStopwords         []string `yaml:"allow_stopwords"`
	BlockLinksForUntrusted *bool    `yaml:"block_links_for_untrusted"`
	BannedDomains          []string `yaml:"banned_domains"`
}

// DetectionBehavior configures the pure behavioral detector (detect.BehaviorCfg).
type DetectionBehavior struct {
	DupThreshold        int      `yaml:"dup_threshold"`
	DupWindow           Duration `yaml:"dup_window"`
	ShortLen            int      `yaml:"short_len"`
	ShortFloodThreshold int      `yaml:"short_flood_threshold"`
	ShortWindow         Duration `yaml:"short_window"`
	FlagEdits           bool     `yaml:"flag_edits"`
}

// Detection configures the M3 detection cascade (internal/detect): the
// trust gate threshold plus the hard-rule and behavioral detector configs.
type Detection struct {
	TrustThreshold int               `yaml:"trust_threshold"`
	Rules          DetectionRules    `yaml:"rules"`
	Behavior       DetectionBehavior `yaml:"behavior"`
}

type Config struct {
	BotToken    string        `yaml:"bot_token"`
	AdminChatID int64         `yaml:"admin_chat_id"`
	Action      domain.Action `yaml:"action"`
	Chats       ChatsPolicy   `yaml:"chats"`
	Detection   Detection     `yaml:"detection"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(b)
}

func Parse(b []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDetectionDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyDetectionDefaults fills in sane defaults for any Detection field left
// unset in the YAML. Numeric/duration fields are treated as unset when they
// are still at their zero value (0 is never a meaningful configured value
// for these thresholds/windows), and are otherwise left as explicitly
// configured. BlockLinksForUntrusted uses a *bool specifically so an
// explicit "false" is never confused with "unset".
func (c *Config) applyDetectionDefaults() {
	if c.Detection.TrustThreshold == 0 {
		c.Detection.TrustThreshold = 5
	}
	if c.Detection.Rules.BlockLinksForUntrusted == nil {
		def := true
		c.Detection.Rules.BlockLinksForUntrusted = &def
	}
	b := &c.Detection.Behavior
	if b.DupThreshold == 0 {
		b.DupThreshold = 3
	}
	if b.DupWindow == 0 {
		b.DupWindow = Duration(60 * time.Second)
	}
	if b.ShortLen == 0 {
		b.ShortLen = 10
	}
	if b.ShortFloodThreshold == 0 {
		b.ShortFloodThreshold = 5
	}
	if b.ShortWindow == 0 {
		b.ShortWindow = Duration(30 * time.Second)
	}
	// FlagEdits defaults to false, which is already the zero value, so
	// there is nothing to fill in.
}

func (c *Config) Validate() error {
	if c.BotToken == "" {
		return fmt.Errorf("bot_token is required")
	}
	if c.AdminChatID == 0 {
		return fmt.Errorf("admin_chat_id is required")
	}
	switch c.Chats.Mode {
	case "auto", "allowlist", "owners_only":
	default:
		return fmt.Errorf("chats.mode must be auto|allowlist|owners_only, got %q", c.Chats.Mode)
	}
	switch c.Action {
	case domain.ActionDeleteMute, domain.ActionMute, domain.ActionBan, domain.ActionDeleteOnly:
	default:
		return fmt.Errorf("action must be delete_mute|mute|ban|delete_only, got %q", c.Action)
	}
	return nil
}
