// Package config loads and validates the YAML config that is the bot's single
// source of truth (spec §10).
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"gopkg.in/yaml.v3"
)

// Operators are Telegram user ids allowed to press the admin-chat buttons in
// ANY chat. Without them the only people who can act are administrators of the
// source chat itself, which is wrong for a shared admin chat: a moderator who
// watches the evidence feed but is not an admin of that particular group gets
// "not authorized" and the incident sits unreviewed.
type ChatsPolicy struct {
	Mode          string  `yaml:"mode"`
	StartInDryRun *bool   `yaml:"start_in_dry_run"`
	Allowlist     []int64 `yaml:"allowlist"`
	// Operators may act on incidents from any chat (see the type doc).
	Operators []int64 `yaml:"operators"`
	// Enforce lists chats that moderate for real, overriding whatever
	// dry-run value was stored when the chat first registered. It exists
	// because start_in_dry_run only ever seeds a NEW chat row (see
	// store.RegisterChat): without a config-side override the only way to
	// take an observed chat live would be an UPDATE against the SQLite file,
	// which a GitOps deploy cannot express and a distroless image cannot run.
	Enforce []int64 `yaml:"enforce"`
	// ForceDryRun lists chats pinned to dry-run no matter what is stored or
	// listed in Enforce. It is the brake: one line takes a misbehaving chat
	// out of enforcement without touching the database, and it deliberately
	// wins every conflict.
	ForceDryRun []int64 `yaml:"force_dry_run"`
}

// DryRunFor resolves the effective dry-run flag for chatID, given the value
// stored for that chat (or the policy default when the chat has no row yet).
//
// Config is evaluated last and therefore wins over the stored row: the
// database records where a chat STARTED, the config says where the operator
// wants it NOW. ForceDryRun beats Enforce so that the safe direction always
// has the final word, and a chat named in neither list keeps its stored
// value — listing every chat is not required to keep existing behavior.
func (p ChatsPolicy) DryRunFor(chatID int64, stored bool) bool {
	if containsChat(p.ForceDryRun, chatID) {
		return true
	}
	if containsChat(p.Enforce, chatID) {
		return false
	}
	return stored
}

func containsChat(ids []int64, chatID int64) bool {
	for _, id := range ids {
		if id == chatID {
			return true
		}
	}
	return false
}

// DryRunDefault reports whether new chats start in dry-run. It is a *bool so an
// explicit start_in_dry_run: false is never re-promoted to the default; nil
// (key absent) means the safe default TRUE — a fresh deploy observes and logs
// verdicts without acting until the operator opts into enforcement.
func (p ChatsPolicy) DryRunDefault() bool {
	if p.StartInDryRun == nil {
		return true
	}
	return *p.StartInDryRun
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

	// FakeAdminEnabled turns the M5 fake-admin detector on or off. *bool
	// for the usual nil-vs-false reason: an explicit "false" must not be
	// re-promoted to the default "true". Default: true.
	FakeAdminEnabled *bool `yaml:"fake_admin_enabled"`
	// FakeAdminMaxDistance is the maximum edit distance between a
	// display name and a trusted admin name for the fake-admin detector
	// to flag it as an impersonation attempt. A plain int (not a
	// pointer): 0 is not a meaningful configuration (it would only match
	// identical strings, which isn't a useful "unset vs disabled"
	// distinction here), so the zero value doubles as "unset" and gets
	// the default. Default: 1.
	FakeAdminMaxDistance int `yaml:"fake_admin_max_distance"`
	// FakeAdminMinFuzzyLen is the minimum rune length (of the shorter of the
	// two compared names) before fuzzy edit-distance matching is allowed;
	// below it, only an exact match flags. Prevents distance-1 false
	// positives on short names (e.g. "CEO" vs "CFO"). Plain int: 0 doubles as
	// "unset" and gets the default. Default: 5.
	FakeAdminMinFuzzyLen int `yaml:"fake_admin_min_fuzzy_len"`
	// FakeAdminSuspiciousTags is the list of sender tags (a message's
	// sender_tag / author signature) that the fake-admin detector flags,
	// matched by EXACT case-insensitive equality against the sender_tag —
	// not a substring scan of the display name. nil (key absent from YAML)
	// means "unset" and gets the default list; an explicit empty list in
	// YAML ([]) means "disable the suspicious-tag check" and is preserved as
	// empty, not re-defaulted — a YAML [] unmarshals to a non-nil empty
	// slice, so applyDetectionDefaults can tell the two apart with a nil
	// check. Default: ["admin", "support", "verified", "moderator", "админ",
	// "саппорт", "поддержка", "модератор", "модер", "верифицирован"].
	FakeAdminSuspiciousTags []string `yaml:"fake_admin_suspicious_tags"`
	// AdminCacheTTLSeconds is how long the fake-admin detector caches the
	// chat's real admin list before refreshing it, in seconds. A plain
	// int (not a pointer): 0 is not a meaningful TTL (it would disable
	// caching in a way nobody would configure on purpose), so the zero
	// value doubles as "unset" and gets the default. Default: 300.
	AdminCacheTTLSeconds int `yaml:"admin_cache_ttl_seconds"`

	// ReactionCleanupEnabled turns the M5 reaction-cleanup feature on or
	// off. *bool for the usual nil-vs-false reason: an explicit "false"
	// must not be re-promoted to the default "true". Default: true.
	ReactionCleanupEnabled *bool `yaml:"reaction_cleanup_enabled"`

	// EphemeralNoticeEnabled turns the M5 ephemeral moderation-notice
	// feature on or off. *bool for the usual nil-vs-false reason, even
	// though the default itself is false here: an explicit "true" must
	// not be left indistinguishable from "unset" (both would otherwise
	// read as the zero value). Default: false — off by default, since
	// delivery to a chat isn't guaranteed and the notice text is
	// chat-specific (see EphemeralNoticeText).
	EphemeralNoticeEnabled *bool `yaml:"ephemeral_notice_enabled"`
	// EphemeralNoticeText is the text of the ephemeral moderation notice
	// posted when EphemeralNoticeEnabled is true. Plain string: there is
	// no meaningful distinction between "unset" and "" here, since an
	// empty notice text is simply "no notice to show". Default: "".
	EphemeralNoticeText string `yaml:"ephemeral_notice_text"`
}

