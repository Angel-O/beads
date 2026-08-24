// Package doltmemorymigration contains the executable A3 Dolt
// conversion-coordination experiment.
//
// It is deliberately isolated from production command wiring, public APIs,
// storage interfaces, and the schema migration chain. Its tables and markers
// are fixture state for testing one truthful branch-at-a-time conversion
// strategy on Dolt.
package doltmemorymigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"github.com/steveyegge/beads/internal/storage/schema"
)

const (
	// These names are private prototype details, not interchange or schema
	// contracts. The control and writer cells live in one branch-qualified
	// local_metadata working table, which is dolt-ignored and clone-local. Every
	// related branch view addresses that same table explicitly.
	controlKey      = "memory-beads/a3/prototype/control"
	writerCellKey   = "memory-beads/a3/prototype/config-writer"
	migrationScope  = "memory-beads-a3-prototype-v2"
	legacyPrefix    = "kv.memory."
	beadsTable      = "memory_a3_prototype_beads"
	revisionsTable  = "memory_a3_prototype_revisions"
	branchLedger    = "memory_a3_prototype_branch_ledger"
	prototypeAuthor = "Memory A3 Prototype <memory-a3@example.invalid>"
)

// Phase is persisted only in the provider-private clone-local control record.
type Phase string

const (
	PhaseInProgress Phase = "migration_in_progress"
	PhaseComplete   Phase = "complete"
)

// EventPoint exposes deterministic fault and pause points to the executable
// spike. It is test control, not product behavior.
type EventPoint string

const (
	EventPreflightRead     EventPoint = "preflight_read"
	EventControlPrepared   EventPoint = "control_prepared"
	EventControlCommitted  EventPoint = "control_committed"
	EventBranchPrepared    EventPoint = "branch_prepared"
	EventBranchPublished   EventPoint = "branch_published"
	EventBeforeFinalize    EventPoint = "before_finalize"
	EventCompletePrepared  EventPoint = "complete_prepared"
	EventCompleteCommitted EventPoint = "complete_committed"
)

// Event describes one prototype-only fault point.
type Event struct {
	Point  EventPoint
	Branch string
}

// EventHook may pause or fail one prototype phase. An error before publication
// rolls that transaction back. An error after publication deliberately leaves
// the clone-local control record behind so a fresh coordinator can reconcile
// the branch ledger and resume idempotently.
type EventHook func(context.Context, Event) error

// Options are provider-private inputs to the prototype coordinator.
type Options struct {
	ProjectID     string
	Author        string
	ExternalOwner string
	OnEvent       EventHook
}

// Result reports only what the prototype proved during this invocation.
type Result struct {
	MigrationID     string
	MigratedViews   []string
	AlreadyComplete bool
}

// Snapshot is a body-free diagnostic view of the provider-private control
// record for tests. It intentionally exposes no serialized plan or source data.
type Snapshot struct {
	MigrationID string
	Phase       Phase
	Views       int
	Remaining   []string
}

// MigrationInProgressError is the typed unavailable result both canonical and
// deprecated memory surfaces must return while the physical branch views are
// being converted. It contains no memory bodies.
type MigrationInProgressError struct {
	MigrationID string
	Remaining   []string
}

func (e *MigrationInProgressError) Error() string {
	if len(e.Remaining) == 0 {
		return fmt.Sprintf("memory migration %s is in progress; resume the migration before using memory", e.MigrationID)
	}
	return fmt.Sprintf("memory migration %s is in progress on view(s) %s; resume the migration before using memory",
		e.MigrationID, strings.Join(e.Remaining, ", "))
}

// LegacyNamespaceRetiredError prevents a generic config/KV alias from
// recreating the old memory store after successful conversion.
type LegacyNamespaceRetiredError struct{ Key string }

func (e *LegacyNamespaceRetiredError) Error() string {
	return fmt.Sprintf("legacy memory namespace is retired for key %q; use canonical Memory Bead operations", e.Key)
}

// SourceChangedError proves stale preflight never publishes over a source view
// changed by an uncoordinated writer.
type SourceChangedError struct{ Branch string }

func (e *SourceChangedError) Error() string {
	return fmt.Sprintf("memory migration source view %q changed after preflight; inspect and resume", e.Branch)
}

// ExternalOwnerError is the stable actionable refusal for a schema owned by a
// service such as beads-team-server.
type ExternalOwnerError struct{ Owner string }

func (e *ExternalOwnerError) Error() string {
	owner := strings.TrimSpace(e.Owner)
	if owner == "" {
		owner = "the external provider"
	}
	return fmt.Sprintf("legacy memory is owned by %s; ask its operator to run the provider's Memory Beads migration", owner)
}

// PublicationIndeterminateError models loss of the branch publication
// acknowledgement. Resume must inspect the durable branch ledger instead of
// replaying blindly.
type PublicationIndeterminateError struct {
	Branch string
	Cause  error
}

func (e *PublicationIndeterminateError) Error() string {
	return fmt.Sprintf("memory migration publication acknowledgement for view %q is indeterminate; inspect the ledger and resume", e.Branch)
}

func (e *PublicationIndeterminateError) Unwrap() error { return e.Cause }

// PreflightError is a body-safe, non-mutating refusal.
type PreflightError struct{ Detail string }

func (e *PreflightError) Error() string { return "memory migration preflight: " + e.Detail }

// DBConn is the minimal SQL surface used by the provider-private gate. It is
// satisfied by *sql.DB, *sql.Conn, and *sql.Tx and never crosses a public API.
type DBConn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Coordinator struct {
	db      *sql.DB
	options Options
}

// New creates an unwired provider-private coordinator.
func New(db *sql.DB, options Options) (*Coordinator, error) {
	if db == nil {
		return nil, fmt.Errorf("memory migration prototype: database must not be nil")
	}
	return &Coordinator{db: db, options: options}, nil
}

