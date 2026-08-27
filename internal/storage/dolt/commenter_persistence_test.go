package dolt

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// This file is the persistence tier of the Commenter role: what the shared
// contract in package conformance deliberately leaves out because it is a
// property of the Dolt working set rather than a promised result of the
// operation. It is pinned at this backend only.
//
//   - The commit-message spelling is single-sourced in
//     storageissueops.AddCommentCommitMessage, so one backend pinning it is
//     enough and re-pinning it three times would be duplication.
//   - The staging assertions need a planted dirty working set and a dolt_status
//     read. The working set is not a caller-visible thing on the unit-of-work
//     route, which is where the same line was already drawn for Lifecycle in
//     conformance/issue_operations_staging.go.
//
// Everything else the old commenter_test.go asserted — verbatim text, the
// result mirroring the row, wisp-thread routing, ErrNotFound — is now
// conformance.RunCommenter*, wired at all three backends.

// TestDoltStoreCommenterNamesTheIssueInHistoryAndCommitsTheTable pins the two
// persistence halves of a durable comment: the history entry is named
// "bd: comment <id>", and the comments table is committed rather than left
// dirty in the working set.
func TestDoltStoreCommenterNamesTheIssueInHistoryAndCommitsTheTable(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	seedIssues(ctx, t, store, "test-comment-role")

	commenter, err := store.Commenter()
	if err != nil {
		t.Fatalf("Commenter(): %v", err)
	}
	if _, err := commenter.AddComment(ctx, publicops.AddCommentRequest{
		Author:  "author",
		IssueID: "test-comment-role",
		Text:    "a durable comment",
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if !doltHasCommitMessage(ctx, t, store, "bd: comment test-comment-role") {
		t.Error("the comment did not name its issue in history")
	}
	requireCleanTables(ctx, t, store, "comments")
}

func TestDoltStoreCommenterEditsAndDeletesByBothIdentities(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	enableJournalForTest(t, store)
	clearJournal(t, store)
	seedIssues(ctx, t, store, "test-comment-edit", "test-comment-other")

	commenter, err := store.Commenter()
	if err != nil {
		t.Fatalf("Commenter(): %v", err)
	}
	if _, err := commenter.EditComment(ctx, publicops.EditCommentRequest{
		IssueID: "test-comment-edit", CommentID: "missing", Text: "text",
	}); !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("edit without actor error = %v, want ErrValidation", err)
	}
	if _, err := commenter.DeleteComment(ctx, publicops.DeleteCommentRequest{
		IssueID: "test-comment-edit", CommentID: "missing",
	}); !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("delete without actor error = %v, want ErrValidation", err)
	}
	added, err := commenter.AddComment(ctx, publicops.AddCommentRequest{
		Author: "author", IssueID: "test-comment-edit", Text: "before",
	})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	edited, err := commenter.EditComment(ctx, publicops.EditCommentRequest{
		IssueID: "test-comment-edit", CommentID: added.Comment.ID, Text: "after", Actor: "editor",
	})
	if err != nil {
		t.Fatalf("EditComment: %v", err)
	}
	if edited.Comment.ID != added.Comment.ID || edited.Comment.IssueID != added.Comment.IssueID || edited.Comment.Author != added.Comment.Author {
		t.Fatalf("edited identity fields = %+v, want %+v", edited.Comment, added.Comment)
	}
	if edited.Comment.Text != "after" || !edited.Comment.CreatedAt.Equal(added.Comment.CreatedAt) {
		t.Fatalf("edited comment = %+v, want text changed and timestamp preserved", edited.Comment)
	}
	if !doltHasCommitMessage(ctx, t, store, "bd: edit comment test-comment-edit "+added.Comment.ID) {
		t.Error("comment edit did not create its durable history entry")
	}
	var editActor string
	var editJSON []byte
	if err := store.db.QueryRowContext(ctx, `SELECT actor, comment_json FROM bd_events_journal WHERE op = 'comment' AND issue_id = ? ORDER BY seq DESC LIMIT 1`, "test-comment-edit").Scan(&editActor, &editJSON); err != nil {
		t.Fatalf("read edit journal entry: %v", err)
	}
	var editedPayload struct {
		Author string `json:"author"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(editJSON, &editedPayload); err != nil {
		t.Fatalf("decode edit journal entry: %v", err)
	}
	if editActor != "editor" || editedPayload.Author != "author" || editedPayload.Text != "after" {
		t.Fatalf("edit journal = actor %q payload %+v, want actor editor and original author", editActor, editedPayload)
	}

	deleted, err := commenter.DeleteComment(ctx, publicops.DeleteCommentRequest{
		IssueID: "test-comment-edit", CommentID: added.Comment.ID, Actor: "deleter",
	})
	if err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if deleted.IssueID != "test-comment-edit" || deleted.CommentID != added.Comment.ID {
		t.Fatalf("delete result = %+v", deleted)
	}
	comments, err := store.GetIssueComments(ctx, "test-comment-edit")
	if err != nil {
		t.Fatalf("GetIssueComments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("comments after delete = %d, want 0", len(comments))
	}
	if !doltHasCommitMessage(ctx, t, store, "bd: delete comment test-comment-edit "+added.Comment.ID) {
		t.Error("comment delete did not create its durable history entry")
	}
	var deleteActor string
	var commentJSON []byte
	if err := store.db.QueryRowContext(ctx, `SELECT actor, comment_json FROM bd_events_journal WHERE op = 'comment' AND issue_id = ? ORDER BY seq DESC LIMIT 1`, "test-comment-edit").Scan(&deleteActor, &commentJSON); err != nil {
		t.Fatalf("read delete journal entry: %v", err)
	}
	var tombstone struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(commentJSON, &tombstone); err != nil {
		t.Fatalf("decode delete journal entry: %v", err)
	}
	if deleteActor != "deleter" || tombstone.ID != added.Comment.ID || !tombstone.Deleted {
		t.Fatalf("delete journal entry = %+v, want tombstone for %s", tombstone, added.Comment.ID)
	}

	if _, err := commenter.EditComment(ctx, publicops.EditCommentRequest{
		IssueID: "test-comment-edit", CommentID: added.Comment.ID, Text: "   ", Actor: "editor",
	}); !errors.Is(err, storage.ErrValidation) {
		t.Fatalf("blank replacement error = %v, want ErrValidation", err)
	}
	if _, err := commenter.DeleteComment(ctx, publicops.DeleteCommentRequest{
		IssueID: "test-comment-other", CommentID: added.Comment.ID, Actor: "deleter",
	}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("comment on another issue error = %v, want ErrNotFound", err)
	}
	if _, err := commenter.DeleteComment(ctx, publicops.DeleteCommentRequest{
		IssueID: "test-comment-missing", CommentID: added.Comment.ID, Actor: "deleter",
	}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing issue error = %v, want ErrNotFound", err)
	}

	_, err = commenter.EditComment(ctx, publicops.EditCommentRequest{
		IssueID: "test-comment-edit", CommentID: added.Comment.ID, Text: "again", Actor: "editor",
	})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("editing deleted comment error = %v, want ErrNotFound", err)
	}
}

// TestDoltStoreCommenterOnAWispSweepsNoPendingRow pins the staging half of the
// ephemeral case, which no data read can see.
//
// It needs a pending row in the durable comments table to be visible at all.
// The wisp tables are dolt-ignored, so a commit that named one is swallowed
// and a commit count alone cannot tell a correctly-staged wisp write from one
// that reached for `comments` — which is exactly what a wrongly resolved plane
// would do while the thread read still passed. A comment on a wisp must
// therefore leave `comments` dirty: it neither wrote to it nor swept someone
// else's pending row into a commit.
func TestDoltStoreCommenterOnAWispSweepsNoPendingRow(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	seedIssues(ctx, t, store, "test-comment-neighbor")
	wisp := &types.Issue{ID: "test-comment-wisp", Title: "wisp", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true}
	if err := store.CreateIssue(ctx, wisp, "seed"); err != nil {
		t.Fatalf("create wisp: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO comments (id, issue_id, author, text, created_at)
		VALUES (?, ?, ?, ?, NOW())`,
		"00000000-0000-4000-8000-00000000c0de", "test-comment-neighbor", "someone-else", "pending",
	); err != nil {
		t.Fatalf("stage a pending comment row: %v", err)
	}

	commenter, err := store.Commenter()
	if err != nil {
		t.Fatalf("Commenter(): %v", err)
	}
	added, err := commenter.AddComment(ctx, publicops.AddCommentRequest{
		Author:  "author",
		IssueID: wisp.ID,
		Text:    "on the ephemeral plane",
	})
	if err != nil {
		t.Fatalf("AddComment on a wisp: %v", err)
	}
	if _, err := commenter.EditComment(ctx, publicops.EditCommentRequest{
		IssueID: wisp.ID, CommentID: added.Comment.ID, Text: "edited on the ephemeral plane", Actor: "editor",
	}); err != nil {
		t.Fatalf("EditComment on a wisp: %v", err)
	}
	if _, err := commenter.DeleteComment(ctx, publicops.DeleteCommentRequest{
		IssueID: wisp.ID, CommentID: added.Comment.ID, Actor: "deleter",
	}); err != nil {
		t.Fatalf("DeleteComment on a wisp: %v", err)
	}
	if doltHasCommitMessage(ctx, t, store, "bd: edit comment "+wisp.ID+" "+added.Comment.ID) || doltHasCommitMessage(ctx, t, store, "bd: delete comment "+wisp.ID+" "+added.Comment.ID) {
		t.Fatal("wisp comment mutation created durable history")
	}

	var dirty int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name = 'comments'").Scan(&dirty); err != nil {
		t.Fatalf("query dolt_status: %v", err)
	}
	if dirty == 0 {
		t.Fatal("comments is clean: the wisp comment staged the durable table and carried a pending row with it")
	}
}