// Blocklist configures the M6 external blocklist syncer, which pulls spam
// user-ID lists from lols.bot and cas.chat on a schedule and checks new
// members/messages against the merged set (internal/blocklist).
//
// Enabled is a *bool for the usual nil-vs-false reason: an explicit "false"
// must not be re-promoted to the default "true". Default: true.
type Blocklist struct {
	// Enabled turns the whole M6 blocklist syncer on or off. Default: true.
	Enabled *bool `yaml:"enabled"`

	// LolsFullURL is the source of the full lols.bot ban list, fetched
	// every FullRefresh interval. Default: "https://lols.bot/spam/banlist.txt".
	LolsFullURL string `yaml:"lols_full_url"`
	// LolsDeltaURL is the source of the incremental (last-hour) lols.bot
	// ban list, fetched every DeltaRefresh interval.
	// Default: "https://lols.bot/spam/banlist-1h.txt".
	LolsDeltaURL string `yaml:"lols_delta_url"`
	// CasFullURL is the source of the full Combot Anti-Spam (CAS) ban
	// list, fetched every FullRefresh interval.
	// Default: "https://api.cas.chat/export.csv".
	CasFullURL string `yaml:"cas_full_url"`

	// FullRefresh is how often the full ban lists (LolsFullURL,
	// CasFullURL) are re-fetched. Default: 6h.
	FullRefresh Duration `yaml:"full_refresh"`
	// DeltaRefresh is how often the incremental ban list (LolsDeltaURL)
	// is re-fetched. Default: 1h.
	DeltaRefresh Duration `yaml:"delta_refresh"`
	// HTTPTimeout is the per-request timeout used when fetching any of
	// the blocklist sources above. Default: 30s.
	HTTPTimeout Duration `yaml:"http_timeout"`
}

// Ops configures the M7 observability surface: the metrics HTTP server and
// the daily digest scheduler.
//
// MetricsEnabled and DigestEnabled are *bool for the usual nil-vs-false
// reason: an explicit "false" in the config file must not be re-promoted to
// the default "true" (see Blocklist.Enabled for the same pattern).
type Ops struct {
	// MetricsEnabled turns the /metrics + /healthz HTTP server on or off.
	// Default: true.
	MetricsEnabled *bool `yaml:"metrics_enabled"`
	// MetricsAddr is the listen address for the metrics HTTP server, e.g.
	// ":9090". Default: ":9090".
	MetricsAddr string `yaml:"metrics_addr"`
	// DigestEnabled turns the daily admin-chat digest scheduler on or off.
	// Default: true.
	DigestEnabled *bool `yaml:"digest_enabled"`
	// DigestInterval is how often the digest is sent. Default: 24h. A
	// non-positive value is clamped to the default (per M6's Blocklist
	// lesson: a non-positive interval would panic time.NewTicker in the
	// scheduler goroutine).
	DigestInterval Duration `yaml:"digest_interval"`
}