// RunRefMutation serializes a provider-controlled branch or ref mutation with
// this prototype's migration coordinator. Dolt branch procedures cannot join
// the branch conversion transaction, so the shared provider-private named lock
// defines their observable ordering instead. A ref created from old history
// after migration still fails CheckMemoryAccess until a subsequent Run brings
// that view into the completed plan.
func RunRefMutation(ctx context.Context, db *sql.DB, mutate func(context.Context, *sql.Conn) error) (retErr error) {
	if db == nil {
		return fmt.Errorf("memory migration prototype: database must not be nil")
	}
	if mutate == nil {
		return fmt.Errorf("memory migration prototype: ref mutation must not be nil")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("memory migration prototype: pin ref mutation connection: %w", err)
	}
	defer conn.Close()

	database, err := baseDatabase(ctx, conn)
	if err != nil {
		return err
	}
	lockName := schema.MigrationLockName(database)
	if err := schema.AcquireMigrationLock(ctx, conn, lockName); err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, schema.ReleaseMigrationLock(conn, lockName)) }()
	return mutate(ctx, conn)
}

type controlRecord struct {
	MigrationID string     `json:"migration_id"`
	Scope       string     `json:"scope"`
	Phase       Phase      `json:"phase"`
	ProjectID   string     `json:"project_id"`
	Author      string     `json:"author"`
	Views       []viewPlan `json:"views"`
}

type viewPlan struct {
	Branch            string `json:"branch"`
	SourceHead        string `json:"source_head"`
	ConfigFingerprint string `json:"config_fingerprint"`
	LegacyCount       int    `json:"legacy_count"`
	Applied           bool   `json:"applied"`
}

type legacyMemory struct {
	userKey string
	body    string
}

// PrepareControlPlane materializes the single conflict-forcing cell used by
// both ordinary config writers and the migration marker transaction. Dolt
// keeps ignored working tables per branch, so all views explicitly address the
// one branch-qualified local_metadata working table chosen here. This creates
// no versioned schema and avoids pretending an unqualified ignored table is
// automatically shared after checkout.
func PrepareControlPlane(ctx context.Context, db DBConn) error {
	if _, found, err := locateControlPlane(ctx, db); err != nil {
		return err
	} else if found {
		return nil
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS local_metadata (
		`+"`key`"+` VARCHAR(255) PRIMARY KEY,
		value TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("memory migration prototype: ensure control table: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT IGNORE INTO local_metadata (`key`, value) VALUES (?, '0')", writerCellKey); err != nil {
		return fmt.Errorf("memory migration prototype: prepare writer coordination cell: %w", err)
	}
	return nil
}

// AdmitConfigMutation is the shared-writer half of the prototype. It must run
// inside the same transaction as the config mutation. Rewriting one clone-local
// cell makes a writer that overlaps marker installation serialize rather than
// merge a stale config snapshot. Once the marker is visible, writes fail before
// touching config; after completion the legacy memory namespace stays retired.
func AdmitConfigMutation(ctx context.Context, tx DBConn, key string) error {
	control, found, err := loadControl(ctx, tx)
	if err != nil {
		return err
	}
	if !found {
		migrationID, views, evidence, err := findMigrationEvidence(ctx, tx)
		if err != nil {
			return err
		}
		if evidence {
			return &MigrationInProgressError{MigrationID: migrationID, Remaining: views}
		}
	}
	if found {
		switch control.Phase {
		case PhaseInProgress:
			return inProgress(control)
		case PhaseComplete:
			if strings.HasPrefix(key, legacyPrefix) {
				return &LegacyNamespaceRetiredError{Key: key}
			}
		}
	}
	return touchWriterCell(ctx, tx)
}

// CheckMemoryAccess is the common gate for canonical and deprecated memory
// reads and writes. A completed global marker is not trusted by itself: the
// active view must carry the matching branch ledger and no legacy rows. Thus a
// branch created later from an old commit fails closed instead of resurrecting
// a mixed memory plane.
func CheckMemoryAccess(ctx context.Context, db DBConn) error {
	control, found, err := loadControl(ctx, db)
	if err != nil {
		return err
	}
	if !found {
		migrationID, views, evidence, err := findMigrationEvidence(ctx, db)
		if err != nil {
			return err
		}
		if evidence {
			return &MigrationInProgressError{MigrationID: migrationID, Remaining: views}
		}
		return nil
	}
	if control.Phase == PhaseInProgress {
		return inProgress(control)
	}
	remaining, err := completionGaps(ctx, db, control)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return &MigrationInProgressError{MigrationID: control.MigrationID, Remaining: remaining}
	}
	return nil
}

// InspectControl returns a body-free test diagnostic.
func InspectControl(ctx context.Context, db DBConn) (Snapshot, bool, error) {
	control, found, err := loadControl(ctx, db)
	if err != nil || !found {
		return Snapshot{}, found, err
	}
	return Snapshot{
		MigrationID: control.MigrationID,
		Phase:       control.Phase,
		Views:       len(control.Views),
		Remaining:   remainingViews(control),
	}, true, nil
}

// Run starts or resumes the branch-at-a-time prototype. Only the clone-local
// control record is visible between branch publications; ordinary memory
// access remains typed-unavailable until final verification succeeds.
func (c *Coordinator) Run(ctx context.Context) (result Result, retErr error) {
	if owner := strings.TrimSpace(c.options.ExternalOwner); owner != "" {
		return result, &ExternalOwnerError{Owner: owner}
	}
	projectID := strings.TrimSpace(c.options.ProjectID)
	if projectID == "" || projectID == "00000000-0000-0000-0000-000000000000" {
		return result, &PreflightError{Detail: "a durable non-sentinel Project ID is required"}
	}
	author := strings.TrimSpace(c.options.Author)
	if !validAuthor(author) {
		return result, &PreflightError{Detail: "configure an accountable human author as Name <email>"}
	}
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("memory migration prototype: pin connection: %w", err)
	}
	defer conn.Close()

	var originalBranch string
	if err := conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&originalBranch); err != nil {
		return result, fmt.Errorf("memory migration prototype: identify original branch: %w", err)
	}
	defer func() {
		if originalBranch != "" {
			retErr = errors.Join(retErr, drainCall(context.Background(), conn, "CALL DOLT_CHECKOUT(?)", originalBranch))
		}
	}()

	database, err := baseDatabase(ctx, conn)
	if err != nil {
		return result, err
	}
	lockName := schema.MigrationLockName(database)
	if err := schema.AcquireMigrationLock(ctx, conn, lockName); err != nil {
		return result, err
	}
	defer func() { retErr = errors.Join(retErr, schema.ReleaseMigrationLock(conn, lockName)) }()
	if err := PrepareControlPlane(ctx, conn); err != nil {
		return result, err
	}

	control, found, err := loadControl(ctx, conn)
	if err != nil {
		return result, err
	}
	if !found {
		migrationID, _, evidence, err := findMigrationEvidence(ctx, conn)
		if err != nil {
			return result, err
		}
		if evidence {
			result.MigrationID = migrationID
			return result, &PreflightError{Detail: fmt.Sprintf(
				"versioned migration evidence for %s exists but the clone-local phase record is missing; restore or rebuild the control record before resuming",
				migrationID)}
		}
		control, err = c.beginControl(ctx, conn, projectID, author)
		if err != nil {
			return result, err
		}
	} else if control.ProjectID != projectID {
		return result, &PreflightError{Detail: fmt.Sprintf(
			"migration %s belongs to Project ID %q, not the provider's current Project ID %q",
			control.MigrationID, control.ProjectID, projectID)}
	}
	result.MigrationID = control.MigrationID

	if control.Phase == PhaseComplete {
		complete, err := c.auditComplete(ctx, conn, control)
		if err != nil {
			return result, err
		}
		if complete {
			result.AlreadyComplete = true
			return result, nil
		}
		control, err = c.reopenForCurrentViews(ctx, conn, control)
		if err != nil {
			return result, err
		}
	}

	for {
		control, err = c.refreshPlan(ctx, conn, control)
		if err != nil {
			return result, err
		}

		progressed := false
		for i := range control.Views {
			if control.Views[i].Applied {
				continue
			}
			published, err := c.convertView(ctx, conn, control, control.Views[i])
			if err != nil {
				return result, err
			}
			if published {
				result.MigratedViews = append(result.MigratedViews, control.Views[i].Branch)
			}
			control, _, err = loadControl(ctx, conn)
			if err != nil {
				return result, err
			}
			progressed = true
			break
		}
		if progressed {
			continue
		}

		if err := c.emit(ctx, Event{Point: EventBeforeFinalize}); err != nil {
			return result, err
		}
		// Re-inventory after the fault point. A branch created from an old ref
		// here becomes another planned view rather than escaping finalization.
		control, err = c.refreshPlan(ctx, conn, control)
		if err != nil {
			return result, err
		}
		if len(remainingViews(control)) != 0 {
			continue
		}

		control, err = c.markComplete(ctx, conn, control)
		if err != nil {
			return result, err
		}
		complete, err := c.auditComplete(ctx, conn, control)
		if err != nil {
			return result, err
		}
		if complete {
			return result, nil
		}
		// A ref created concurrently between final inventory and the complete
		// marker is treated as a later operation. Re-open the durable marker and
		// converge it before returning success. A ref created after this audit is
		// still fail-closed by CheckMemoryAccess's per-view ledger check.
		control, err = c.reopenForCurrentViews(ctx, conn, control)
		if err != nil {
			return result, err
		}
	}
}

