//go:build cgo

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

const testUserAlice = "alice"

func TestCommentsSuite(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("CommentsCommand", func(t *testing.T) {
		// Create test issue
		issue := &types.Issue{
			Title:       "Test Issue",
			Description: "Test description",
			Priority:    1,
			IssueType:   types.TypeBug,
			Status:      types.StatusOpen,
		}

		if err := s.CreateIssue(ctx, issue, "test-user"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}

		t.Run("add comment", func(t *testing.T) {
			comment, err := s.AddIssueComment(ctx, issue.ID, testUserAlice, "This is a test comment")
			if err != nil {
				t.Fatalf("Failed to add comment: %v", err)
			}

			if comment.IssueID != issue.ID {
				t.Errorf("Expected issue ID %s, got %s", issue.ID, comment.IssueID)
			}
			if comment.Author != testUserAlice {
				t.Errorf("Expected author alice, got %s", comment.Author)
			}
			if comment.Text != "This is a test comment" {
				t.Errorf("Expected text 'This is a test comment', got %s", comment.Text)
			}
		})

		t.Run("list comments", func(t *testing.T) {
			comments, err := s.GetIssueComments(ctx, issue.ID)
			if err != nil {
				t.Fatalf("Failed to get comments: %v", err)
			}

			if len(comments) != 1 {
				t.Errorf("Expected 1 comment, got %d", len(comments))
			}

			if comments[0].Text != "This is a test comment" {
				t.Errorf("Expected comment text, got %s", comments[0].Text)
			}
		})

		t.Run("multiple comments", func(t *testing.T) {
			_, err := s.AddIssueComment(ctx, issue.ID, "bob", "Second comment")
			if err != nil {
				t.Fatalf("Failed to add second comment: %v", err)
			}

			comments, err := s.GetIssueComments(ctx, issue.ID)
			if err != nil {
				t.Fatalf("Failed to get comments: %v", err)
			}

			if len(comments) != 2 {
				t.Errorf("Expected 2 comments, got %d", len(comments))
			}
		})

		t.Run("comments on non-existent issue", func(t *testing.T) {
			comments, err := s.GetIssueComments(ctx, "bd-nonexistent")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(comments) != 0 {
				t.Errorf("Expected 0 comments for non-existent issue, got %d", len(comments))
			}
		})
	})

	t.Run("AddComment", func(t *testing.T) {
		issue := &types.Issue{
			Title:       "Test Issue for Comment",
			Description: "Test description",
			Priority:    1,
			IssueType:   types.TypeBug,
			Status:      types.StatusOpen,
		}

		if err := s.CreateIssue(ctx, issue, "test-user"); err != nil {
			t.Fatalf("Failed to create issue: %v", err)
		}

		t.Run("comment added via storage API works", func(t *testing.T) {
			comment, err := s.AddIssueComment(ctx, issue.ID, testUserAlice, "Test comment")
			if err != nil {
				t.Fatalf("Failed to add comment: %v", err)
			}

			if comment.Text != "Test comment" {
				t.Errorf("Expected 'Test comment', got %s", comment.Text)
			}

			comments, err := s.GetIssueComments(ctx, issue.ID)
			if err != nil {
				t.Fatalf("Failed to get comments: %v", err)
			}

			if len(comments) != 1 {
				t.Fatalf("Expected 1 comment, got %d", len(comments))
			}
		})
	})
}
