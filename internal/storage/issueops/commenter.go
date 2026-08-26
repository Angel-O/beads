package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// ValidateAddCommentRequest applies the request rules every Commenter
// implementation shares.
//
// Blankness is decided on a TRIMMED copy and the request's own Text is left
// alone: a comment of nothing but whitespace carries no information and is
// almost always a shell quoting accident, but a comment that merely begins
// with a newline is a comment.
func ValidateAddCommentRequest(request publicops.AddCommentRequest) error {
	if request.Author == "" {
		return fmt.Errorf("%w: add comment requires an author", storage.ErrValidation)
	}
	if request.IssueID == "" {
		return fmt.Errorf("%w: add comment requires an issue ID", storage.ErrValidation)
	}
	if strings.TrimSpace(request.Text) == "" {
		return fmt.Errorf("%w: comment text cannot be empty", storage.ErrValidation)
	}
	return nil
}

func ValidateEditCommentRequest(request publicops.EditCommentRequest) error {
	if request.IssueID == "" {
		return fmt.Errorf("%w: edit comment requires an issue ID", storage.ErrValidation)
	}
	if request.CommentID == "" {
		return fmt.Errorf("%w: edit comment requires a comment ID", storage.ErrValidation)
	}
	if strings.TrimSpace(request.Text) == "" {
		return fmt.Errorf("%w: comment text cannot be empty", storage.ErrValidation)
	}
	return nil
}

func ValidateDeleteCommentRequest(request publicops.DeleteCommentRequest) error {
	if request.IssueID == "" {
		return fmt.Errorf("%w: delete comment requires an issue ID", storage.ErrValidation)
	}
	if request.CommentID == "" {
		return fmt.Errorf("%w: delete comment requires a comment ID", storage.ErrValidation)
	}
	return nil
}

// AddCommentCommitMessage is the history entry a comment records. It is the
// spelling both stores' own AddIssueComment already wrote.
func AddCommentCommitMessage(issueID string) string {
	return "bd: comment " + issueID
}

func EditCommentCommitMessage(issueID, commentID string) string {
	return "bd: edit comment " + issueID + " " + commentID
}

func DeleteCommentCommitMessage(issueID, commentID string) string {
	return "bd: delete comment " + issueID + " " + commentID
}

// ExecuteAddComment appends one comment in tx and reports the durable tables
// changed. It is the store-backed body behind the Commenter accessor; the
// unit-of-work provider has its own, for the reason Lifecycle does.
//
// A comment on an ephemeral row changes only wisp_comments, which
// ChangedTables drops, so the caller's transaction commits nothing and records
// no history entry: the wisp tables are dolt-ignored and there is nothing to
// version.
func ExecuteAddComment(ctx context.Context, tx *sql.Tx, request publicops.AddCommentRequest) (publicops.AddCommentResult, ChangedTables, error) {
	commentTable, err := resolveCommentPlaneInTx(ctx, tx, request.IssueID)
	if err != nil {
		return publicops.AddCommentResult{}, nil, err
	}
	comment, err := AddIssueCommentInTx(ctx, tx, request.IssueID, request.Author, request.Text)
	if err != nil {
		return publicops.AddCommentResult{}, nil, err
	}
	tables := ChangedTables{}
	tables.Add(commentTable)
	return publicops.AddCommentResult{Comment: comment}, tables, nil
}

func ExecuteEditComment(ctx context.Context, tx *sql.Tx, request publicops.EditCommentRequest) (publicops.EditCommentResult, ChangedTables, error) {
	isWisp := IsActiveWispInTx(ctx, tx, request.IssueID)
	comment, err := EditCommentInTx(ctx, tx, request.IssueID, request.CommentID, request.Text, isWisp)
	if err != nil {
		return publicops.EditCommentResult{}, nil, err
	}
	tables := ChangedTables{}
	tables.Add(commentTable(isWisp))
	return publicops.EditCommentResult{Comment: comment}, tables, nil
}

func ExecuteDeleteComment(ctx context.Context, tx *sql.Tx, request publicops.DeleteCommentRequest) (publicops.DeleteCommentResult, ChangedTables, error) {
	isWisp := IsActiveWispInTx(ctx, tx, request.IssueID)
	result, err := DeleteCommentInTx(ctx, tx, request.IssueID, request.CommentID, isWisp)
	if err != nil {
		return publicops.DeleteCommentResult{}, nil, err
	}
	tables := ChangedTables{}
	tables.Add(commentTable(isWisp))
	return result, tables, nil
}