func (c *Coordinator) beginControl(ctx context.Context, conn *sql.Conn, projectID, author string) (controlRecord, error) {
	for {
		if err := ctx.Err(); err != nil {
			return controlRecord{}, err
		}
		if unsafe, err := unsafeDirtyConfigKeys(ctx, conn); err != nil {
			return controlRecord{}, err
		} else if len(unsafe) != 0 {
			return controlRecord{}, &PreflightError{Detail: fmt.Sprintf(
				"config has unrelated working-set changes at key(s) %s; publish or revert them before retrying",
				strings.Join(unsafe, ", "))}
		}
		before, err := inventoryViews(ctx, conn)
		if err != nil {
			return controlRecord{}, err
		}
		if err := validatePreflight(before); err != nil {
			return controlRecord{}, err
		}
		if err := validateNoEmptyLegacy(ctx, conn, before); err != nil {
			return controlRecord{}, err
		}
		if err := c.emit(ctx, Event{Point: EventPreflightRead}); err != nil {
			return controlRecord{}, err
		}
		control := controlRecord{
			MigrationID: uuid.NewString(),
			Scope:       migrationScope,
			Phase:       PhaseInProgress,
			ProjectID:   projectID,
			Author:      author,
			Views:       before,
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			if isSerializationError(err) {
				continue
			}
			return controlRecord{}, fmt.Errorf("memory migration prototype: start marker transaction: %w", err)
		}
		if err := touchWriterCell(ctx, tx); err != nil {
			_ = tx.Rollback()
			if isSerializationError(err) {
				continue
			}
			return controlRecord{}, err
		}
		after, err := inventoryViews(ctx, tx)
		if err != nil {
			_ = tx.Rollback()
			if isSerializationError(err) {
				continue
			}
			return controlRecord{}, err
		}
		if !sameInventory(before, after) {
			_ = tx.Rollback()
			continue
		}
		if err := validateNoEmptyLegacy(ctx, tx, after); err != nil {
			_ = tx.Rollback()
			if isSerializationError(err) {
				continue
			}
			return controlRecord{}, err
		}
		if err := saveControl(ctx, tx, control); err != nil {
			_ = tx.Rollback()
			if isSerializationError(err) {
				continue
			}
			return controlRecord{}, err
		}
		if err := c.emit(ctx, Event{Point: EventControlPrepared}); err != nil {
			_ = tx.Rollback()
			return controlRecord{}, err
		}
		if err := tx.Commit(); err != nil {
			if isSerializationError(err) {
				continue
			}
			return controlRecord{}, fmt.Errorf("memory migration prototype: commit in-progress marker: %w", err)
		}
		if err := c.emit(ctx, Event{Point: EventControlCommitted}); err != nil {
			return controlRecord{}, err
		}
		return control, nil
	}
}

