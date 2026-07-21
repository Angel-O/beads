package main

import (
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// applyEventsJournalConfig turns the durable events journal on or off for
// this process from the events-journal config knob (or the
// BD_EVENTS_JOURNAL environment override). It must run before any mutation on
// either write plumbing, since emission lives at the shared issueops seam. OFF
// by default: when off, the seam's emit helpers are a cheap no-op and no journal
// rows are written.
func applyEventsJournalConfig() {
	issueops.SetJournalEnabled(config.GetBool("events-journal"))
}
