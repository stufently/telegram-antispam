package incident

import "github.com/stufently/telegram-antispam/internal/store"

var _ Repo = (*store.DB)(nil)