func (c *Coordinator) refreshPlan(ctx context.Context, conn *sql.Conn, control controlRecord) (controlRecord, error) {
	current, err := inventoryViews(ctx, conn)
	if err != nil {
		return control, err
	}
	if err := validateNoEmptyLegacy(ctx, conn, current); err != nil {
		return control, err
	}
	byName := make(map[string]viewPlan, len(current))
	for _, view := range current {
		byName[view.Branch] = view
	}
	changed := false
	for i := range control.Views {
		planned := &control.Views[i]
		now, ok := byName[planned.Branch]
		if !ok {
			return control, &PreflightError{Detail: fmt.Sprintf("planned view %q disappeared; recover it or record an accountable disposition", planned.Branch)}
		}
		delete(byName, planned.Branch)
		complete, err := viewCompleteOnView(ctx, conn, planned.Branch, control.MigrationID)
		if err != nil {
			return control, err
		}
		if complete {
			if !planned.Applied {
				planned.Applied = true
				changed = true
			}
			continue
		}
		if planned.Applied {
			return control, fmt.Errorf("memory migration prototype: completed view %q lost its matching ledger or regained legacy state", planned.Branch)
		}
		if planned.SourceHead != now.SourceHead || planned.ConfigFingerprint != now.ConfigFingerprint {
			return control, &SourceChangedError{Branch: planned.Branch}
		}
	}
	if len(byName) != 0 {
		names := make([]string, 0, len(byName))
		for name := range byName {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			view := byName[name]
			complete, err := viewCompleteOnView(ctx, conn, name, control.MigrationID)
			if err != nil {
				return control, err
			}
			view.Applied = complete
			control.Views = append(control.Views, view)
		}
		changed = true
	}
	sort.Slice(control.Views, func(i, j int) bool { return control.Views[i].Branch < control.Views[j].Branch })
	if !changed {
		return control, nil
	}
	if err := replaceControl(ctx, conn, control); err != nil {
		return control, err
	}
	return control, nil
}

