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
//
// DupThreshold, ShortLen, and ShortFloodThreshold are *int (rather than
// int) so that Defaults can tell "unset in YAML" (nil, gets the default)
// apart from an explicit "0" in the config file. This matters because 0 is
// a documented, meaningful value here: detect.CheckBehavior treats
// DupThreshold/ShortFloodThreshold <= 0 as "check disabled" — a plain int
// can't distinguish "user asked to disable this check" from "user didn't
// set this field" since both leave the field at its zero value.
type DetectionBehavior struct {
	DupThreshold        *int     `yaml:"dup_threshold"`
	DupWindow           Duration `yaml:"dup_window"`
	ShortLen            *int     `yaml:"short_len"`
	ShortFloodThreshold *int     `yaml:"short_flood_threshold"`
	ShortWindow         Duration `yaml:"short_window"`
	FlagEdits           bool     `yaml:"flag_edits"`
}

// Detection configures the M3/M4 detection cascade (internal/detect): the
// trust gate threshold, the hard-rule and behavioral detector configs, and
// the M4 naive-Bayes borderline stage (internal/detect.BayesIsSpam).
//
// TrustThreshold is a *int for the same reason as DupThreshold etc: 0 is a
// meaningful explicit value (everyone is immediately trusted) and must not
// be silently promoted to the default.
//
// BayesThreshold is a *float64 for the same nil-vs-zero reason: 0.0 is
// this field's own documented default (a non-negative log-ratio is
// spam-leaning, so threshold 0.0 flags any message the scorer favors
// spam for at all), and a plain float64 can't tell "user explicitly
// wrote 0.0" apart from "user didn't set this field" since both leave
// the field at its zero value.
//
// BayesVocabGuess is a plain int (not a pointer): unlike the threshold,
// 0 is not a meaningful configuration for a vocabulary-size guess (it
// would make every Laplace-smoothed likelihood denominator degenerate),
// so the zero value doubles as "unset" and gets the default.
type Detection struct {
	TrustThreshold *int              `yaml:"trust_threshold"`
	Rules          DetectionRules    `yaml:"rules"`
	Behavior       DetectionBehavior `yaml:"behavior"`

	// BayesEnabled turns the M4 naive-Bayes borderline stage on or off.
	// *bool for the usual nil-vs-false reason: an explicit "false" must
	// not be re-promoted to the default "true". Default: true.
	BayesEnabled *bool `yaml:"bayes_enabled"`
	// BayesThreshold is the minimum log-ratio (see detect.BayesLogRatio)
	// at or above which a message is flagged spam-leaning. Default: 0.0.
	BayesThreshold *float64 `yaml:"bayes_threshold"`
	// BayesVocabGuess is the estimated vocabulary size used for Laplace
	// smoothing (see detect.BayesLogRatio's vocabGuess param). Default: 5000.
	BayesVocabGuess int `yaml:"bayes_vocab_guess"`
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
// unset in the YAML. Pointer fields (TrustThreshold, DupThreshold,
// ShortLen, ShortFloodThreshold, BlockLinksForUntrusted, BayesEnabled,
// BayesThreshold) are treated as unset only when nil, so an explicit "0"
// (or "false") in the config file is always honored rather than clobbered
// — 0 is a documented, meaningful value for the threshold fields (see
// DetectionBehavior and Detection docs). Duration window fields are
// treated as unset at their zero value, since a 0 window is not a
// documented "disable" and isn't a value anyone would configure on
// purpose. BayesVocabGuess is likewise treated as unset at its zero
// value, since 0 is not a meaningful vocabulary-size guess (see Detection
// doc).
func (c *Config) applyDetectionDefaults() {
	if c.Detection.TrustThreshold == nil {
		def := 5
		c.Detection.TrustThreshold = &def
	}
	if c.Detection.Rules.BlockLinksForUntrusted == nil {
		def := true
		c.Detection.Rules.BlockLinksForUntrusted = &def
	}
	b := &c.Detection.Behavior
	if b.DupThreshold == nil {
		def := 3
		b.DupThreshold = &def
	}
	if b.DupWindow == 0 {
		b.DupWindow = Duration(60 * time.Second)
	}
	if b.ShortLen == nil {
		def := 10
		b.ShortLen = &def
	}
	if b.ShortFloodThreshold == nil {
		def := 5
		b.ShortFloodThreshold = &def
	}
	if b.ShortWindow == 0 {
		b.ShortWindow = Duration(30 * time.Second)
	}
	// FlagEdits defaults to false, which is already the zero value, so
	// there is nothing to fill in.
	if c.Detection.BayesEnabled == nil {
		def := true
		c.Detection.BayesEnabled = &def
	}
	if c.Detection.BayesThreshold == nil {
		def := 0.0
		c.Detection.BayesThreshold = &def
	}
	if c.Detection.BayesVocabGuess == 0 {
		c.Detection.BayesVocabGuess = 5000
	}
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