func commentTable(useWisp bool) string {
	if useWisp {
		return "wisp_comments"
	}
	return "comments"
}

// EditCommentInTx replaces a comment while preserving its author, identity and
// creation timestamp. The issue and comment checks share the write transaction.
//
//nolint:gosec // G201: table names are hardcoded routing constants
func EditCommentInTx(ctx context.Context, tx DBTX, issueID, commentID, text string, useWisp bool) (*types.Comment, error) {
	table := commentTable(useWisp)
	if err := requireCommentIssue(ctx, tx, issueID, useWisp); err != nil {
		return nil, err
	}
	comment, err := getCommentInTx(ctx, tx, table, issueID, commentID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET text = ? WHERE id = ? AND issue_id = ?", table), text, commentID, issueID); err != nil {
		return nil, fmt.Errorf("edit comment: %w", err)
	}
	comment.Text = text
	if err := RecordCommentEventInTx(ctx, tx, issueID, &EventComment{
		ID: comment.ID, Author: comment.Author, Text: comment.Text, CreatedAt: comment.CreatedAt, Source: CommentSourceStructured,
	}); err != nil {
		return nil, err
	}
	return comment, nil
}

// DeleteCommentInTx removes a comment and journals its tombstone.
//
//nolint:gosec // G201: table names are hardcoded routing constants
func DeleteCommentInTx(ctx context.Context, tx DBTX, issueID, commentID string, useWisp bool) (publicops.DeleteCommentResult, error) {
	table := commentTable(useWisp)
	if err := requireCommentIssue(ctx, tx, issueID, useWisp); err != nil {
		return publicops.DeleteCommentResult{}, err
	}
	comment, err := getCommentInTx(ctx, tx, table, issueID, commentID)
	if err != nil {
		return publicops.DeleteCommentResult{}, err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = ? AND issue_id = ?", table), commentID, issueID); err != nil {
		return publicops.DeleteCommentResult{}, fmt.Errorf("delete comment: %w", err)
	}
	if err := RecordCommentEventInTx(ctx, tx, issueID, &EventComment{
		ID: comment.ID, Author: comment.Author, Text: comment.Text, CreatedAt: comment.CreatedAt,
		Source: CommentSourceStructured, Deleted: true,
	}); err != nil {
		return publicops.DeleteCommentResult{}, err
	}
	return publicops.DeleteCommentResult{IssueID: issueID, CommentID: comment.ID}, nil
}

//nolint:gosec // G201: table names are hardcoded routing constants
func requireCommentIssue(ctx context.Context, tx DBTX, issueID string, useWisp bool) error {
	issueTable, _, _, _ := WispTableRouting(useWisp)
	var exists bool
	if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = ?)", issueTable), issueID).Scan(&exists); err != nil {
		return fmt.Errorf("check issue existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: issue %s", storage.ErrNotFound, issueID)
	}
	return nil
}

//nolint:gosec // G201: table names are hardcoded routing constants
func getCommentInTx(ctx context.Context, tx DBTX, table, issueID, commentID string) (*types.Comment, error) {
	var comment types.Comment
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id, issue_id, author, text, created_at FROM %s WHERE id = ? AND issue_id = ?`, table), commentID, issueID).
		Scan(&comment.ID, &comment.IssueID, &comment.Author, &comment.Text, &comment.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: comment %s", storage.ErrNotFound, commentID)
	}
	if err != nil {
		return nil, fmt.Errorf("get comment: %w", err)
	}
	return &comment, nil
}

// resolveCommentPlaneInTx names the comment table the anchor's thread lives in,
// refusing an id that names neither an issue nor a wisp.
//
// The existence probe is here rather than left to the insert's own so the
// refusal is TYPED: AddIssueCommentInTx reports a missing anchor as prose, and
// a caller of this role classifies with errors.Is. It resolves the plane in
// the same transaction the insert runs in, so a comment cannot land on a row
// an earlier read saw and this one did not.
//
//nolint:gosec // G201: issueTable comes from WispTableRouting ("issues" or "wisps")
func resolveCommentPlaneInTx(ctx context.Context, tx *sql.Tx, issueID string) (string, error) {
	isWisp := IsActiveWispInTx(ctx, tx, issueID)
	issueTable, _, _, _ := WispTableRouting(isWisp)
	var exists bool
	if err := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE id = ?)`, issueTable), issueID).Scan(&exists); err != nil {
		return "", fmt.Errorf("check issue existence: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("%w: issue %s", storage.ErrNotFound, issueID)
	}
	if isWisp {
		return "wisp_comments", nil
	}
	return "comments", nil
}