func (c *Coordinator) convertView(ctx context.Context, conn *sql.Conn, control controlRecord, plan viewPlan) (published bool, retErr error) {
	if err := drainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", plan.Branch); err != nil {
		return false, fmt.Errorf("memory migration prototype: checkout view %q: %w", plan.Branch, err)
	}

	// A crash after publication but before advancing the clone-local plan is
	// reconciled from the versioned branch ledger without another commit.
	complete, err := currentViewComplete(ctx, conn, control.MigrationID)
	if err != nil {
		return false, err
	}
	if complete {
		if err := c.markViewApplied(ctx, conn, control.MigrationID, plan.Branch); err != nil {
			return false, err
		}
		return false, nil
	}
	if unsafe, err := unsafeDirtyConfigKeys(ctx, conn); err != nil {
		return false, err
	} else if len(unsafe) != 0 {
		return false, &PreflightError{Detail: fmt.Sprintf(
			"view %q has unrelated config working-set changes at key(s) %s; publish or revert them before resuming",
			plan.Branch, strings.Join(unsafe, ", "))}
	}

	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return false, fmt.Errorf("memory migration prototype: start view transaction: %w", err)
	}
	transactionOpen := true
	var schemaBefore map[string]bool
	publicationAttempted := false
	defer func() {
		if transactionOpen {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			retErr = errors.Join(retErr, rollbackErr)
			// Dolt DDL changes the working root even when the surrounding SQL
			// transaction rolls back. Before publication is attempted, remove
			// only prototype tables that did not exist when this attempt began.
			// Existing tables and all non-prototype working state are untouched.
			if !publicationAttempted && rollbackErr == nil && schemaBefore != nil {
				retErr = errors.Join(retErr, restorePrototypeViewSchema(context.Background(), conn, schemaBefore))
			}
		}
	}()
	// Do not rewrite the branch-qualified ignored writer cell here. Marker
	// installation already serialized with every admitted config writer, and
	// admitted writers refuse once that marker is visible. Keeping this
	// publication transaction on one branch lets Dolt atomically include DDL,
	// canonical rows, legacy retirement, and the ledger. Its config writes still
	// provide the final optimistic conflict check against an unsupported writer.

	var head string
	if err := conn.QueryRowContext(ctx, "SELECT DOLT_HASHOF('HEAD')").Scan(&head); err != nil {
		return false, fmt.Errorf("memory migration prototype: read source head for %q: %w", plan.Branch, err)
	}
	fingerprint, legacy, err := fingerprintCurrentConfig(ctx, conn)
	if err != nil {
		return false, err
	}
	if head != plan.SourceHead || fingerprint != plan.ConfigFingerprint || len(legacy) != plan.LegacyCount {
		return false, &SourceChangedError{Branch: plan.Branch}
	}
	schemaBefore, err = currentPrototypeViewTables(ctx, conn)
	if err != nil {
		return false, err
	}
	// Related views may predate the canonical schema itself. Apply the current
	// throwaway schema only after the source has been revalidated and inside the
	// same transaction that writes canonical rows, retires legacy rows, and
	// publishes the ledger. A fault before DOLT_COMMIT therefore restores the
	// whole historical view, including the absence of these tables.
	if err := applyPrototypeViewSchema(ctx, conn); err != nil {
		return false, fmt.Errorf("memory migration prototype: apply fixture schema on %q: %w", plan.Branch, err)
	}
	for _, memory := range legacy {
		beadID := stableID("mem-", control.ProjectID, memory.userKey)
		revisionID := stableID("mrev-", beadID, memory.body, plan.SourceHead, plan.ConfigFingerprint)
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO memory_a3_prototype_beads
				(id, legacy_key, title, body, origin)
			VALUES (?, ?, ?, ?, 'legacy_migration')`,
			beadID, memory.userKey, memory.userKey, memory.body); err != nil {
			return false, fmt.Errorf("memory migration prototype: insert bead for key %q: %w", memory.userKey, err)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO memory_a3_prototype_revisions
				(id, bead_id, body, change_author, origin, source_head, source_fingerprint)
			VALUES (?, ?, ?, ?, 'legacy_migration', ?, ?)`,
			revisionID, beadID, memory.body, control.Author, plan.SourceHead, plan.ConfigFingerprint); err != nil {
			return false, fmt.Errorf("memory migration prototype: insert revision for key %q: %w", memory.userKey, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM config WHERE `key` LIKE CONCAT(?, '%')", legacyPrefix); err != nil {
		return false, fmt.Errorf("memory migration prototype: delete legacy rows: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO memory_a3_prototype_branch_ledger
			(migration_id, project_id, source_head, source_fingerprint, migrated_count)
		VALUES (?, ?, ?, ?, ?)`,
		control.MigrationID, control.ProjectID, plan.SourceHead, plan.ConfigFingerprint, len(legacy)); err != nil {
		return false, fmt.Errorf("memory migration prototype: write branch ledger: %w", err)
	}

	for _, table := range []string{beadsTable, revisionsTable, branchLedger, "config"} {
		if err := drainCall(ctx, conn, "CALL DOLT_ADD(?)", table); err != nil {
			return false, fmt.Errorf("memory migration prototype: stage %s: %w", table, err)
		}
	}
	if err := c.emit(ctx, Event{Point: EventBranchPrepared, Branch: plan.Branch}); err != nil {
		return false, err
	}
	publicationAttempted = true
	if err := drainCall(ctx, conn,
		"CALL DOLT_COMMIT('-m', ?, '--author', ?)",
		"prototype: migrate legacy memory on "+plan.Branch, control.Author); err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) {
			return false, fmt.Errorf("memory migration prototype: publish view %q: %w", plan.Branch, err)
		}
		return false, &PublicationIndeterminateError{Branch: plan.Branch, Cause: err}
	}
	transactionOpen = false
	if err := c.emit(ctx, Event{Point: EventBranchPublished, Branch: plan.Branch}); err != nil {
		return true, err
	}
	if err := c.markViewApplied(ctx, conn, control.MigrationID, plan.Branch); err != nil {
		return true, err
	}
	return true, nil
}

func (c *Coordinator) markViewApplied(ctx context.Context, conn *sql.Conn, migrationID, branch string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory migration prototype: start progress transaction: %w", err)
	}
	defer tx.Rollback()
	control, found, err := loadControl(ctx, tx)
	if err != nil {
		return err
	}
	if !found || control.MigrationID != migrationID || control.Phase != PhaseInProgress {
		return fmt.Errorf("memory migration prototype: control record changed while advancing %q", branch)
	}
	foundView := false
	for i := range control.Views {
		if control.Views[i].Branch == branch {
			control.Views[i].Applied = true
			foundView = true
			break
		}
	}
	if !foundView {
		return fmt.Errorf("memory migration prototype: view %q missing from control plan", branch)
	}
	if err := saveControl(ctx, tx, control); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory migration prototype: commit view progress: %w", err)
	}
	return nil
}

func (c *Coordinator) markComplete(ctx context.Context, conn *sql.Conn, control controlRecord) (controlRecord, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return control, fmt.Errorf("memory migration prototype: start finalization transaction: %w", err)
	}
	defer tx.Rollback()
	current, found, err := loadControl(ctx, tx)
	if err != nil {
		return control, err
	}
	if !found || current.MigrationID != control.MigrationID || current.Phase != PhaseInProgress {
		return control, fmt.Errorf("memory migration prototype: control record changed before finalization")
	}
	if remaining := remainingViews(current); len(remaining) != 0 {
		return control, &MigrationInProgressError{MigrationID: current.MigrationID, Remaining: remaining}
	}
	if err := touchWriterCell(ctx, tx); err != nil {
		return control, err
	}
	current.Phase = PhaseComplete
	if err := saveControl(ctx, tx, current); err != nil {
		return control, err
	}
	if err := c.emit(ctx, Event{Point: EventCompletePrepared}); err != nil {
		return control, err
	}
	if err := tx.Commit(); err != nil {
		return control, fmt.Errorf("memory migration prototype: commit complete marker: %w", err)
	}
	if err := c.emit(ctx, Event{Point: EventCompleteCommitted}); err != nil {
		return current, err
	}
	return current, nil
}

func (c *Coordinator) auditComplete(ctx context.Context, conn *sql.Conn, control controlRecord) (bool, error) {
	remaining, err := completionGaps(ctx, conn, control)
	if err != nil {
		return false, err
	}
	return len(remaining) == 0, nil
}

func completionGaps(ctx context.Context, db DBConn, control controlRecord) ([]string, error) {
	current, err := inventoryViews(ctx, db)
	if err != nil {
		return nil, err
	}
	var remaining []string
	currentNames := make(map[string]bool, len(current))
	for _, view := range current {
		currentNames[view.Branch] = true
		complete, err := viewCompleteOnView(ctx, db, view.Branch, control.MigrationID)
		if err != nil {
			return nil, err
		}
		if !complete {
			remaining = append(remaining, view.Branch)
		}
	}
	for _, planned := range control.Views {
		if !currentNames[planned.Branch] {
			remaining = append(remaining, planned.Branch)
		}
	}
	sort.Strings(remaining)
	return remaining, nil
}

func (c *Coordinator) reopenForCurrentViews(ctx context.Context, conn *sql.Conn, control controlRecord) (controlRecord, error) {
	control.Phase = PhaseInProgress
	current, err := inventoryViews(ctx, conn)
	if err != nil {
		return control, err
	}
	byName := make(map[string]int, len(control.Views))
	for i := range control.Views {
		byName[control.Views[i].Branch] = i
	}
	for _, view := range current {
		complete, err := viewCompleteOnView(ctx, conn, view.Branch, control.MigrationID)
		if err != nil {
			return control, err
		}
		if i, ok := byName[view.Branch]; ok {
			control.Views[i].Applied = complete
			if !complete {
				control.Views[i].SourceHead = view.SourceHead
				control.Views[i].ConfigFingerprint = view.ConfigFingerprint
				control.Views[i].LegacyCount = view.LegacyCount
			}
			continue
		}
		view.Applied = complete
		control.Views = append(control.Views, view)
	}
	sort.Slice(control.Views, func(i, j int) bool { return control.Views[i].Branch < control.Views[j].Branch })
	if err := replaceControl(ctx, conn, control); err != nil {
		return control, err
	}
	return control, nil
}

func replaceControl(ctx context.Context, conn *sql.Conn, control controlRecord) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory migration prototype: start control update: %w", err)
	}
	defer tx.Rollback()
	if err := saveControl(ctx, tx, control); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory migration prototype: commit control update: %w", err)
	}
	return nil
}

func inventoryViews(ctx context.Context, db DBConn) ([]viewPlan, error) {
	database, err := baseDatabase(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SELECT name, hash FROM dolt_branches ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("memory migration prototype: list related views: %w", err)
	}
	type branchRef struct {
		name string
		head string
	}
	var branches []branchRef
	for rows.Next() {
		var name, head string
		if err := rows.Scan(&name, &head); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("memory migration prototype: scan related view: %w", err)
		}
		branches = append(branches, branchRef{name: name, head: head})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("memory migration prototype: iterate related views: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("memory migration prototype: close related views: %w", err)
	}

	views := make([]viewPlan, 0, len(branches))
	for _, branch := range branches {
		fingerprint, legacy, err := fingerprintConfigOnView(ctx, db, database, branch.name)
		if err != nil {
			return nil, err
		}
		views = append(views, viewPlan{
			Branch:            branch.name,
			SourceHead:        branch.head,
			ConfigFingerprint: fingerprint,
			LegacyCount:       len(legacy),
		})
	}
	return views, nil
}

func fingerprintCurrentConfig(ctx context.Context, db DBConn) (string, []legacyMemory, error) {
	return fingerprintConfigQuery(ctx, db, "SELECT `key`, value FROM config ORDER BY `key`")
}

func fingerprintConfigOnView(ctx context.Context, db DBConn, database, branch string) (string, []legacyMemory, error) {
	query := "SELECT `key`, value FROM " + qualifiedViewTable(database, branch, "config") + " ORDER BY `key`"
	return fingerprintConfigQuery(ctx, db, query)
}

func fingerprintConfigQuery(ctx context.Context, db DBConn, query string) (string, []legacyMemory, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", nil, fmt.Errorf("memory migration prototype: inspect config source: %w", err)
	}
	defer rows.Close()
	h := sha256.New()
	var legacy []legacyMemory
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return "", nil, fmt.Errorf("memory migration prototype: scan config source: %w", err)
		}
		writeDigestPart(h, key)
		writeDigestPart(h, value)
		if userKey, ok := strings.CutPrefix(key, legacyPrefix); ok {
			legacy = append(legacy, legacyMemory{userKey: userKey, body: value})
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, fmt.Errorf("memory migration prototype: iterate config source: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), legacy, nil
}

func unsafeDirtyConfigKeys(ctx context.Context, db DBConn) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT COALESCE(to_key, from_key) FROM dolt_diff('HEAD', 'WORKING', 'config')")
	if err != nil {
		return nil, fmt.Errorf("memory migration prototype: inspect config working set: %w", err)
	}
	defer rows.Close()
	var unsafe []string
	for rows.Next() {
		var key sql.NullString
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("memory migration prototype: scan config working-set key: %w", err)
		}
		if key.Valid && !strings.HasPrefix(key.String, legacyPrefix) {
			unsafe = append(unsafe, key.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory migration prototype: iterate config working set: %w", err)
	}
	sort.Strings(unsafe)
	return unsafe, nil
}

type byteWriter interface{ Write([]byte) (int, error) }

func writeDigestPart(w byteWriter, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = w.Write(size[:])
	_, _ = w.Write([]byte(value))
}

func validatePreflight(views []viewPlan) error {
	if len(views) == 0 {
		return &PreflightError{Detail: "provider exposes no related branch view"}
	}
	return nil
}

func validateNoEmptyLegacy(ctx context.Context, db DBConn, views []viewPlan) error {
	database, err := baseDatabase(ctx, db)
	if err != nil {
		return err
	}
	for _, view := range views {
		_, legacy, err := fingerprintConfigOnView(ctx, db, database, view.Branch)
		if err != nil {
			return err
		}
		for _, memory := range legacy {
			if memory.body == "" {
				return &PreflightError{Detail: fmt.Sprintf("legacy entry %q is empty; supply replacement state or an accountable discard before retrying", memory.userKey)}
			}
		}
	}
	return nil
}

func sameInventory(a, b []viewPlan) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Branch != b[i].Branch || a[i].SourceHead != b[i].SourceHead ||
			a[i].ConfigFingerprint != b[i].ConfigFingerprint || a[i].LegacyCount != b[i].LegacyCount {
			return false
		}
	}
	return true
}

func viewCompleteOnView(ctx context.Context, db DBConn, branch, migrationID string) (bool, error) {
	database, err := baseDatabase(ctx, db)
	if err != nil {
		return false, err
	}
	var migrated int
	var sourceHead, sourceFingerprint string
	err = db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT migrated_count, source_head, source_fingerprint
		FROM %s
		WHERE migration_id = ?`, qualifiedViewTable(database, branch, branchLedger)), migrationID).
		Scan(&migrated, &sourceHead, &sourceFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if isTableNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect ledger on %q: %w", branch, err)
	}
	var legacyCount, beadCount, revisionCount int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+qualifiedViewTable(database, branch, "config")+" WHERE `key` LIKE CONCAT(?, '%')", legacyPrefix).Scan(&legacyCount); err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect legacy rows on %q: %w", branch, err)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+qualifiedViewTable(database, branch, beadsTable)+" WHERE origin = 'legacy_migration'").Scan(&beadCount); err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect canonical beads on %q: %w", branch, err)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+qualifiedViewTable(database, branch, revisionsTable)+
			" WHERE origin = 'legacy_migration' AND source_head = ? AND source_fingerprint = ?",
		sourceHead, sourceFingerprint).Scan(&revisionCount); err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect canonical revisions on %q: %w", branch, err)
	}
	return legacyCount == 0 && beadCount == migrated && revisionCount == migrated, nil
}

