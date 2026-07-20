package main

import (
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// applyMutationsJournalConfig turns the durable mutations journal on or off for
// this process from the mutations-journal config knob (or the
// BD_MUTATIONS_JOURNAL environment override). It must run before any mutation on
// either write plumbing, since emission lives at the shared issueops seam. OFF
// by default: when off, the seam's emit helpers are a cheap no-op and no journal
// rows are written.
func applyMutationsJournalConfig() {
	issueops.SetJournalEnabled(config.GetBool("mutations-journal"))
}