// LLMProvider is one configured LLM backend for the borderline stage (§5.4).
// Kind selects the API ("openai" or "anthropic"); APIKey and Model are the
// credentials and model name for that API.
type LLMProvider struct {
	Kind   string `yaml:"kind"`
	APIKey string `yaml:"api_key"`
	Model  string `yaml:"model"`
}

// PromptFor returns the system prompt to use for chatID: its override when one
// is configured, otherwise the shared prompt (empty means the built-in one).
func (l LLM) PromptFor(chatID int64) string {
	if p, ok := l.PromptOverrides[chatID]; ok && strings.TrimSpace(p) != "" {
		return p
	}
	return l.Prompt
}

// LLM configures the optional, opt-in borderline LLM adjudication stage
// (spec §5.4). It runs one or two paid official LLM APIs over a message whose
// Bayes score sits within BorderlineBand of the threshold and combines their
// verdicts under Policy. It is disabled by default because it sends message
// text to an external service (spec §9 privacy rules).
//
// Enabled is a *bool so an explicit "false" is never re-promoted; unlike the
// Blocklist/Ops blocks its default is FALSE (privacy: external calls are
// opt-in).
type LLM struct {
	// Enabled turns the borderline LLM stage on. Default: false.
	Enabled *bool `yaml:"enabled"`
	// Policy combines multiple providers' verdicts: "any" (spam if any says
	// spam) or "all" (spam only if all agree). Default: "any". Ignored with a
	// single provider.
	Policy string `yaml:"policy"`
	// BorderlineBand is the width below BayesThreshold within which a message
	// is sent to the LLM. Must be > 0 for the stage to ever run. Default: 0.5.
	BorderlineBand float64 `yaml:"borderline_band"`
	// HTTPTimeout bounds each provider call. Default: 10s.
	HTTPTimeout Duration `yaml:"http_timeout"`
	// Providers lists the LLM backends (1 or 2). An empty list disables the
	// stage regardless of Enabled.
	Providers []LLMProvider `yaml:"providers"`
	// PromptOverrides replaces Prompt for specific chats, keyed by chat id.
	//
	// It exists because one prompt cannot serve chats with opposite norms:
	// in a trading chat "buying USDT" is the topic, while in the others it is
	// the exact spam being filtered. An override REPLACES the whole prompt
	// (it does not extend it), so it must restate every category it still
	// wants — the same trap as any "custom prompt" setting: edit the shared
	// list and the overrides silently keep the old one.
	PromptOverrides map[int64]string `yaml:"prompt_overrides"`
	// Prompt overrides the built-in classifier system prompt. The built-in
	// one is English and generic; a chat with its own norms (a marketplace
	// where "selling my used X" is legitimate, a crypto chat where coin
	// names are not automatically spam) needs its own wording, and hardcoding
	// one prompt makes those chats unservable. Empty ⇒ built-in prompt.
	Prompt string `yaml:"prompt"`
	// Temperature is sent to the provider only when set. It is a *float64
	// because "unset" and "0" must differ: reasoning models REJECT an
	// explicit temperature, so sending 0 by default would break them, while
	// 0 is exactly what a classic completion model wants.
	Temperature *float64 `yaml:"temperature"`
	// MaxTokens caps the reply length. Unset (or 0) means "do not send a
	// cap" for providers that allow omitting it. A tiny cap is actively
	// harmful with reasoning models, which spend the budget thinking and
	// then return an empty answer — which the stage reads as "not spam".
	MaxTokens int `yaml:"max_tokens"`
}