func currentViewComplete(ctx context.Context, db DBConn, migrationID string) (bool, error) {
	var migrated int
	var sourceHead, sourceFingerprint string
	err := db.QueryRowContext(ctx,
		"SELECT migrated_count, source_head, source_fingerprint FROM "+branchLedger+" WHERE migration_id = ?", migrationID).
		Scan(&migrated, &sourceHead, &sourceFingerprint)
	if errors.Is(err, sql.ErrNoRows) || isTableNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect current branch ledger: %w", err)
	}
	var legacyCount, beadCount, revisionCount int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", legacyPrefix).Scan(&legacyCount); err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect current legacy rows: %w", err)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+beadsTable+" WHERE origin = 'legacy_migration'").Scan(&beadCount); err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect current canonical beads: %w", err)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+revisionsTable+
			" WHERE origin = 'legacy_migration' AND source_head = ? AND source_fingerprint = ?",
		sourceHead, sourceFingerprint).Scan(&revisionCount); err != nil {
		return false, fmt.Errorf("memory migration prototype: inspect current canonical revisions: %w", err)
	}
	return legacyCount == 0 && beadCount == migrated && revisionCount == migrated, nil
}

func loadControl(ctx context.Context, db DBConn) (controlRecord, bool, error) {
	plane, found, err := locateControlPlane(ctx, db)
	if err != nil || !found {
		return controlRecord{}, false, err
	}
	var raw string
	err = db.QueryRowContext(ctx,
		"SELECT value FROM "+plane.table+" WHERE `key` = ?", controlKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return controlRecord{}, false, nil
	}
	if err != nil {
		return controlRecord{}, false, fmt.Errorf("memory migration prototype: read control record: %w", err)
	}
	var control controlRecord
	if err := json.Unmarshal([]byte(raw), &control); err != nil {
		return controlRecord{}, false, fmt.Errorf("memory migration prototype: decode control record: %w", err)
	}
	if err := validateControlRecord(control); err != nil {
		return controlRecord{}, false, err
	}
	return control, true, nil
}

