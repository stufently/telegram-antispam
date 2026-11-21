package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/stufently/telegram-antispam/internal/domain"
	"github.com/stufently/telegram-antispam/internal/mediashadow"
	"github.com/stufently/telegram-antispam/internal/store"
)

// runJEVReport implements `jev-report`: every incident the media shadow
// labelled, its scores and the moderators' decision, then a summary of how
// the scores line up with the buttons. Read only, ids and numbers only — the
// database holds no message text, and neither does this output.
//
// Like backup it streams to stdout, because the runtime image is distroless:
// `kubectl exec deploy/tg-antispam -- /tg-antispam jev-report -days 14`.
func runJEVReport(args []string, out io.Writer, open func(string) (*store.DB, error)) (err error) {
	fs := flag.NewFlagSet("jev-report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", os.Getenv("DB_PATH"), "path to the database (default $DB_PATH)")
	days := fs.Int("days", 14, "report incidents from the last N days")
	cut := fs.Float64("threshold", 0.9, "score at or above which the shadow counts as saying \"ad\"")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return errors.New("no database path: pass -db or set DB_PATH")
	}
	if *days <= 0 || *cut <= 0 || *cut > 1 {
		return errors.New("-days must be positive and -threshold in (0, 1]")
	}
	if _, statErr := os.Stat(*dbPath); statErr != nil {
		return fmt.Errorf("database %s: %w", *dbPath, statErr)
	}
	db, err := open(*dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := db.Close(); err == nil {
			err = cerr
		}
	}()
	since := time.Now().Add(-time.Duration(*days) * 24 * time.Hour).Unix()
	rows, err := db.IncidentsWithSignal(mediashadow.SignalName, since)
	if err != nil {
		return err
	}
	writeJEVReport(out, rows, *days, *cut)
	return nil
}

// effectiveDecision reads the moderators' answer off the incident row. A
// successful Enforce press RELEASES its "enf" claim (so the undo buttons stay
// usable) and leaves the decision column empty, as does a /spam takeover; what
// remains is the row itself: a review card starts as dry-run, and only an
// applied sanction flips it to live and acted.
func effectiveDecision(r store.SignalIncident) string {
	if r.Decision == "" && !r.DryRun {
		switch domain.IncidentState(r.State) {
		case domain.StateActed, domain.StateCleaned, domain.StateDone:
			return "enforced"
		}
	}
	return r.Decision
}

// decisionClass folds the admin-card decisions into what the shadow is
// judged against. "enforced" (an applied sanction, see effectiveDecision),
// "enf" (an enforce claim) and "confirm" say spam;
// "fp" and "lift" say not spam; no decision yet is undecided.
func decisionClass(decision string) string {
	switch decision {
	case "enforced", "confirm", "enf":
		return "spam"
	case "fp", "lift":
		return "ham"
	case "":
		return "undecided"
	default:
		return "other"
	}
}

// jevTally counts decisions within one score bucket.
type jevTally struct{ spam, ham, undecided, other int }

func (t *jevTally) add(class string) {
	switch class {
	case "spam":
		t.spam++
	case "ham":
		t.ham++
	case "undecided":
		t.undecided++
	default:
		t.other++
	}
}

func writeJEVReport(out io.Writer, rows []store.SignalIncident, days int, cut float64) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "incident\tcreated_utc\tchat\tmsg\tpromo\tcontact\tordinary\tdecision")
	var high, mid, low jevTally
	// A row is "ad" when promo OR contact clears the cut: a bare phone
	// number on a picture is the flyer's whole point even when the model
	// hesitates about calling it an advertisement.
	var adHigh jevTally
	parsed := 0
	for _, r := range rows {
		s, ok := mediashadow.ParseDetail(r.Detail)
		if !ok {
			continue
		}
		parsed++
		decision := effectiveDecision(r)
		class := decisionClass(decision)
		if decision == "" {
			decision = "-"
		}
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%.2f\t%.2f\t%.2f\t%s\n", r.ID,
			time.Unix(r.CreatedAt, 0).UTC().Format("2006-01-02 15:04"), r.ChatID, r.MessageID,
			s.Promo, s.Contact, s.Ordinary, decision)
		switch {
		case s.Promo >= cut:
			high.add(class)
		case s.Promo >= 0.3:
			mid.add(class)
		default:
			low.add(class)
		}
		if s.Promo >= cut || s.Contact >= cut {
			adHigh.add(class)
		}
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "\n%d labelled photo card(s) in the last %d day(s)\n", parsed, days)
	tw = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "bucket\tspam\tham\tundecided\tother")
	for _, b := range []struct {
		name string
		t    jevTally
	}{
		{fmt.Sprintf("promo >= %.2f", cut), high},
		{fmt.Sprintf("0.30 <= promo < %.2f", cut), mid},
		{"promo < 0.30", low},
		{fmt.Sprintf("promo or contact >= %.2f", cut), adHigh},
	} {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", b.name, b.t.spam, b.t.ham, b.t.undecided, b.t.other)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "agreement (promo >= %.2f, decided): %s\n", cut, ratio(high.spam, high.spam+high.ham))
	_, _ = fmt.Fprintf(out, "missed (promo < 0.30 but moderators said spam): %d of %d decided\n", low.spam, low.spam+low.ham)
}

func ratio(n, d int) string {
	if d == 0 {
		return "n/a (no decided cards)"
	}
	return fmt.Sprintf("%d/%d = %.0f%%", n, d, 100*float64(n)/float64(d))
}