type Config struct {
	BotToken    string        `yaml:"bot_token"`
	AdminChatID int64         `yaml:"admin_chat_id"`
	Action      domain.Action `yaml:"action"`
	Chats       ChatsPolicy   `yaml:"chats"`
	Detection   Detection     `yaml:"detection"`
	Blocklist   Blocklist     `yaml:"blocklist"`
	Ops         Ops           `yaml:"ops"`
	LLM         LLM           `yaml:"llm"`
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
	c.applyBlocklistDefaults()
	c.applyOpsDefaults()
	c.applyLLMDefaults()
	// BOT_TOKEN env overrides bot_token from the file, so the token can be
	// supplied by a Kubernetes Secret / Docker secret and kept out of the
	// config file entirely (12-factor). Env wins when both are set.
	if v := os.Getenv("BOT_TOKEN"); v != "" {
		c.BotToken = v
	}
	// Same reasoning for the LLM credentials: a Secret-supplied env var keeps
	// them out of the config file, which in a Kubernetes deploy is a
	// ConfigMap in version control. Env wins when both are set.
	for i := range c.LLM.Providers {
		var envKey string
		switch c.LLM.Providers[i].Kind {
		case "openai":
			envKey = "OPENAI_API_KEY"
		case "anthropic":
			envKey = "ANTHROPIC_API_KEY"
		default:
			continue
		}
		if v := os.Getenv(envKey); v != "" {
			c.LLM.Providers[i].APIKey = v
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyDetectionDefaults fills in sane defaults for any Detection field left
// unset in the YAML. Pointer fields (TrustThreshold, DupThreshold,
// ShortLen, ShortFloodThreshold, BlockLinksForUntrusted, BayesEnabled,
// BayesThreshold, FakeAdminEnabled, ReactionCleanupEnabled,
// EphemeralNoticeEnabled) are treated as unset only when nil, so an
// explicit "0" (or "false"/"true") in the config file is always honored
// rather than clobbered — 0 is a documented, meaningful value for the
// threshold fields (see DetectionBehavior and Detection docs). Duration
// window fields are treated as unset at their zero value, since a 0
// window is not a documented "disable" and isn't a value anyone would
// configure on purpose. BayesVocabGuess, FakeAdminMaxDistance, and
// AdminCacheTTLSeconds are likewise treated as unset at their zero value,
// since 0 is not a meaningful configuration for any of them (see
// Detection doc). FakeAdminSuspiciousTags is treated as unset only when
// nil, so an explicit empty list ([]) is preserved rather than
// re-defaulted (see Detection doc).
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
		// A conservative positive default: require clearer spam evidence than
		// the neutral 0.0 boundary, reducing false positives on a trained
		// corpus. Operators tune this precisely via detect.Evaluate.
		def := 1.0
		c.Detection.BayesThreshold = &def
	}
	if c.Detection.BayesVocabGuess == 0 {
		c.Detection.BayesVocabGuess = 5000
	}
	if c.Detection.FakeAdminEnabled == nil {
		def := true
		c.Detection.FakeAdminEnabled = &def
	}
	if c.Detection.FakeAdminMaxDistance == 0 {
		c.Detection.FakeAdminMaxDistance = 1
	}
	if c.Detection.FakeAdminMinFuzzyLen == 0 {
		c.Detection.FakeAdminMinFuzzyLen = 5
	}
	if c.Detection.FakeAdminSuspiciousTags == nil {
		c.Detection.FakeAdminSuspiciousTags = []string{"admin", "support", "verified", "moderator", "админ", "саппорт", "поддержка", "модератор", "модер", "верифицирован"}
	}
	if c.Detection.AdminCacheTTLSeconds == 0 {
		c.Detection.AdminCacheTTLSeconds = 300
	}
	if c.Detection.ReactionCleanupEnabled == nil {
		def := true
		c.Detection.ReactionCleanupEnabled = &def
	}
	if c.Detection.EphemeralNoticeEnabled == nil {
		def := false
		c.Detection.EphemeralNoticeEnabled = &def
	}
	// EphemeralNoticeText defaults to "", which is already the zero
	// value, so there is nothing to fill in.
}

// applyBlocklistDefaults fills in sane defaults for any Blocklist field
// left unset in the YAML. Enabled is treated as unset only when nil, so an
// explicit "false" in the config file is always honored rather than
// clobbered (see Blocklist doc). The URL strings and Duration fields are
// treated as unset at their zero value, since an empty URL or a 0 interval
// is not a documented, meaningful configuration for any of them.
func (c *Config) applyBlocklistDefaults() {
	if c.Blocklist.Enabled == nil {
		def := true
		c.Blocklist.Enabled = &def
	}
	if c.Blocklist.LolsFullURL == "" {
		c.Blocklist.LolsFullURL = "https://lols.bot/spam/banlist.txt"
	}
	if c.Blocklist.LolsDeltaURL == "" {
		c.Blocklist.LolsDeltaURL = "https://lols.bot/spam/banlist-1h.txt"
	}
	if c.Blocklist.CasFullURL == "" {
		c.Blocklist.CasFullURL = "https://api.cas.chat/export.csv"
	}
	// Intervals are clamped at <= 0 (not just == 0): a negative full/delta
	// interval would panic time.NewTicker inside the syncer goroutine and
	// crash the process, and a non-positive HTTP timeout would disable the
	// fetch deadline. A non-positive value is treated as unset → default.
	if c.Blocklist.FullRefresh <= 0 {
		c.Blocklist.FullRefresh = Duration(6 * time.Hour)
	}
	if c.Blocklist.DeltaRefresh <= 0 {
		c.Blocklist.DeltaRefresh = Duration(1 * time.Hour)
	}
	if c.Blocklist.HTTPTimeout <= 0 {
		c.Blocklist.HTTPTimeout = Duration(30 * time.Second)
	}
}

// applyOpsDefaults fills in sane defaults for any Ops field left unset in
// the YAML. MetricsEnabled and DigestEnabled are treated as unset only when
// nil, so an explicit "false" in the config file is always honored rather
// than clobbered (see Ops doc). MetricsAddr is treated as unset at its zero
// value, since an empty listen address is not a documented, meaningful
// configuration. DigestInterval is clamped at <= 0 (not just == 0): a
// negative interval would panic time.NewTicker inside the digest scheduler
// goroutine and crash the process (see applyBlocklistDefaults for the same
// M6 lesson).
func (c *Config) applyOpsDefaults() {
	if c.Ops.MetricsEnabled == nil {
		def := true
		c.Ops.MetricsEnabled = &def
	}
	if c.Ops.MetricsAddr == "" {
		c.Ops.MetricsAddr = ":9090"
	}
	if c.Ops.DigestEnabled == nil {
		def := true
		c.Ops.DigestEnabled = &def
	}
	if c.Ops.DigestInterval <= 0 {
		c.Ops.DigestInterval = Duration(24 * time.Hour)
	}
}

// applyLLMDefaults fills LLM fields left unset. Enabled defaults to FALSE
// (external calls are opt-in), so — unlike the other blocks — a nil Enabled
// stays disabled.
func (c *Config) applyLLMDefaults() {
	if c.LLM.Enabled == nil {
		def := false
		c.LLM.Enabled = &def
	}
	if c.LLM.Policy == "" {
		c.LLM.Policy = "any"
	}
	if c.LLM.BorderlineBand == 0 {
		c.LLM.BorderlineBand = 0.5
	}
	if c.LLM.HTTPTimeout <= 0 {
		c.LLM.HTTPTimeout = Duration(10 * time.Second)
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
	// An enforce entry outside the allowlist can never fire (the allowlist
	// gate drops the update before moderation), so it is an operator typo
	// with a silent, dangerous failure mode: the chat looks configured for
	// enforcement and quietly moderates nothing. Reject it loudly instead.
	if c.Chats.Mode == "allowlist" {
		for _, id := range c.Chats.Enforce {
			if !containsChat(c.Chats.Allowlist, id) {
				return fmt.Errorf("chats.enforce contains %d, which is not in chats.allowlist", id)
			}
		}
	}
	switch c.Action {
	case domain.ActionDeleteMute, domain.ActionMute, domain.ActionBan, domain.ActionDeleteOnly:
	default:
		return fmt.Errorf("action must be delete_mute|mute|ban|delete_only, got %q", c.Action)
	}
	if c.LLM.Enabled != nil && *c.LLM.Enabled {
		switch c.LLM.Policy {
		case "any", "all":
		default:
			return fmt.Errorf("llm.policy must be any|all, got %q", c.LLM.Policy)
		}
		if len(c.LLM.Providers) == 0 {
			return fmt.Errorf("llm.enabled is true but no llm.providers are configured")
		}
		for i, p := range c.LLM.Providers {
			switch p.Kind {
			case "openai", "anthropic":
			default:
				return fmt.Errorf("llm.providers[%d].kind must be openai|anthropic, got %q", i, p.Kind)
			}
			if p.APIKey == "" {
				return fmt.Errorf("llm.providers[%d] (%s) missing api_key", i, p.Kind)
			}
			if p.Model == "" {
				return fmt.Errorf("llm.providers[%d] (%s) missing model", i, p.Kind)
			}
		}
	}
	return nil
}