func validateControlRecord(control controlRecord) error {
	if control.Scope != migrationScope {
		return fmt.Errorf("memory migration prototype: unsupported control scope %q", control.Scope)
	}
	if control.MigrationID == "" || control.ProjectID == "" || !validAuthor(control.Author) {
		return fmt.Errorf("memory migration prototype: incomplete clone-local control record")
	}
	if control.Phase != PhaseInProgress && control.Phase != PhaseComplete {
		return fmt.Errorf("memory migration prototype: unsupported control phase %q", control.Phase)
	}
	if len(control.Views) == 0 {
		return fmt.Errorf("memory migration prototype: control record has no related view")
	}
	seen := make(map[string]bool, len(control.Views))
	for _, view := range control.Views {
		if view.Branch == "" || view.SourceHead == "" || view.ConfigFingerprint == "" || seen[view.Branch] {
			return fmt.Errorf("memory migration prototype: invalid view plan for %q", view.Branch)
		}
		seen[view.Branch] = true
		if control.Phase == PhaseComplete && !view.Applied {
			return fmt.Errorf("memory migration prototype: complete control record has unfinished view %q", view.Branch)
		}
	}
	return nil
}

func saveControl(ctx context.Context, db DBConn, control controlRecord) error {
	raw, err := json.Marshal(control)
	if err != nil {
		return fmt.Errorf("memory migration prototype: encode control record: %w", err)
	}
	plane, found, err := locateControlPlane(ctx, db)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("memory migration prototype: clone-local control plane is not prepared")
	}
	if _, err := db.ExecContext(ctx,
		"REPLACE INTO "+plane.table+" (`key`, value) VALUES (?, ?)", controlKey, string(raw)); err != nil {
		return fmt.Errorf("memory migration prototype: write control record: %w", err)
	}
	return nil
}

func touchWriterCell(ctx context.Context, db DBConn) error {
	plane, found, err := locateControlPlane(ctx, db)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("memory migration prototype: clone-local control plane is not prepared")
	}
	if _, err := db.ExecContext(ctx,
		"REPLACE INTO "+plane.table+" (`key`, value) VALUES (?, ?)", writerCellKey, uuid.NewString()); err != nil {
		return fmt.Errorf("memory migration prototype: coordinate config writer: %w", err)
	}
	return nil
}

type controlPlane struct {
	branch string
	table  string
}

// locateControlPlane discovers the one branch-qualified ignored working table
// carrying the writer cell. A late branch made from historical HEAD normally
// has no local_metadata table at all; scanning the current ref inventory keeps
// its gate attached to the existing clone-local marker instead of interpreting
// absence as permission to serve legacy state.
func locateControlPlane(ctx context.Context, db DBConn) (controlPlane, bool, error) {
	database, err := baseDatabase(ctx, db)
	if err != nil {
		return controlPlane{}, false, err
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM dolt_branches ORDER BY name")
	if err != nil {
		return controlPlane{}, false, fmt.Errorf("memory migration prototype: list control-plane views: %w", err)
	}
	var branches []string
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch); err != nil {
			_ = rows.Close()
			return controlPlane{}, false, fmt.Errorf("memory migration prototype: scan control-plane view: %w", err)
		}
		branches = append(branches, branch)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return controlPlane{}, false, fmt.Errorf("memory migration prototype: iterate control-plane views: %w", err)
	}
	if err := rows.Close(); err != nil {
		return controlPlane{}, false, fmt.Errorf("memory migration prototype: close control-plane views: %w", err)
	}

	var located *controlPlane
	for _, branch := range branches {
		plane := controlPlane{
			branch: branch,
			table:  qualifiedViewTable(database, branch, "local_metadata"),
		}
		var count int
		err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+plane.table+" WHERE `key` IN (?, ?)", writerCellKey, controlKey).Scan(&count)
		if isTableNotFound(err) {
			continue
		}
		if err != nil {
			return controlPlane{}, false, fmt.Errorf("memory migration prototype: inspect control plane on %q: %w", branch, err)
		}
		if count == 0 {
			continue
		}
		if located != nil && located.branch != branch {
			return controlPlane{}, false, fmt.Errorf("memory migration prototype: ambiguous clone-local control planes on %q and %q", located.branch, branch)
		}
		copy := plane
		located = &copy
	}
	if located == nil {
		return controlPlane{}, false, nil
	}
	return *located, true, nil
}

// findMigrationEvidence prevents loss of the ignored control anchor from
// turning already-converted branch history back into permission to serve an
// old legacy view. The versioned per-branch ledger is recovery evidence, not
// the phase authority; without the phase record the only safe answer is typed
// unavailable.
func findMigrationEvidence(ctx context.Context, db DBConn) (migrationID string, views []string, found bool, retErr error) {
	database, err := baseDatabase(ctx, db)
	if err != nil {
		return "", nil, false, err
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM dolt_branches ORDER BY name")
	if err != nil {
		return "", nil, false, fmt.Errorf("memory migration prototype: list recovery-evidence views: %w", err)
	}
	var branches []string
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch); err != nil {
			_ = rows.Close()
			return "", nil, false, fmt.Errorf("memory migration prototype: scan recovery-evidence view: %w", err)
		}
		branches = append(branches, branch)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return "", nil, false, fmt.Errorf("memory migration prototype: iterate recovery-evidence views: %w", err)
	}
	if err := rows.Close(); err != nil {
		return "", nil, false, fmt.Errorf("memory migration prototype: close recovery-evidence views: %w", err)
	}

	for _, branch := range branches {
		var candidate string
		err := db.QueryRowContext(ctx,
			"SELECT migration_id FROM "+qualifiedViewTable(database, branch, branchLedger)+" LIMIT 1").Scan(&candidate)
		switch {
		case errors.Is(err, sql.ErrNoRows), isTableNotFound(err):
			continue
		case err != nil:
			return "", nil, false, fmt.Errorf("memory migration prototype: inspect recovery evidence on %q: %w", branch, err)
		case migrationID != "" && migrationID != candidate:
			return "", nil, false, fmt.Errorf("memory migration prototype: conflicting migration evidence %q and %q", migrationID, candidate)
		default:
			migrationID = candidate
			found = true
		}
	}
	if found {
		views = append(views, branches...)
	}
	return migrationID, views, found, nil
}

func baseDatabase(ctx context.Context, db DBConn) (string, error) {
	var database string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return "", fmt.Errorf("memory migration prototype: identify database: %w", err)
	}
	if slash := strings.IndexByte(database, '/'); slash >= 0 {
		database = database[:slash]
	}
	return database, nil
}

func qualifiedViewTable(database, branch, table string) string {
	return quoteIdentifier(database+"/"+branch) + "." + quoteIdentifier(table)
}

func remainingViews(control controlRecord) []string {
	remaining := make([]string, 0, len(control.Views))
	for _, view := range control.Views {
		if !view.Applied {
			remaining = append(remaining, view.Branch)
		}
	}
	sort.Strings(remaining)
	return remaining
}

func inProgress(control controlRecord) error {
	return &MigrationInProgressError{MigrationID: control.MigrationID, Remaining: remainingViews(control)}
}

func (c *Coordinator) emit(ctx context.Context, event Event) error {
	if c.options.OnEvent == nil {
		return nil
	}
	return c.options.OnEvent(ctx, event)
}

func validAuthor(author string) bool {
	open := strings.LastIndex(author, " <")
	return open > 0 && strings.HasSuffix(author, ">") && open+2 < len(author)-1
}

func stableID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		writeDigestPart(h, part)
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:32]
}

func isSerializationError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205)
}

func isTableNotFound(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1146
}

func quoteIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func drainCall(ctx context.Context, db DBConn, query string, args ...any) error {
	return schema.DrainCall(ctx, db, query, args...)
}

func prototypeViewSchemaStatements() [3]string {
	return [3]string{
		`CREATE TABLE IF NOT EXISTS memory_a3_prototype_beads (
			id VARCHAR(64) PRIMARY KEY,
			legacy_key VARCHAR(255) NOT NULL UNIQUE,
			title VARCHAR(500) NOT NULL,
			body LONGTEXT NOT NULL,
			origin VARCHAR(32) NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memory_a3_prototype_revisions (
			id VARCHAR(64) PRIMARY KEY,
			bead_id VARCHAR(64) NOT NULL,
			body LONGTEXT NOT NULL,
			change_author VARCHAR(255) NOT NULL,
			origin VARCHAR(32) NOT NULL,
			source_head VARCHAR(64) NOT NULL,
			source_fingerprint VARCHAR(64) NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS memory_a3_prototype_branch_ledger (
			migration_id VARCHAR(64) PRIMARY KEY,
			project_id VARCHAR(64) NOT NULL,
			source_head VARCHAR(64) NOT NULL,
			source_fingerprint VARCHAR(64) NOT NULL,
			migrated_count INT NOT NULL
		)`,
	}
}

// applyPrototypeViewSchema applies the prototype's current branch-versioned
// schema to whichever view owns db's working set. The fixture has one schema
// revision, so CREATE IF NOT EXISTS covers both an already-current view and a
// historical view with no canonical tables. A production implementation would
// run its provider-private schema upgrade sequence at this seam.
func applyPrototypeViewSchema(ctx context.Context, db DBConn) error {
	for _, statement := range prototypeViewSchemaStatements() {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("install fixture schema: %w", err)
		}
	}
	return nil
}

func currentPrototypeViewTables(ctx context.Context, db DBConn) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name IN (?, ?, ?)`, beadsTable, revisionsTable, branchLedger)
	if err != nil {
		return nil, fmt.Errorf("inspect fixture schema: %w", err)
	}
	defer rows.Close()
	present := make(map[string]bool, 3)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, fmt.Errorf("scan fixture schema: %w", err)
		}
		present[table] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fixture schema: %w", err)
	}
	return present, nil
}

func restorePrototypeViewSchema(ctx context.Context, db DBConn, before map[string]bool) error {
	for _, table := range []string{beadsTable, revisionsTable, branchLedger} {
		if before[table] {
			continue
		}
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+quoteIdentifier(table)); err != nil {
			return fmt.Errorf("restore absent fixture table %s: %w", table, err)
		}
	}
	return nil
}

// InstallPrototypeSchema creates only the throwaway branch-versioned tables
// needed by this executable evidence. It is called by isolated tests and is not
// registered with the production schema runner.
func InstallPrototypeSchema(ctx context.Context, conn *sql.Conn) error {
	if err := applyPrototypeViewSchema(ctx, conn); err != nil {
		return fmt.Errorf("memory migration prototype: %w", err)
	}
	for _, table := range []string{beadsTable, revisionsTable, branchLedger} {
		if err := drainCall(ctx, conn, "CALL DOLT_ADD(?)", table); err != nil {
			return fmt.Errorf("memory migration prototype: stage fixture table %s: %w", table, err)
		}
	}
	if err := drainCall(ctx, conn,
		"CALL DOLT_COMMIT('-m', 'prototype: install A3 migration tables', '--author', ?)", prototypeAuthor); err != nil {
		return fmt.Errorf("memory migration prototype: commit fixture schema: %w", err)
	}
	return PrepareControlPlane(ctx, conn)
}

// ResetPrototypeControl removes only clone-local prototype coordination state.
// Tests use it when they deliberately install the fixture schema on a reused
// database. It does not touch branch-versioned memory or config state.
func ResetPrototypeControl(ctx context.Context, db DBConn) error {
	plane, found, err := locateControlPlane(ctx, db)
	if err != nil {
		return err
	}
	if found {
		if _, err := db.ExecContext(ctx,
			"DELETE FROM "+plane.table+" WHERE `key` IN (?, ?)", controlKey, writerCellKey); err != nil {
			return fmt.Errorf("memory migration prototype: reset control state: %w", err)
		}
	}
	return PrepareControlPlane(ctx, db)
}

// LegacyPrefix returns the existing compatibility namespace for black-box
// spike fixtures. It does not define a new product value.
func LegacyPrefix() string { return legacyPrefix }

// Prototype table accessors keep test SQL from duplicating private fixture
// names while making clear that none of them is a production schema contract.
func PrototypeBeadsTable() string     { return beadsTable }
func PrototypeRevisionsTable() string { return revisionsTable }
func PrototypeLedgerTable() string    { return branchLedger }
